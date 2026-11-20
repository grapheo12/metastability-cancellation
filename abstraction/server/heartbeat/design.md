# Heartbeat — Design

## HeartbeatSender

```
procedure serve_heartbeat(task, config, queue):
    interval_ns = config.heartbeat_interval_ms * 1_000_000
    send HEADERS(:status 200, content-type text/plain)          # no content-length, no END_STREAM
    seq = 0                                                     # u64
    loop:
        worker_pool.freeze_wait(queue)                          # WorkerFreeze: blocks for good once frozen
        if task.cancelled.load() or the server is shutting down: return
        send DATA(protocol.build_heartbeat_line(seq)) and flush # one line, written now
        if that write failed: return                           # stream reset or connection gone
        seq = seq + 1
        wait interval_ns, ending early if the stream ends or the server shuts down
```

- **One stream, one sender.** Each `GET /heartbeat` stream gets its own sender on the stream's coroutine. A reader
  that reconnects gets a new stream with `seq` from 0.
- **Spacing.** The wait starts after each write, so lines are `heartbeat_interval_ms` apart plus the write time. The
  first line is written at once.
- **WorkerFreeze.** `freeze_wait` returns at once unless the queue is frozen
  ([../worker_pool/design.md#workerfreeze](../worker_pool/design.md#workerfreeze)). Once it is, the sender blocks
  before its next line and writes no more: the stream stays open but silent, as a wedged server's would.
- **Never queued.** The sender does not push onto the WorkQueue, take a worker, touch the CancelRegistry or count
  anything. A long queue does not delay it.
- **Shutdown.** `queue_close` releases a frozen sender, and the shutdown check then ends it. A sender waiting
  between lines wakes when its connection is closed. It writes nothing during shutdown.

Worked example, `heartbeat_interval_ms = 500`, `freeze_after_ms` set so the queue freezes at 1200 ms, times in ms
since the stream opened, writes taking no time:

| Time | Event | Line written | Check |
|---|---|---|---|
| 0 | stream opens | `hb 0` | first line at once |
| 500 | wait ends | `hb 1` | 0 + 500 |
| 1000 | wait ends | `hb 2` | 500 + 500 |
| 1200 | queue freezes | none | not a line time |
| 1500 | wait ends; `freeze_wait` blocks | none | frozen since 1200 |
| any later | still blocked | none | only `queue_close` releases it |
| no `freeze_after_ms` | — | a line every 500 ms | `freeze_wait` never blocks |

## Failure and edge cases

| Situation | Behavior |
|---|---|
| Reader resets the stream | The next write fails, or the wait ends; the sender returns |
| Connection closes | Same as a reset |
| Server shuts down while frozen | `queue_close` releases `freeze_wait`; the sender sees shutdown and returns without writing |
| Many readers | One sender per stream; they share nothing but the queue's freeze state |
| `GET /heartbeat` with a body | The body is ignored |
