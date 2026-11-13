# Ping Handler — Design

## handle_ping

A `POST /ping` goes through two functions, on two different activities:

| Step | Function | Runs on | Uses exclusively | Shares |
|---|---|---|---|---|
| Admit | [admit_ping](#admit_ping) | the request's Connection Handler, one request at a time per connection | the connection's `rng` and `ConnectionCancelState` | — |
| Run | [run_ping](#run_ping) | any worker of the WorkerPool | the PingJob | the client's ClientCancelState (under its lock), CancelCounters |

Between the two the request waits in the WorkQueue
([../worker_pool/design.md#workqueue](../worker_pool/design.md#workqueue)). The Connection Handler writes the
result ([../connection_handler/design.md#response-writing](../connection_handler/design.md#response-writing)).

## admit_ping

```
function admit_ping(task, config, rng, conn_state):
    id = protocol.parse_body(task.body)
    if id is ParseError:
        return Response(400, empty)

    (seq, client_id) = protocol.split_request_id(id)       # client_id = id >> 32
    if conn_state.client_id is unbound:
        conn_state.client_id = client_id                   # see Client binding
    else if conn_state.client_id != client_id:
        return Response(400, empty)

    service_us = 0
    if config.workload == cpu_burn:
        service_us = sampling.exponential_us(rng, config.response_rate)  # see Sampling order
    deadline = protocol.parse_deadline(task.body)          # RequestDeadline; none if missing or malformed
    if deadline is none: deadline = NO_DEADLINE            # 2^64 - 1: after every deadline under edf
    return PingJob(task = task, request_id = id, client_id = client_id,
                   service_us = service_us, deadline_unix_ns = deadline, reply = new OneShot)
```

- **RequestDeadline.** `admit_ping` copies the body's RequestDeadline into the PingJob for the WorkQueue's `edf`
  QueueDiscipline ([../worker_pool/design.md#earliestdeadlinefirst](../worker_pool/design.md#earliestdeadlinefirst)).
  A missing or malformed deadline never rejects the request; it becomes `NO_DEADLINE = 2^64 - 1`. Under `fifo` the
  field is carried but never read. Parsing the deadline draws nothing from the Rng, so the Sampling order is
  unchanged.

## run_ping

```
function run_ping(job, config, registry, counters, buf):        # buf: MessageBuffer, or NONE for cpu_burn
    id = job.request_id
    slot = cancel_tracker.registry_client(registry, job.client_id)      # none if the registry is full
    cancel_tracker.registry_start(registry, slot, id)                    # CancelMatching: started, not finished
    finish = (completed) -> cancel_tracker.registry_publish_finished(registry, slot, id, completed, counters)

    if config.cancel_mode == cooperative:
        id_check = () -> cancel_tracker.should_cancel(registry, slot, counters, id)

        # CancelCheckPoint 1: before processing
        if job.task.cancelled.load():
            finish(false); return Aborted                                # RST wins
        if id_check():
            counters.cancelled_before_processing.fetch_add(1, relaxed)
            finish(false); return Response(503, protocol.build_body(id, config.response_bytes))

        # CancelCheckPoint 2: during processing
        if config.workload == cpu_burn:
            outcome = cpu_burner.burn(job.service_us, job.task.cancelled, config.cancel_check_interval_us,
                                      id_check, CHECK_ITER)
        else:
            outcome = fill_messages(job, config, buf, job.task.cancelled, id_check)        # MessageWork
        if outcome == CANCELLED_RST:
            finish(false); return Aborted
        if outcome == CANCELLED_ID:
            counters.cancelled_during_processing.fetch_add(1, relaxed)
            finish(false); return Response(503, protocol.build_body(id, config.response_bytes))
    else:  # ignore
        if config.workload == cpu_burn:
            cpu_burner.burn(job.service_us, NONE, config.cancel_check_interval_us, NONE, CHECK_ITER)
        else:
            fill_messages(job, config, buf, NONE, NONE)                                    # always COMPLETED

    finish(true)                                                         # completed: feeds completed_before_cancel
    return Response(202, protocol.build_body(id, config.response_bytes))
```

`run_ping` never fails. If the registry is full and the client has no slot, `slot` is `none`: `should_cancel` returns
`false` and `finish` does nothing, so the request just runs to `202` (or `Aborted` by RST). `/cancel`s for such a
client are answered `503` by `handle_cancel`, because they would need the same missing slot.

## MessageWork

The work of a request when `workload = message_buffer`: create `n_messages_per_request` messages, then write them
all into the MessageBuffer ([../message_buffer/design.md](../message_buffer/design.md#messagecursor)).

```
function fill_messages(job, config, buf, cancel_flag, id_check):    # -> COMPLETED | CANCELLED_RST | CANCELLED_ID
    (seq, client_id) = protocol.split_request_id(job.request_id)
    rng = sampling.rng_new(sampling.derive_seed(
              sampling.derive_seed(config.seed, STREAM_SERVER_MESSAGES, client_id), STREAM_SERVER_MESSAGES, seq))
    msgs = []
    for k in 0 ..= config.n_messages_per_request - 1:
        msgs.append(message_buffer.make_tree(rng, config.n_gc_binary_tree_size))     # MessageTree k, in order
        if cancel_flag is not NONE and cancel_flag.load(): return CANCELLED_RST
        if id_check is not NONE and id_check(): return CANCELLED_ID
    message_buffer.push_messages(buf, job.client_id, msgs)      # takes the buffer lock once
    return COMPLETED
```

- **Created before the lock.** Every message is allocated and filled before `push_messages`, so the buffer lock
  covers only the slot writes.
- **Checks after each message.** After every message, the RST flag first, then CancelMatching, the same precedence
  as the burn. A cancelled request drops the messages it created and never writes to the buffer, so cancelled work
  still leaves garbage.
- **No checks after the push.** Once `push_messages` starts, the request completes.
- **Per-request stream.** The trees of a request come from its own Rng, derived from the server `seed` and its
  RequestId ([../../sampling/design.md#seedderivation](../../sampling/design.md#seedderivation)), drawn message by
  message in order. They are the same whichever worker runs the request, whenever it runs; the connection's Rng is
  never touched. A cancelled request stops drawing where it stops creating messages.
- The work takes as long as the allocations take; there is no ServiceTime, and `response_rate` is not used.

Worked example, `n_messages_per_request = 3`, request id `0x0000000200000005` (client 2, seq 5), `cooperative`;
the stream is `derive_seed(derive_seed(seed, STREAM_SERVER_MESSAGES, 2), STREAM_SERVER_MESSAGES, 5)`:

| Situation | Messages created | Buffer writes | Result |
|---|---|---|---|
| No cancel | trees 0, 1, 2 of the request's stream, each of `n_gc_binary_tree_size` nodes | 3 slots from client 2's cursor | `202` |
| `/cancel` registered after check point 1, before the second message's check | 2 | none | `503`, `cancelled_during_processing += 1` |
| RST set after check point 1, before the first message's check | 1 | none | `Aborted` |
| `/cancel` registered before the request starts | 0 (caught at check point 1) | none | `503`, `cancelled_before_processing += 1` |

## Client binding

The first `/ping` on a connection whose body parses binds `conn_state.client_id = id >> 32`, in `admit_ping`.
Because the CancelInbox lives in the client's registry slot, not in the connection, cancels registered before the
client connected are picked up at that request's CancelCheckPoint 1. A later `/ping` with a different client id is
a protocol violation: it gets `400` from `admit_ping`, draws no ServiceTime sample, is never queued, and does not
change the state.

## Sampling order

With `workload = cpu_burn`, `exponential_us` is called **exactly once for each request that parses and matches
the binding, before it is queued and before any cancellation check**, in both modes, inside `admit_ping`. With
`workload = message_buffer` it is never called, and the connection's `rng` is not used. A connection admits
its requests one at a time in the order they were fully received. The `k`-th such request on connection `c`
therefore always gets the `k`-th sample of that connection's stream, whatever the cancellation timing, whatever
`n_worker_threads` is, and whichever worker runs it. A `400` request draws no sample.

## CancelCheckPoints

In `cooperative` mode the worker running the request checks for cancellation at these points:

| # | When | RST flag | `/cancel` (CancelMatching) |
|---|---|---|---|
| 1 | When a worker has popped the job, before the burn | checked first, then → `Aborted` | → `503`, `cancelled_before_processing += 1` |
| 2 | During the burn (`cpu_burn`) | every `cancel_check_interval_us` → `Aborted` | every `CHECK_ITER` spin units → `503`, `cancelled_during_processing += 1` |
| 2 | During MessageWork (`message_buffer`), before the buffer is written | after each message → `Aborted` | after each message → `503`, `cancelled_during_processing += 1` |
| (3) | Before the response is written | checked by the Connection Handler ([response writing](../connection_handler/design.md#response-writing)) | not checked: a completed burn is final |

A request reset or cancelled while it waits in the WorkQueue is caught at check point 1 when its turn comes. It
still occupies its place in the line until then, but costs no burn.

In `ignore` mode no check point runs. The RST flag is never read and `handle_cancel` throws away
every id, so `should_cancel` is never needed.

## FinishedWatermark

`finish(completed)` publishes the request's id as the client's FinishedWatermark and records its outcome for
CancelMatching ([../cancel_tracker/design.md#finishedwatermark](../cancel_tracker/design.md#finishedwatermark)):
`completed` is `true` only for `202`, so only a request whose burn completed can turn a late `/cancel` into
`completed_before_cancel`. It runs **once
for every admitted request**, on every way out of `run_ping`, in both modes, and before the response is returned
for writing:

| Outcome | `registry_start` | `finish(completed)` | A later `/cancel` for this id | FinishedWatermark |
|---|---|---|---|---|
| `202` (burn completed) | called | `finish(true)` | `completed_before_cancel += 1` | raised to `id` |
| `503` (`/cancel` matched at check point 1 or 2) | called | `finish(false)` | dropped, not counted | raised to `id` |
| `Aborted` (RST at check point 1 or during the burn) | called | `finish(false)` | dropped, not counted | raised to `id` |
| `400` (unparseable, or client id mismatch) | not called: never admitted | not called | aged out, not counted | unchanged |
| Dropped from the WorkQueue at shutdown | not called: never run | not called | not counted | unchanged |

A `202` that the Connection Handler then drops because of a late RST (check point 3) has still finished its burn,
so its id is already published. Publishing before the write means a `/cancel` answered after the client
received its response never reports an older id for that request.

Worked example for client 0 in `cooperative` mode, `/cancel` for id 1 registered before request 1 starts:

| Request | Outcome | `finish` | FinishedWatermark after |
|---|---|---|---|
| 0 | `202` | `finish(true)` | 0 |
| 1 | `503` at check point 1 | `finish(false)` | 1 |
| 2 | RST during the burn → `Aborted` | `finish(false)` | 2 |
| 3 | `202` | `finish(true)` | 3 |

## Response status

| Status | Returned by | When | Body |
|---|---|---|---|
| `202` | `run_ping` | Burn or MessageWork completed | PaddedJsonBody of `response_bytes` |
| `503` | `run_ping` | `/cancel` matched at check point 1 or 2 | PaddedJsonBody of `response_bytes` |
| `400` | `admit_ping` | Unparseable body or client id mismatch | empty |
| none (`Aborted`) | `run_ping` | RST at check point 1 or 2 | — |

## Design specific to Go

Implementation: `implementation/go` (module `testbed`, Go 1.21, `golang.org/x/net v0.17.0`).
Build and test with `CGO_ENABLED=0 go build ./...` / `CGO_ENABLED=0 go test ./...`. On recent macOS the dynamic
loader rejects cgo-linked Go 1.21 binaries (missing `LC_UUID`), so cgo is disabled.

### Go choices

- File `internal/server/pinghandler.go`. `admit_ping` is `admitPing(task, cfg, rng, st) (*PingJob, Response)`: a nil job means the returned `Response` (a `400`) is the answer. `run_ping` is `runPing(job, cfg, registry, counters, buf) Response`, with `buf` nil for `cpu_burn`.
- `admitPing` runs under the connection's admission lock (`connectionHandler.mu`), because x/net/http2 runs one handler goroutine per stream.
- `runPing` calls `registry.Start` before anything else. `id_check` is a Go closure over the registry, the slot and the request id, passed to `Burn`. `finish(completed)` is a closure over `PublishFinished(slot, id, completed, counters)`, called explicitly on each way out.
- MessageWork's stream is `sampling.NewRng(sampling.DeriveSeed(sampling.DeriveSeed(cfg.Seed, StreamServerMessages, clientID), StreamServerMessages, seq))`, with `seq` and `clientID` from `protocol.SplitRequestID`; the trees come from `makeTree`. `TestMessageBufferEndToEnd` and `TestFillMessagesCancel` rebuild the expected trees from the same stream.
- `fill_messages` is `fillMessages(job, cfg, buf, cancelled *atomic.Bool, idCheck func() bool) BurnOutcome`, reusing the CPU Burner's `BurnOutcome` values. `nil` arguments are the design's `NONE`. Messages are collected in a slice with capacity `n_messages_per_request`, then written with one `buf.Push`.
- `admitPing` leaves `ServiceUs` at 0 and does not touch the `Rng` when the workload is `message_buffer`.
- `admitPing` reads the RequestDeadline with `protocol.ParseDeadline` after the ServiceTime draw; `ok == false` becomes `NoDeadline`. `newPingJob` takes the deadline as its last argument.
- Counters are incremented with `atomic.Uint64.Add(1)`.
- Tests: `server_test.go` `TestFinishedWatermarkReplies` checks the `/cancel` reply end to end after a `202`, a `503`, and an RST-stopped burn. `workerpool_test.go` `TestResetWhileQueued` checks check point 1 for a job reset while queued. `TestFillMessagesCancel` checks the MessageWork worked example: `/cancel` at the second check, RST checked first, and that a cancelled request leaves the buffer untouched.

### Traceability

Generated from the `// Design:` tags in the code by `implementation/spec_tools/tracegen.py` (see `AGENTS.md`). Each link points at the first code line after the tag.
Regenerate from the repository root with `python3 implementation/spec_tools/tracegen.py --lang Go --ext .go --comment // --write`.

<!-- trace:begin -->
| Design subsection | Implementation (file:line) |
|---|---|
| [handle_ping](#handle_ping) | [pinghandler.go:16](../../../implementation/go/internal/server/pinghandler.go#L16) |
| [admit_ping](#admit_ping) | [pinghandler.go:16](../../../implementation/go/internal/server/pinghandler.go#L16), [server_test.go:481](../../../implementation/go/internal/server/server_test.go#L481) |
| [run_ping](#run_ping) | [pinghandler.go:49](../../../implementation/go/internal/server/pinghandler.go#L49) |
| [MessageWork](#messagework) | [pinghandler.go:101](../../../implementation/go/internal/server/pinghandler.go#L101), [server_test.go:325](../../../implementation/go/internal/server/server_test.go#L325), [server_test.go:365](../../../implementation/go/internal/server/server_test.go#L365) |
| [Client binding](#client-binding) | [pinghandler.go:24](../../../implementation/go/internal/server/pinghandler.go#L24) |
| [Sampling order](#sampling-order) | [pinghandler.go:33](../../../implementation/go/internal/server/pinghandler.go#L33) |
| [CancelCheckPoints](#cancelcheckpoints) | [pinghandler.go:60](../../../implementation/go/internal/server/pinghandler.go#L60), [server_test.go:365](../../../implementation/go/internal/server/server_test.go#L365) |
| [FinishedWatermark](#finishedwatermark) | [pinghandler.go:54](../../../implementation/go/internal/server/pinghandler.go#L54), [server_test.go:159](../../../implementation/go/internal/server/server_test.go#L159) |
| [Response status](#response-status) | [pinghandler.go:93](../../../implementation/go/internal/server/pinghandler.go#L93) |
<!-- trace:end -->
