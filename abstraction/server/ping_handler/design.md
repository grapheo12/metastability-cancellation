# Ping Handler — Design

## handle_ping

```
function handle_ping(task, config, rng, cancel_ctx):
    st = cancel_ctx.state
    id = protocol.parse_body(task.body)
    if id is ParseError:
        return Response(400, empty)

    (seq, client_id) = protocol.split_request_id(id)       # client_id = id >> 32
    if st.client_id is unbound:
        st.client_id = client_id                           # see Client binding
    else if st.client_id != client_id:
        return Response(400, empty)

    service_us = sampling.exponential_us(rng, config.response_rate)      # see Sampling order
    finish = () -> cancel_tracker.registry_publish_finished(cancel_ctx.registry, st, id)   # FinishedWatermark

    if config.cancel_mode == cooperative:
        id_check = () -> cancel_tracker.should_cancel(st, cancel_ctx.registry, cancel_ctx.counters, id)

        # CancelCheckPoint 1: before processing
        if task.cancelled.load():
            finish(); return Aborted                                     # RST wins
        if id_check():
            cancel_ctx.counters.cancelled_before_processing.fetch_add(1, relaxed)
            finish(); return Response(503, protocol.build_body(id, config.response_bytes))

        # CancelCheckPoint 2: during processing
        outcome = cpu_burner.burn(service_us, task.cancelled, config.cancel_check_interval_us,
                                  id_check, CHECK_ITER)
        if outcome == CANCELLED_RST:
            finish(); return Aborted
        if outcome == CANCELLED_ID:
            cancel_ctx.counters.cancelled_during_processing.fetch_add(1, relaxed)
            finish(); return Response(503, protocol.build_body(id, config.response_bytes))
    else:  # ignore
        cpu_burner.burn(service_us, NONE, config.cancel_check_interval_us, NONE, CHECK_ITER)

    st.last_completed_id = id                                            # in both modes
    finish()
    return Response(202, protocol.build_body(id, config.response_bytes))
```

## Client binding

The first `/ping` on a connection whose body parses binds `st.client_id = id >> 32`. That client's
CancelInbox is first drained at this same request's CancelCheckPoint 1, so cancels registered
before the client connected are picked up. A later `/ping` with a different client id is a protocol
violation: it gets `400`, draws no ServiceTime sample, and does not change the state.

## Sampling order

`exponential_us` is called **exactly once for each request that parses and matches the binding,
before any cancellation check**, in both modes. The `k`-th such request on connection `c` therefore
always gets the `k`-th sample of that connection's stream, whatever the cancellation timing. A
`400` request draws no sample.

## CancelCheckPoints

In `cooperative` mode the worker checks for cancellation at these points:

| # | When | RST flag | `/cancel` (CancelMatching) |
|---|---|---|---|
| 1 | After the previous request finished, as this request is about to be processed | checked first, then → `Aborted` | → `503`, `cancelled_before_processing += 1` |
| 2 | During the burn | every `cancel_check_interval_us` → `Aborted` | every `CHECK_ITER` spin units → `503`, `cancelled_during_processing += 1` |
| (3) | Before the response is written | checked by the Connection Handler ([response writing](../connection_handler/design.md#response-writing)) | not checked: a completed burn is final |

In `ignore` mode no check point runs. The RST flag is never read and `handle_cancel` throws away
every id, so `should_cancel` is never needed.

## last_completed_id

The id is set only when the burn **completes** (`202`), never for `503`, `Aborted` or `400`. Ids
arrive in increasing order, so `last_completed_id` only grows. CancelMatching uses it to tell
"already completed" cancels from stale ones
([../cancel_tracker/design.md#cancelmatching](../cancel_tracker/design.md#cancelmatching)).

## FinishedWatermark

`finish()` publishes the request's id as the client's FinishedWatermark
([../cancel_tracker/design.md#finishedwatermark](../cancel_tracker/design.md#finishedwatermark)). It runs **once
for every request that parses and matches the binding**, on every way out of `handle_ping` after the ServiceTime
is drawn, in both modes, and before the response is returned for writing:

| Outcome | `last_completed_id` | FinishedWatermark |
|---|---|---|
| `202` (burn completed) | set to `id` | published |
| `503` (`/cancel` matched at check point 1 or 2) | unchanged | published |
| `Aborted` (RST at check point 1 or during the burn) | unchanged | published |
| `400` (unparseable, or client id mismatch) | unchanged | not published |

A `202` that the Connection Handler then drops because of a late RST (check point 3) has still finished its burn,
so its id is already published. Publishing before the write means a `/cancel` answered after the client
received its response never reports an older id for that request.

Worked example for client 0 in `cooperative` mode, `/cancel` for id 1 registered before request 1 starts:

| Request | Outcome | FinishedWatermark after |
|---|---|---|
| 0 | `202` | 0 |
| 1 | `503` at check point 1 | 1 |
| 2 | RST during the burn → `Aborted` | 2 |
| 3 | `202` | 3 |

## Response status

| Status | When | Body |
|---|---|---|
| `202` | Burn completed | PaddedJsonBody of `response_bytes` |
| `503` | `/cancel` matched at check point 1 or 2 | PaddedJsonBody of `response_bytes` |
| `400` | Unparseable body or client id mismatch | empty |
