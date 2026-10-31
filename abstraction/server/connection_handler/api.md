# Connection Handler — API

## `ConnectionHandler(conn, config: ServerConfig, rng: Rng)`

Starts handling one accepted connection. Returns once the connection is closed. It is called by the
Listener in [../design.md#listener](../design.md#listener), on a coroutine/thread of its own.

## `StreamTask`

| Field | Type | Meaning |
|---|---|---|
| `stream_id` | u31 | HTTP/2 stream id |
| `method` | string | `:method` pseudo-header |
| `path` | string | `:path` pseudo-header |
| `body` | bytes | Full request body (all DATA frames joined) |
| `cancelled` | atomic boolean, initially `false` | Set by the FrameReader on `RST_STREAM`. Read by the worker and the burner |

## `close()`

Asks the handler to shut down ([design.md#connection-close](design.md#connection-close)).

## Uses

- [../ping_handler/api.md](../ping_handler/api.md) `handle_ping(task, config, rng) -> Response | Aborted`
- [../../protocol/api.md](../../protocol/api.md) status constants
