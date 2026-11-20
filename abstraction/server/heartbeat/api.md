# Heartbeat — API

## Parameters

| Parameter | Type / unit / constraint | Default | Meaning |
|---|---|---|---|
| `heartbeat_interval_ms` | integer, ms, `>= 1` | `500` | Time between two HeartbeatLines on a HeartbeatStream. Same as [../../api.md#server-parameters](../../api.md#server-parameters) |

## Functions

### `serve_heartbeat(task: StreamTask, config: ServerConfig, queue: WorkQueue) -> nothing`
Writes the `200` headers, then one HeartbeatLine every `heartbeat_interval_ms`, starting at once, until the stream
ends (RST, connection close, write error) or the server shuts down. Before each line it calls
`worker_pool.freeze_wait(queue)`. Runs on the stream's own coroutine, started by the Connection Handler's router for
`GET /heartbeat`. See [design.md#heartbeatsender](design.md#heartbeatsender).

## Outputs / wire-visible behavior

The HeartbeatStream of [../../api.md#wire-contract-heartbeat](../../api.md#wire-contract-heartbeat).

## Uses

- [../../protocol/api.md](../../protocol/api.md): `build_heartbeat_line`, `STATUS_STREAM_OK`, `CONTENT_TYPE_TEXT`
- [../worker_pool/api.md](../worker_pool/api.md): `freeze_wait`
- [../connection_handler/api.md](../connection_handler/api.md): `StreamTask` (its RST flag)
