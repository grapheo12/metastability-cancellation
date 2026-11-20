# Connection Handler — Design

## Shared state

```
stream_table : map stream_id -> StreamTask, for tasks admitted or being answered (used by the reader)
closing      : atomic boolean, initially false
rng          : this connection's Rng                      # used only by admission
conn_state   : ConnectionCancelState (client_id unbound)   # used only by admission
registry     : the process-wide CancelRegistry
queue        : the process-wide WorkQueue
```

`ConnectionCancelState` is defined in [../cancel_tracker/api.md](../cancel_tracker/api.md#connectioncancelstate).
`registry` and `queue` are passed in by the Listener. `rng` and `conn_state` are touched only by
[Admission](#admission), which runs one request at a time.

## FrameReader

The HTTP/2 layer (frame parsing, HPACK, flow control, SETTINGS, PING, WINDOW_UPDATE) may come from a
library. This design fixes only how **request completion** and **RST_STREAM** are handled.

```
procedure frame_reader():
    loop:
        event = next_http2_event(conn)          # blocks
        match event:
            RequestComplete(stream_id, headers, body):   # END_STREAM seen
                task = StreamTask(stream_id, headers[":method"], headers[":path"], body, cancelled=false)
                stream_table[stream_id] = task
                match route(task):                       # never waits for a worker
                    Immediate(response):
                        write_response(task, response)
                        remove stream_id from stream_table
                    Queued(job):
                        spawn stream_responder(task, job)
            StreamReset(stream_id, error_code):
                if stream_id in stream_table:
                    stream_table[stream_id].cancelled.store(true)
                # Reset of an unknown or finished stream is ignored.
            ConnectionClosed or GoAway or ProtocolError:
                close()
                return

procedure stream_responder(task, job):
    response = job.reply.take()                  # waits for the worker, or for queue_close
    write_response(task, response)
    remove task.stream_id from stream_table
```

A request that is reset **before** END_STREAM never becomes a StreamTask, so it is never processed.

The reader itself never waits for a worker: only the stream's responder does. A responder is anything that
runs after `job.reply` is filled without holding up the reader (a coroutine, a thread, or a completion callback).

## Admission

```
function route(task):                                     # Immediate(Response) | Queued(PingJob) | Streamed
    if task.path == "/ping":
        if task.method != "POST":   return Immediate(Response(405, empty))
        r = ping_handler.admit_ping(task, config, rng, conn_state)
        if r is a Response: return Immediate(r)          # 400: never queued, no sample drawn
        if worker_pool.queue_push(queue, r) is Closed:
            return Immediate(Aborted)                    # server shutting down
        return Queued(r)
    if task.path == "/cancel":
        if task.method != "DELETE": return Immediate(Response(405, empty))
        return Immediate(cancel_tracker.handle_cancel(task.body, config, registry))   # never queued
    if task.path == "/heartbeat":
        if task.method != "GET":    return Immediate(Response(405, empty))
        spawn heartbeat.serve_heartbeat(task, config, queue)   # its own coroutine; never queued
        return Streamed                                  # the sender writes the stream; nothing else does
    return Immediate(Response(404, empty))
```

- **One at a time, in completion order.** `admit_ping` and `queue_push` for one connection run as one step, one
  request at a time, in the order the requests completed. In the reference design the FrameReader does both
  itself, so this holds trivially. If an HTTP/2 library delivers each completed request on its own per-stream
  handler instead, those handlers must take a per-connection **admission lock** around `admit_ping` plus
  `queue_push`. This is what keeps the connection's ServiceTime sequence
  ([../ping_handler/design.md#sampling-order](../ping_handler/design.md#sampling-order)) and its push order
  ([../worker_pool/design.md#ordering](../worker_pool/design.md#ordering)) tied to arrival order.
- **`/heartbeat` is never queued.** It gets its own coroutine at once, so the FrameReader goes on reading, and
  the HeartbeatSender writes the stream until it ends ([../heartbeat/design.md#heartbeatsender](../heartbeat/design.md#heartbeatsender)).
  The stream's RST flag is set as for any stream, and the sender reads it.
- **`/cancel` is never queued.** `handle_cancel` is lock-free and short, so answering it on the reader cannot delay
  the reader by more than one registry push.

## Routing

[Admission](#admission) is the router. The table summarizes it:

| `:path` | `:method` | Handled by | Queued | Answer |
|---|---|---|---|---|
| `/ping` | `POST` | `admit_ping`, then a worker's `run_ping` | yes, if admitted | `202`, `503`, nothing, or `400` from admission |
| `/ping` | other | router | no | `405`, empty |
| `/cancel` | `DELETE` | `handle_cancel` | never | `202`, `400` or `503` |
| `/cancel` | other | router | no | `405`, empty |
| `/heartbeat` | `GET` | `heartbeat.serve_heartbeat` on its own coroutine | never | `200`, then a HeartbeatStream |
| `/heartbeat` | other | router | no | `405`, empty |
| other | any | router | no | `404`, empty |

## Response writing

```
procedure write_response(task, response):
    if response is Aborted: return                                   # RST skip/abort, or dropped at shutdown
    if config.cancel_mode == cooperative and task.cancelled.load(): return
    worker_count = config.n_worker_threads if task.path == "/ping" and response.status in {202, 503} else none
    send HEADERS(:status, content-type if body non-empty, content-length,
                 x-worker-threads: decimal(worker_count) if worker_count is not none)       # WorkerCount
    send DATA(body, END_STREAM)
    # If the stream was reset, the HTTP/2 layer drops these frames. Any write error
    # on a reset stream is ignored and does not close the connection.
```

Every `/ping` answer from a worker (`202`, or `503` from a `/cancel` match) carries the WorkerCount header
([../../protocol/design.md#workercount](../../protocol/design.md#workercount)); `400`, `404`, `405` and `/cancel`
answers do not.

`503` responses from a `/cancel` match are written like any other response. The client is still
waiting for them. Writing hands the frames to the connection's writer; it never waits for the peer, so a write
from the reader does not stall frame reading.

## Concurrency requirement

The FrameReader **must keep running while workers burn**. Workers run on their own activities
([../worker_pool/design.md#concurrency-requirement](../worker_pool/design.md#concurrency-requirement)), and the
reader never waits on one, so `RST_STREAM` and `/cancel` are seen while every worker is busy. The RST flag is an
atomic boolean: the reader writes it and the worker or burner reads it, with no further locking.

`/cancel` requests do not need the reader of the target client's connection. They arrive on the
canceller's own connection, are answered by that connection's handler, and reach the target's worker through the
lock-free CancelRegistry.

## State machine (per `/ping` StreamTask)

```mermaid
stateDiagram-v2
  [*] --> Received: RequestComplete
  Received --> Responded400: admit_ping rejects (unparseable or wrong client)
  Received --> Dropped: queue_push Closed (shutdown)
  Received --> Queued: admitted and pushed
  Queued --> Queued: RST (cancelled=true)
  Queued --> Dropped: queue_close (shutdown)
  Queued --> Running: a worker pops
  Running --> Running: RST (cancelled=true)
  Running --> Responded202: burn completed, written
  Running --> Responded503: /cancel matched (before or during burn), written
  Running --> Dropped: Aborted (RST), or RST before write (cooperative), or write dropped by H2 layer
  Responded400 --> [*]
  Responded202 --> [*]
  Responded503 --> [*]
  Dropped --> [*]
```

## Connection close

`close()` or a GOAWAY/EOF from the peer sets `closing`, sends `GOAWAY` if the connection is still open, stops
reading, and sets the RST flag of every task still in `stream_table`. Those tasks' jobs stay in the WorkQueue or keep
running: in `cooperative` mode a worker skips or stops them at their next CancelCheckPoint, in `ignore` mode they
burn to the end. Their writes are dropped. The handler returns once the reader and every stream responder have
ended and the socket is closed. Each responder ends when its job's `reply` is filled, by a worker or by
`queue_close` at server shutdown.

The client's registry entry and its ClientCancelState stay (entries are never deleted). They are shared by the
client, not owned by the connection.

## Design specific to Go

Implementation: `implementation/go` (module `testbed`, Go 1.21, `golang.org/x/net v0.17.0`).
Build and test with `CGO_ENABLED=0 go build ./...` / `CGO_ENABLED=0 go test ./...`. On recent macOS the dynamic
loader rejects cgo-linked Go 1.21 binaries (missing `LC_UUID`), so cgo is disabled.

### Go choices

- Route `Streamed` is `Response{Streamed: true}`. `frameReader` then calls `serveHeartbeat(w, r.Context().Done(), task, cfg, queue)` on the stream's own handler goroutine, which x/net/http2 already gives every stream, so the design's `spawn` needs no extra goroutine; the handler returns when the sender does. `TestHeartbeatStreamAndFreeze` checks the `405` for `POST /heartbeat`.
- File `internal/server/connhandler.go`.
- The FrameReader is split in two. The x/net/http2 read loop does the framing, and each stream's `http.Handler` goroutine (`frameReader`) does the rest: it runs once the body is read to END_STREAM, builds the `StreamTask`, routes it, and for a queued `/ping` acts as the stream responder. It turns `r.Context().Done()`, which fires on RST_STREAM or when the connection closes, into the RST flag. Each handler goroutine holds its own `*StreamTask`, which takes the place of `stream_table`.
- Stream handlers of one connection run concurrently, so Admission takes the per-connection admission lock `mu` around `admitPing` plus `WorkQueue.Push`, as the design requires for per-stream handlers. Go does not promise that handler goroutines reach the lock in exact END_STREAM order. Clients keep one request outstanding, so in practice this does not matter.
- The router returns `(Response, *PingJob)`; a non-nil job means queued. `/cancel` runs `HandleCancel` on the stream's goroutine and is never queued.
- The worker sends its `Response` through the job's 1-buffered `reply` channel, and the handler goroutine writes it, because x/net/http2 only allows writes from the handler goroutine. "Write nothing" (`Aborted`, or RST in cooperative mode) is `panic(http.ErrAbortHandler)`. That resets the stream instead of letting the library send an empty 200.
- Connection close: `close()` sets `closing` under `mu` and closes the socket. `run` then waits on a `sync.WaitGroup` of live stream handlers; `addStream` refuses new ones once `closing` is set, so `Add` never races with `Wait`.
- WorkerCount: `writeResponse` sets `protocol.WorkerThreadsHeader` to `strconv.FormatUint(cfg.NWorkerThreads)` when `task.Path == "/ping"` and the status is `202` or `503`, before `WriteHeader`. `TestWorkerCountHeader` checks it on `202` and `503` and its absence on `/cancel`, `400` and `405`.
- The RST flag is `sync/atomic.Bool`. `maxConcurrentStreams` (4096, advertised in SETTINGS) bounds the requests one connection can have admitted at once.

### Traceability

Generated from the `// Design:` tags in the code by `implementation/spec_tools/tracegen.py` (see `AGENTS.md`). Each link points at the first code line after the tag.
Regenerate from the repository root with `python3 implementation/spec_tools/tracegen.py --lang Go --ext .go --comment // --write`.

<!-- trace:begin -->
| Design subsection | Implementation (file:line) |
|---|---|
| [Shared state](#shared-state) | [connhandler.go:47](../../../implementation/go/internal/server/connhandler.go#L47) |
| [FrameReader](#framereader) | [connhandler.go:93](../../../implementation/go/internal/server/connhandler.go#L93), [server_test.go:136](../../../implementation/go/internal/server/server_test.go#L136) |
| [Admission](#admission) | [connhandler.go:160](../../../implementation/go/internal/server/connhandler.go#L160) |
| [Routing](#routing) | [connhandler.go:135](../../../implementation/go/internal/server/connhandler.go#L135), [heartbeat_test.go:33](../../../implementation/go/internal/server/heartbeat_test.go#L33) |
| [Response writing](#response-writing) | [connhandler.go:181](../../../implementation/go/internal/server/connhandler.go#L181), [server_test.go:628](../../../implementation/go/internal/server/server_test.go#L628) |
| [Concurrency requirement](#concurrency-requirement) | [connhandler.go:79](../../../implementation/go/internal/server/connhandler.go#L79) |
| [State machine (per `/ping` StreamTask)](#state-machine-per-ping-streamtask) | [connhandler.go:93](../../../implementation/go/internal/server/connhandler.go#L93) |
| [Connection close](#connection-close) | [connhandler.go:205](../../../implementation/go/internal/server/connhandler.go#L205) |
<!-- trace:end -->
