# Connection Handler — API

## Types

### `StreamTask`

| Field | Type | Meaning |
|---|---|---|
| `stream_id` | u31 | HTTP/2 stream id |
| `method` | string | `:method` pseudo-header |
| `path` | string | `:path` pseudo-header |
| `body` | bytes | Full request body (all DATA frames joined) |
| `cancelled` | atomic boolean, initially `false` | The RST flag. Set by the FrameReader on `RST_STREAM` or when the connection closes. Read by the worker and the burner through the stream's PingJob |

Created by [design.md#framereader](design.md#framereader). Lifecycle in
[design.md#state-machine-per-ping-streamtask](design.md#state-machine-per-ping-streamtask).

### `Response`

Either `Response(status: integer, body: bytes)` or `Aborted`, which means "write nothing". Returned by
`admit_ping`, `run_ping` (through a PingJob's `reply`), `handle_cancel` and routing, and written by
[design.md#response-writing](design.md#response-writing).

## Functions

### `ConnectionHandler(conn, config: ServerConfig, rng: Rng, registry: CancelRegistry, queue: WorkQueue)`

Starts handling one accepted connection. Returns once the connection is closed and every stream it admitted has
been answered or dropped. It is called by the Listener in [../design.md#listener](../design.md#listener), on a
coroutine/thread of its own. See [design.md#shared-state](design.md#shared-state).

### `close()`

Asks the handler to shut down ([design.md#connection-close](design.md#connection-close)).

## Uses

- [../ping_handler/api.md](../ping_handler/api.md): `admit_ping(task, config, rng, conn_state) -> PingJob | Response`
- [../worker_pool/api.md](../worker_pool/api.md): `PingJob`, `queue_push`
- [../cancel_tracker/api.md](../cancel_tracker/api.md): `ConnectionCancelState`, `handle_cancel`
- [../../protocol/api.md](../../protocol/api.md): route and status constants
