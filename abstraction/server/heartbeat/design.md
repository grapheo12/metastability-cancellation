# Heartbeat — Design

## HeartbeatSender

```
procedure serve_heartbeat(task, config, queue):
    interval_ns = config.heartbeat_interval_ms * 1_000_000
    send HEADERS(:status 200, content-type text/plain)          # no content-length, no END_STREAM
    seq = 0                                                     # u64
    loop:
        worker_pool.freeze_wait(queue)                          # WorkerFreeze: for good; StalledState: until it ends
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
- **StalledState.** With a ServiceScenario, `freeze_wait` also blocks while the server is in a StalledState, but only
  until the state is left ([../worker_pool/design.md#scenariostall](../worker_pool/design.md#scenariostall)). The
  sender then writes its next line at once, with `seq` continuing from where it stopped: the stream is silent for
  the stall, as a server that stops and recovers would be, and no line is written for the time missed.
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

Worked example, `heartbeat_interval_ms = 500`, a ServiceScenario in a StalledState from 1200 ms to 2700 ms of the
same time base, writes taking no time:

| Time | Event | Line written | Check |
|---|---|---|---|
| 0, 500, 1000 | as above | `hb 0`, `hb 1`, `hb 2` | |
| 1200 | the stall starts | none | not a line time |
| 1500 | wait ends; `freeze_wait` blocks until `next_change_at` = 2700 | none | stalled |
| 2700 | the stall ends; `freeze_wait` returns | `hb 3` | at once, `seq` continues |
| 3200 | wait ends | `hb 4` | 2700 + 500 |
| a stall from 1200 to 1400 | the sender is waiting between lines | `hb 3` at 1500 | a stall between two line times is not seen |

## Failure and edge cases

| Situation | Behavior |
|---|---|
| Reader resets the stream | The next write fails, or the wait ends; the sender returns |
| Connection closes | Same as a reset |
| Server shuts down while frozen | `queue_close` releases `freeze_wait`; the sender sees shutdown and returns without writing |
| Many readers | One sender per stream; they share nothing but the queue's freeze state |
| A StalledState that never ends (the last schedule entry) | Same as a WorkerFreeze: silent until `queue_close` |
| Server shuts down during a StalledState | `queue_close` releases `freeze_wait`; the sender sees shutdown and returns without writing |
| `GET /heartbeat` with a body | The body is ignored |

## Design specific to Go

Implementation: `implementation/go` (module `testbed`, Go 1.21, `golang.org/x/net v0.17.0`).
Build and test with `CGO_ENABLED=0 go build ./...` / `CGO_ENABLED=0 go test ./...`.

### Go choices

- File `internal/server/heartbeat.go`. `serve_heartbeat` is `serveHeartbeat(w, done, task, cfg, q)`, run on the stream's x/net/http2 handler goroutine. `done` is `r.Context().Done()`, which fires on RST_STREAM or when the connection closes; seeing it sets the task's RST flag and returns.
- The `200` is `w.WriteHeader(protocol.StatusStreamOK)` with `content-type: text/plain` and no `content-length`, so x/net/http2 sends the body as DATA frames without END_STREAM. Each line is one `w.Write` followed by `http.Flusher.Flush`.
- The wait is a fresh `time.Timer` per line in a `select` with `done`. "Server is shutting down" is `q.Closed()`, checked right after `q.FreezeWait()`.
- Tests: `heartbeat_test.go` `TestHeartbeatStreamAndFreeze` runs a server with `HeartbeatIntervalMs: 100` and `FreezeAfterMs: 400`: 3 to 6 lines in 350 ms before any `/ping` (the queue is not armed), no line more than 550 ms after the arming `/ping`, `Shutdown` returning promptly while the sender is frozen, and `405` for `POST /heartbeat`. `TestValidateHeartbeatInterval` covers validation.

### Traceability

Generated from the `// Design:` tags in the code by `implementation/spec_tools/tracegen.py` (see `AGENTS.md`). Each link points at the first code line after the tag.
Regenerate from the repository root with `python3 implementation/spec_tools/tracegen.py --lang Go --ext .go --comment // --write`.

<!-- trace:begin -->
| Design subsection | Implementation (file:line) |
|---|---|
| [HeartbeatSender](#heartbeatsender) | [heartbeat.go:17](../../../implementation/go/internal/server/heartbeat.go#L17), [heartbeat_test.go:33](../../../implementation/go/internal/server/heartbeat_test.go#L33), [workerpool.go:208](../../../implementation/go/internal/server/workerpool.go#L208) |
| [Failure and edge cases](#failure-and-edge-cases) | [heartbeat.go:28](../../../implementation/go/internal/server/heartbeat.go#L28) |
<!-- trace:end -->
