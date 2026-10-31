# Server — API

## Program parameters

Same as [../api.md#server-parameters](../api.md#server-parameters):

| Parameter | Type / unit | Default |
|---|---|---|
| `listen_addr` | `host:port` | `0.0.0.0:8080` |
| `response_rate` | float, requests/s, `> 0` | required |
| `response_bytes` | integer, bytes, `>= 42` | required |
| `cancel_mode` | `ignore` \| `cooperative` | `cooperative` |
| `cancel_check_interval_us` | integer, µs, `>= 1` | `50` |
| `seed` | u64 | `0` |

## `ServerConfig`

An immutable record holding the six parameters above after validation. One read-only copy is shared by all
Connection Handlers.

## Behavior visible on the wire

- Speaks HTTP/2 over cleartext TCP with prior knowledge. Does not support HTTP/1.1.
- Serves `/ping` as described in [../protocol/api.md](../protocol/api.md).
- Processes requests on one connection serially, in the order their request is fully received (END_STREAM).
- On `RST_STREAM`, behaves as `cancel_mode` says ([design.md#cancellation-semantics](design.md#cancellation-semantics)).

## Lifecycle

- **Start:** validate the config, bind `listen_addr`, print one line `listening on <addr>` to standard output.
- **Run:** accept connections until stopped.
- **Stop:** on interrupt or termination signal, stop accepting, close all connections, exit `0`.
- **Error:** an invalid config or a failed bind exits non-zero before accepting.

## Subcomponents

| Subcomponent | Spec |
|---|---|
| Connection Handler | [connection_handler/api.md](connection_handler/api.md) |
| Ping Handler | [ping_handler/api.md](ping_handler/api.md) |
| CPU Burner | [cpu_burner/api.md](cpu_burner/api.md) |
