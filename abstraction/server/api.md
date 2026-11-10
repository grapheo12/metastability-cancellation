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
| `n_worker_threads` | integer, `1 ..= 2^32-1` | `1` |
| `queue_log_path` | path, or empty | empty |
| `queue_sample_interval_ms` | integer, ms, `>= 1` | `10` |

## Constants

| Name | Value | Meaning |
|---|---|---|
| `CHECK_ITER` | `1024` | Spin units between `/cancel` checks during a burn ([ping_handler/design.md#cancelcheckpoints](ping_handler/design.md#cancelcheckpoints)) |
| `CANCEL_REGISTRY_CAPACITY` | `65536` | Number of client ids the CancelRegistry can hold. Must be a power of two and at least `N` ([cancel_tracker/design.md#cancelregistry](cancel_tracker/design.md#cancelregistry)) |

## Types

### `ServerConfig`

An immutable record holding the nine parameters above after validation
([design.md#configuration-validation](design.md#configuration-validation)). One read-only copy is
shared by all Connection Handlers and workers.

## Behavior visible on the wire

- Speaks HTTP/2 over cleartext TCP with prior knowledge. Does not support HTTP/1.1.
- Serves `/ping` and `/cancel` as described in [../protocol/api.md](../protocol/api.md).
- Starts `/ping` requests from all connections in one global order, the order in which they were fully received
  (END_STREAM) and admitted, and runs up to `n_worker_threads` of them at once
  ([worker_pool/design.md#ordering](worker_pool/design.md#ordering)).
- Answers `/cancel` without queuing it behind any `/ping`.
- Binds each connection to the client id of its first valid `/ping`.
- Cancels work as `cancel_mode` says ([design.md#cancellation-semantics](design.md#cancellation-semantics)).

## CancelCounters output

At shutdown the server prints exactly one line to standard output, after `listening on <addr>`:

```
{"cancelled_before_processing":<u64>,"cancelled_during_processing":<u64>,"completed_before_cancel":<u64>}
```

The keys are in this order, there is no whitespace, and the values are decimal. They are defined in
[cancel_tracker/design.md#cancelcounters](cancel_tracker/design.md#cancelcounters).

## QueueLengthLog output

Written only when `queue_log_path` is not empty. A CSV file, overwritten if it exists:

```
t_ms,queue_len
0,0
10,0
20,3
```

| Column | Type / unit | Meaning |
|---|---|---|
| `t_ms` | u64, whole ms | Time of the sample since the sampler started, on a monotonic clock. The sampler starts right after `listening on` is printed |
| `queue_len` | u64 | Number of PingJobs waiting in the WorkQueue at that instant. Jobs being run by a worker are not counted |

Rows are in increasing `t_ms` order, one every `queue_sample_interval_ms` unless the sampler was late
([worker_pool/design.md#queuelengthlog](worker_pool/design.md#queuelengthlog)). Line endings are `\n`, numbers are
decimal with no padding, and there is no whitespace. The file is complete once the CancelCounters line has been
printed.

## Lifecycle

- **Start:** validate the config, create the CancelRegistry, the CancelCounters, the WorkQueue and the
  WorkerPool, open `queue_log_path` if set, bind `listen_addr`, print one line `listening on <addr>` to standard
  output, then start the QueueLengthLog sampler if set.
- **Run:** accept connections until stopped.
- **Stop:** on interrupt or termination signal, stop accepting, stop the QueueLengthLog sampler and close its
  file, close the WorkQueue and all connections, wait for the workers, print the CancelCounters line, exit `0`.
- **Error:** an invalid config, a `queue_log_path` that cannot be created, or a failed bind exits non-zero before
  accepting. A write error on the QueueLengthLog later does not stop the server: sampling stops and one warning
  line goes to standard error.

## Subcomponents

| Subcomponent | Spec |
|---|---|
| Connection Handler | [connection_handler/api.md](connection_handler/api.md) |
| Worker Pool | [worker_pool/api.md](worker_pool/api.md) |
| Ping Handler | [ping_handler/api.md](ping_handler/api.md) |
| CPU Burner | [cpu_burner/api.md](cpu_burner/api.md) |
| Cancel Tracker | [cancel_tracker/api.md](cancel_tracker/api.md) |
