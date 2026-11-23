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
    if config.workload in {cpu_burn, sleep}:
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
function run_ping(job, config, registry, counters, buf, queue):  # buf: MessageBuffer, or NONE for cpu_burn and sleep
    id = job.request_id
    report = start_exec_report(job)                                      # ExecReport: stamped first, right after the pop
    body = () -> protocol.build_body(id, exec_ns(report), report.start_slack_ns, config.response_bytes)
    freeze = () -> worker_pool.freeze_wait(queue)                         # WorkerFreeze: returns at once unless frozen
    slot = cancel_tracker.registry_client(registry, job.client_id)      # none if the registry is full
    cancel_tracker.registry_start(registry, slot, id)                    # CancelMatching: started, not finished
    finish = (completed) -> cancel_tracker.registry_publish_finished(registry, slot, id, completed, counters)

    if config.cancel_mode == cooperative:
        id_check = () -> { freeze(); return cancel_tracker.should_cancel(registry, slot, counters, id) }

        # CancelCheckPoint 1: before processing
        freeze()
        if job.task.cancelled.load():
            finish(false); return Aborted                                # RST wins
        if past_deadline(config, report):                                # PastDeadlineRejection
            counters.rejected_past_deadline.fetch_add(1, relaxed)
            finish(false); return Response(503, body())
        if id_check():
            counters.cancelled_before_processing.fetch_add(1, relaxed)
            finish(false); return Response(503, body())

        # CancelCheckPoint 2: during processing
        if config.workload == cpu_burn:
            outcome = cpu_burner.burn(job.service_us, job.task.cancelled, config.cancel_check_interval_us,
                                      id_check, CHECK_ITER)
        else if config.workload == sleep:
            outcome = sleeper.sleep(job.service_us, job.task.cancelled, id_check)        # QuantumSleep
        else:
            outcome = fill_messages(job, config, buf, job.task.cancelled, id_check)        # MessageWork
        if outcome == CANCELLED_RST:
            finish(false); return Aborted
        if outcome == CANCELLED_ID:
            counters.cancelled_during_processing.fetch_add(1, relaxed)
            finish(false); return Response(503, body())
    else:  # ignore
        if past_deadline(config, report):                                # PastDeadlineRejection: the only check in ignore mode
            counters.rejected_past_deadline.fetch_add(1, relaxed)
            finish(false); return Response(503, body())
        freeze_check = (() -> { freeze(); return false }) if config.freeze_after_ms is set else NONE
        if config.workload == cpu_burn:
            cpu_burner.burn(job.service_us, NONE, config.cancel_check_interval_us, freeze_check, CHECK_ITER)
        else if config.workload == sleep:
            sleeper.sleep(job.service_us, NONE, freeze_check)                              # always COMPLETED
        else:
            fill_messages(job, config, buf, NONE, freeze_check)                            # always COMPLETED

    finish(true)                                                         # completed: feeds completed_before_cancel
    return Response(202, body())
```

`run_ping` never fails. If the registry is full and the client has no slot, `slot` is `none`: `should_cancel` returns
`false` and `finish` does nothing, so the request just runs to `202` (or `Aborted` by RST). `/cancel`s for such a
client are answered `503` by `handle_cancel`, because they would need the same missing slot.

## ExecReport

`run_ping` stamps the ExecReport ([../../protocol/design.md#execreport](../../protocol/design.md#execreport)) as its
very first step, which is right after the worker popped the job:

```
NO_DEADLINE = 2^64 - 1
I64_MIN = -2^63
I64_MAX = 2^63 - 1

function start_exec_report(job):
    t0_mono_ns = monotonic_now_ns()
    t0_wall_ns = wall_clock_unix_ns()                       # u64
    if job.deadline_unix_ns == NO_DEADLINE:
        slack = none
    else:
        slack = job.deadline_unix_ns - t0_wall_ns           # exact signed arithmetic (no wrap), then:
        slack = min(max(slack, I64_MIN), I64_MAX)           # saturate to i64
    return { t0_mono_ns, start_slack_ns: slack }

function exec_ns(report):                                   # u64; read when the body is built
    return monotonic_now_ns() - report.t0_mono_ns
```

- **ExecutionTime** is read when the `202` or `503` body is built, so it covers everything the worker did for the
  request: the ServiceTime burn, sleep or MessageWork, CancelCheckPoints, a WorkerFreeze, and the CancelMatching
  bookkeeping. Queueing delay (push to pop) is outside it.
- **StartSlack** uses the server's wall clock, read once per request. The RequestDeadline is the client's wall clock,
  so clock offset between the machines adds directly to it.
- `Aborted` requests write no body, so their ExecReport is dropped. A job dropped by `queue_close` never reaches
  `run_ping`.

Worked examples (`t0_wall_ns = 1794533185000000000`):

| deadline_unix_ns | start_slack_ns | check |
|---|---|---|
| `1794533190000000000` | `5000000000` | 5 s left |
| `1794533184965000000` | `-35000000` | 35 ms late |
| `1794533185000000000` | `0` | exactly at the deadline |
| `NO_DEADLINE` | none (empty in the body) | no deadline |
| `0` | `-1794533185000000000` | fits in i64, no saturation |

| t0_mono_ns | monotonic_now_ns() when the body is built | exec_ns |
|---|---|---|
| `1000` | `200001000` | `200000000` (a 200 ms sleep) |
| `5000` | `5000` | `0` (503 at CancelCheckPoint 1, same tick) |

## PastDeadlineRejection

With `reject_past_deadline = true`, a worker that pops a request whose RequestDeadline has already passed answers it
`503` instead of running it. The test is made **once**, when the worker pops the request, using the StartSlack the
[ExecReport](#execreport) stamped at that moment; a request that passes it is never checked against its deadline
again and runs to completion (or until cooperative cancellation stops it, as before).

```
function past_deadline(config, report):              # boolean
    return config.reject_past_deadline and report.start_slack_ns is not none and report.start_slack_ns < 0
```

- **Where.** In `cooperative` mode it runs at CancelCheckPoint 1, after the WorkerFreeze wait and the RST check and
  before the `/cancel` match; in `ignore` mode it is the only check, first thing after the pop. So RST still wins over
  it, and a request both late and cancelled by id counts as `rejected_past_deadline`, not as
  `cancelled_before_processing`.
- **Answer.** `503` with the usual PaddedJsonBody and ExecReport: `exec_ns` close to 0 and a negative
  `start_slack_ns`. The client records it `rejected`, which counts against Availability. `finish(false)` publishes it
  to CancelMatching as finished but not completed, as for any `503`.
- **Counted** in `rejected_past_deadline` ([../cancel_tracker/design.md#cancelcounters](../cancel_tracker/design.md#cancelcounters)).
- **Boundary.** Exactly at the deadline (`start_slack_ns = 0`) the request runs: the test is strict. A request without
  a RequestDeadline (`start_slack_ns` none) always runs.
- **Clocks.** StartSlack compares the client's deadline with the server's wall clock, so clock offset between the
  machines moves the boundary by that much.
- **Off** (`false`, the default): never rejects; behavior is unchanged.

Worked examples, `reject_past_deadline = true`:

| `cancel_mode` | `start_slack_ns` at the pop | RST flag | `/cancel` matched | Result |
|---|---|---|---|---|
| cooperative | 5_000_000_000 | no | no | runs |
| cooperative | 0 | no | no | runs (strict `< 0`) |
| cooperative | -1 | no | no | `503`, `rejected_past_deadline += 1` |
| cooperative | -1 | yes | — | `Aborted` (RST wins) |
| cooperative | -1 | no | yes | `503`, `rejected_past_deadline += 1` (checked first) |
| cooperative | none (no RequestDeadline) | no | no | runs |
| ignore | -35_000_000 | (not read) | (not read) | `503`, `rejected_past_deadline += 1` |
| ignore | 1 | (not read) | (not read) | runs to completion, never checked again |

With `reject_past_deadline = false` every row that is not `Aborted` or a `/cancel` match runs.

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

With `workload = cpu_burn` or `sleep`, `exponential_us` is called **exactly once for each request that parses and matches
the binding, before it is queued and before any cancellation check**, in both modes, inside `admit_ping`. With
`workload = message_buffer` it is never called, and the connection's `rng` is not used. A connection admits
its requests one at a time in the order they were fully received. The `k`-th such request on connection `c`
therefore always gets the `k`-th sample of that connection's stream, whatever the cancellation timing, whatever
`n_worker_threads` is, and whichever worker runs it. A `400` request draws no sample.

## CancelCheckPoints

In `cooperative` mode the worker running the request checks for cancellation at these points:

| # | When | RST flag | `/cancel` (CancelMatching) |
|---|---|---|---|
| 1 | When a worker has popped the job, before the burn | checked first, then → `Aborted` | after the [PastDeadlineRejection](#pastdeadlinerejection) test → `503`, `cancelled_before_processing += 1` |
| 2 | During the burn (`cpu_burn`) | every `cancel_check_interval_us` → `Aborted` | every `CHECK_ITER` spin units → `503`, `cancelled_during_processing += 1` |
| 2 | During the sleep (`sleep`) | after each wake-up that does not reach the deadline → `Aborted` | after each such wake-up, after the RST check → `503`, `cancelled_during_processing += 1` |
| 2 | During MessageWork (`message_buffer`), before the buffer is written | after each message → `Aborted` | after each message → `503`, `cancelled_during_processing += 1` |
| (3) | Before the response is written | checked by the Connection Handler ([response writing](../connection_handler/design.md#response-writing)) | not checked: a completed burn is final |

A request reset or cancelled while it waits in the WorkQueue is caught at check point 1 when its turn comes. It
still occupies its place in the line until then, but costs no burn.

In `ignore` mode no check point runs (only the [PastDeadlineRejection](#pastdeadlinerejection) test, when enabled, at the pop). The RST flag is never read and `handle_cancel` throws away
every id, so `should_cancel` is never needed.

**WorkerFreeze checks.** Every check of a running request first calls `worker_pool.freeze_wait(queue)`
([../worker_pool/design.md#workerfreeze](../worker_pool/design.md#workerfreeze)), which returns at once unless the
queue is frozen, and then blocks until `queue_close`. In `cooperative` mode it runs at the start of check point 1
and inside `id_check`, so at every check of check point 2. In `ignore` mode, when `freeze_after_ms` is set, the burn,
the sleep and the MessageWork get a `freeze_check` in place of `id_check` that only waits and always answers
`false`, so it never cancels anything. The freeze therefore stops a running request at its next check:

| Workload | Next check after `freeze_at_ns` |
|---|---|
| `cpu_burn` | the next `CHECK_ITER` spin units |
| `sleep` | the next wake-up that does not reach the deadline (at most one 20 ms quantum) |
| `message_buffer` | after the next message, before the buffer is written |

After `queue_close` ends the wait, the check goes on as usual: the RST flag, then CancelMatching.

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
| `202` | `run_ping` | Burn or MessageWork completed | PaddedJsonBody of `response_bytes`, with the [ExecReport](#execreport) |
| `503` | `run_ping` | `/cancel` matched at check point 1 or 2 | PaddedJsonBody of `response_bytes`, with the [ExecReport](#execreport) |
| `503` | `run_ping` | [PastDeadlineRejection](#pastdeadlinerejection): popped after its RequestDeadline | PaddedJsonBody of `response_bytes`, with the [ExecReport](#execreport) |
| `400` | `admit_ping` | Unparseable body or client id mismatch | empty |
| none (`Aborted`) | `run_ping` | RST at check point 1 or 2 | — |
