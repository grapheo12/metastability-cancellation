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

## Constants

| Name | Value | Meaning |
|---|---|---|
| `CHECK_ITER` | `1024` | Spin units between `/cancel` checks during a burn ([ping_handler/design.md#cancelcheckpoints](ping_handler/design.md#cancelcheckpoints)) |
| `CANCEL_REGISTRY_CAPACITY` | `65536` | Number of client ids the CancelRegistry can hold. Must be a power of two and at least `N` ([cancel_tracker/design.md#cancelregistry](cancel_tracker/design.md#cancelregistry)) |

## Types

### `ServerConfig`

An immutable record holding the six parameters above after validation
([design.md#configuration-validation](design.md#configuration-validation)). One read-only copy is
shared by all Connection Handlers.

## Behavior visible on the wire

- Speaks HTTP/2 over cleartext TCP with prior knowledge. Does not support HTTP/1.1.
- Serves `/ping` and `/cancel` as described in [../protocol/api.md](../protocol/api.md).
- Processes requests on one connection serially, in the order their request is fully received (END_STREAM).
- Binds each connection to the client id of its first valid `/ping`.
- Cancels work as `cancel_mode` says ([design.md#cancellation-semantics](design.md#cancellation-semantics)).

## CancelCounters output

At shutdown the server prints exactly one line to standard output, after `listening on <addr>`:

```
{"cancelled_before_processing":<u64>,"cancelled_during_processing":<u64>,"completed_before_cancel":<u64>}
```

The keys are in this order, there is no whitespace, and the values are decimal. They are defined in
[cancel_tracker/design.md#cancelcounters](cancel_tracker/design.md#cancelcounters).

## Lifecycle

- **Start:** validate the config, create the CancelRegistry and the CancelCounters, bind
  `listen_addr`, print one line `listening on <addr>` to standard output.
- **Run:** accept connections until stopped.
- **Stop:** on interrupt or termination signal, stop accepting, close all connections, print the
  CancelCounters line, exit `0`.
- **Error:** an invalid config or a failed bind exits non-zero before accepting.

## Subcomponents

| Subcomponent | Spec |
|---|---|
| Connection Handler | [connection_handler/api.md](connection_handler/api.md) |
| Ping Handler | [ping_handler/api.md](ping_handler/api.md) |
| CPU Burner | [cpu_burner/api.md](cpu_burner/api.md) |
| Cancel Tracker | [cancel_tracker/api.md](cancel_tracker/api.md) |
