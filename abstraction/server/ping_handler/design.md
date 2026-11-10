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

    service_us = sampling.exponential_us(rng, config.response_rate)      # see Sampling order
    return PingJob(task = task, request_id = id, client_id = client_id,
                   service_us = service_us, reply = new OneShot)
```

## run_ping

```
function run_ping(job, config, registry, counters):
    id = job.request_id
    slot = cancel_tracker.registry_client(registry, job.client_id)      # none if the registry is full
    finish = (completed) -> cancel_tracker.registry_publish_finished(registry, slot, id, completed)

    if config.cancel_mode == cooperative:
        id_check = () -> cancel_tracker.should_cancel(registry, slot, counters, id)

        # CancelCheckPoint 1: before processing
        if job.task.cancelled.load():
            finish(false); return Aborted                                # RST wins
        if id_check():
            counters.cancelled_before_processing.fetch_add(1, relaxed)
            finish(false); return Response(503, protocol.build_body(id, config.response_bytes))

        # CancelCheckPoint 2: during processing
        outcome = cpu_burner.burn(job.service_us, job.task.cancelled, config.cancel_check_interval_us,
                                  id_check, CHECK_ITER)
        if outcome == CANCELLED_RST:
            finish(false); return Aborted
        if outcome == CANCELLED_ID:
            counters.cancelled_during_processing.fetch_add(1, relaxed)
            finish(false); return Response(503, protocol.build_body(id, config.response_bytes))
    else:  # ignore
        cpu_burner.burn(job.service_us, NONE, config.cancel_check_interval_us, NONE, CHECK_ITER)

    finish(true)                                                         # also raises last_completed_id
    return Response(202, protocol.build_body(id, config.response_bytes))
```

`run_ping` never fails. If the registry is full and the client has no slot, `slot` is `none`: `should_cancel` returns
`false` and `finish` does nothing, so the request just runs to `202` (or `Aborted` by RST). `/cancel`s for such a
client are answered `503` by `handle_cancel`, because they would need the same missing slot.

## Client binding

The first `/ping` on a connection whose body parses binds `conn_state.client_id = id >> 32`, in `admit_ping`.
Because the CancelInbox lives in the client's registry slot, not in the connection, cancels registered before the
client connected are picked up at that request's CancelCheckPoint 1. A later `/ping` with a different client id is
a protocol violation: it gets `400` from `admit_ping`, draws no ServiceTime sample, is never queued, and does not
change the state.

## Sampling order

`exponential_us` is called **exactly once for each request that parses and matches the binding,
before it is queued and before any cancellation check**, in both modes, inside `admit_ping`. A connection admits
its requests one at a time in the order they were fully received. The `k`-th such request on connection `c`
therefore always gets the `k`-th sample of that connection's stream, whatever the cancellation timing, whatever
`n_worker_threads` is, and whichever worker runs it. A `400` request draws no sample.

## CancelCheckPoints

In `cooperative` mode the worker running the request checks for cancellation at these points:

| # | When | RST flag | `/cancel` (CancelMatching) |
|---|---|---|---|
| 1 | When a worker has popped the job, before the burn | checked first, then → `Aborted` | → `503`, `cancelled_before_processing += 1` |
| 2 | During the burn | every `cancel_check_interval_us` → `Aborted` | every `CHECK_ITER` spin units → `503`, `cancelled_during_processing += 1` |
| (3) | Before the response is written | checked by the Connection Handler ([response writing](../connection_handler/design.md#response-writing)) | not checked: a completed burn is final |

A request reset or cancelled while it waits in the WorkQueue is caught at check point 1 when its turn comes. It
still occupies its place in the line until then, but costs no burn.

In `ignore` mode no check point runs. The RST flag is never read and `handle_cancel` throws away
every id, so `should_cancel` is never needed.

## last_completed_id

The client's `last_completed_id` ([../cancel_tracker/design.md#clientcancelstate](../cancel_tracker/design.md#clientcancelstate))
is raised only when the burn **completes** (`202`, `finish(true)`), never for `503`, `Aborted` or `400`. It is a
max-update: two requests of one client can finish out of order on different workers, and the value never goes
down. CancelMatching uses it to tell "already completed" cancels from stale ones
([../cancel_tracker/design.md#cancelmatching](../cancel_tracker/design.md#cancelmatching)).

## FinishedWatermark

`finish()` publishes the request's id as the client's FinishedWatermark
([../cancel_tracker/design.md#finishedwatermark](../cancel_tracker/design.md#finishedwatermark)). It runs **once
for every admitted request**, on every way out of `run_ping`, in both modes, and before the response is returned
for writing:

| Outcome | `finish(completed)` | `last_completed_id` | FinishedWatermark |
|---|---|---|---|
| `202` (burn completed) | `finish(true)` | raised to `id` | raised to `id` |
| `503` (`/cancel` matched at check point 1 or 2) | `finish(false)` | unchanged | raised to `id` |
| `Aborted` (RST at check point 1 or during the burn) | `finish(false)` | unchanged | raised to `id` |
| `400` (unparseable, or client id mismatch) | not called: never admitted | unchanged | unchanged |
| Dropped from the WorkQueue at shutdown | not called: never run | unchanged | unchanged |

A `202` that the Connection Handler then drops because of a late RST (check point 3) has still finished its burn,
so its id is already published. Publishing before the write means a `/cancel` answered after the client
received its response never reports an older id for that request.

Worked example for client 0 in `cooperative` mode, `/cancel` for id 1 registered before request 1 starts:

| Request | Outcome | `last_completed_id` after | FinishedWatermark after |
|---|---|---|---|
| 0 | `202` | 0 | 0 |
| 1 | `503` at check point 1 | 0 | 1 |
| 2 | RST during the burn → `Aborted` | 0 | 2 |
| 3 | `202` | 3 | 3 |

## Response status

| Status | Returned by | When | Body |
|---|---|---|---|
| `202` | `run_ping` | Burn completed | PaddedJsonBody of `response_bytes` |
| `503` | `run_ping` | `/cancel` matched at check point 1 or 2 | PaddedJsonBody of `response_bytes` |
| `400` | `admit_ping` | Unparseable body or client id mismatch | empty |
| none (`Aborted`) | `run_ping` | RST at check point 1 or 2 | — |

## Design specific to Go

Implementation: `implementation/go` (module `testbed`, Go 1.21, `golang.org/x/net v0.17.0`).
Build and test with `CGO_ENABLED=0 go build ./...` / `CGO_ENABLED=0 go test ./...`. On recent macOS the dynamic
loader rejects cgo-linked Go 1.21 binaries (missing `LC_UUID`), so cgo is disabled.

### Go choices

- File `internal/server/pinghandler.go`. `admit_ping` is `admitPing(task, cfg, rng, st) (*PingJob, Response)`: a nil job means the returned `Response` (a `400`) is the answer. `run_ping` is `runPing(job, cfg, registry, counters) Response`.
- `admitPing` runs under the connection's admission lock (`connectionHandler.mu`), because x/net/http2 runs one handler goroutine per stream.
- `id_check` is a Go closure over the registry, the slot and the request id, passed to `Burn`. `finish(completed)` is a closure over `PublishFinished`, called explicitly on each way out.
- Counters are incremented with `atomic.Uint64.Add(1)`.
- Tests: `server_test.go` `TestFinishedWatermarkReplies` checks the `/cancel` reply end to end after a `202`, a `503`, and an RST-stopped burn. `workerpool_test.go` `TestResetWhileQueued` checks check point 1 for a job reset while queued.

### Traceability

Generated from the `// Design:` tags in the code by `implementation/spec_tools/tracegen.py` (see `AGENTS.md`). Each link points at the first code line after the tag.
Regenerate from the repository root with `python3 implementation/spec_tools/tracegen.py --lang Go --ext .go --comment // --write`.

<!-- trace:begin -->
| Design subsection | Implementation (file:line) |
|---|---|
| [handle_ping](#handle_ping) | [pinghandler.go:14](../../../implementation/go/internal/server/pinghandler.go#L14) |
| [admit_ping](#admit_ping) | [pinghandler.go:14](../../../implementation/go/internal/server/pinghandler.go#L14) |
| [run_ping](#run_ping) | [pinghandler.go:39](../../../implementation/go/internal/server/pinghandler.go#L39) |
| [Client binding](#client-binding) | [pinghandler.go:22](../../../implementation/go/internal/server/pinghandler.go#L22) |
| [Sampling order](#sampling-order) | [pinghandler.go:30](../../../implementation/go/internal/server/pinghandler.go#L30) |
| [CancelCheckPoints](#cancelcheckpoints) | [pinghandler.go:49](../../../implementation/go/internal/server/pinghandler.go#L49) |
| [last_completed_id](#last_completed_id) | [pinghandler.go:73](../../../implementation/go/internal/server/pinghandler.go#L73) |
| [FinishedWatermark](#finishedwatermark) | [pinghandler.go:43](../../../implementation/go/internal/server/pinghandler.go#L43), [server_test.go:156](../../../implementation/go/internal/server/server_test.go#L156) |
| [Response status](#response-status) | [pinghandler.go:75](../../../implementation/go/internal/server/pinghandler.go#L75) |
<!-- trace:end -->
