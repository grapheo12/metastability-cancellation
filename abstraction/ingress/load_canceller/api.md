# Load Canceller — API

## Parameters

The LoadCanceller reads these Ingress parameters ([../api.md#program-parameters](../api.md#program-parameters)):

| Parameter | Type / unit | Default |
|---|---|---|
| `backends` | list of `host:port` | required |
| `cancel_rate` | float, cancels/s, `>= 0`; `0` disables the LoadCanceller | `0` |
| `duration_s` | float, s, `> 0` | required if `cancel_rate > 0` |
| `seed` | u64 | `0` |

## Constants

| Name | Value | Meaning |
|---|---|---|
| `CANCELLER_DRAIN_TIMEOUT_MS` | `1000` | Longest wait for outstanding responses after the sending phase ([design.md#cancellerdrain](design.md#cancellerdrain)) |

## Types

### `CancelRecord`

One per arrival. Created by [design.md#cancelschedule](design.md#cancelschedule), resolved by
[design.md#response-collection](design.md#response-collection) or
[design.md#cancellerdrain](design.md#cancellerdrain).

| Field | Type | Meaning |
|---|---|---|
| `arrival` | u64 | Index of the arrival, from 0, in schedule order |
| `cancel_id` | u64 or empty | RequestId sent. Empty for `skipped` |
| `client_id` | u32 or empty | Client of the victim. Empty for `skipped` |
| `server_id` | u32 or empty | Backend the `/cancel` was sent to. Empty for `skipped` |
| `target_seq` | u32 or empty | Low 32 bits of `cancel_id`. Empty for `skipped` |
| `outcome` | `accepted` \| `rejected` \| `bad_request` \| `error` \| `timeout` \| `skipped` | See [design.md#outcome-classification](design.md#outcome-classification) |
| `http_status` | integer or empty | Status received. Empty for `error` without a status, `timeout` and `skipped` |
| `scheduled_ns` | integer | Scheduled arrival time, ns since `run_start` |
| `send_ns` | integer or empty | When the stream was opened. Empty for `skipped` |
| `end_ns` | integer or empty | When the response completed or failed. Empty for `timeout` and `skipped` |

## Functions

### `canceller_start(config: IngressConfig, inflight: InFlightMap, run_start) -> Canceller`
Called once by the Frontend on the first `/ping`, if `cancel_rate > 0`. Starts the sender. See
[design.md#cancelschedule](design.md#cancelschedule).

### `canceller_stop(canceller) -> list of CancelRecord`
Called at Ingress shutdown. Stops the sender if it is still running, runs the CancellerDrain, closes the
canceller's connections, and returns the records in arrival order. See [design.md#cancellerdrain](design.md#cancellerdrain).

### `summarize(records, config, run_end) -> CancellerSummary`
The `counts`, `rates_per_s` and `clients_targeted` of the Ingress summary
([../api.md#output_pathsummaryjson](../api.md#output_pathsummaryjson)). See [design.md#summary](design.md#summary).

## Outputs

### `<output_path>.cancels.csv`
UTF-8, comma-separated, `\n` line endings, one header line, rows in `arrival` order:

```
arrival,cancel_id,client_id,server_id,target_seq,outcome,http_status,scheduled_ns,send_ns,end_ns
```

Integers are written in decimal. Empty fields are written as nothing between the commas. The Ingress writes this
file at shutdown, header only when the LoadCanceller is disabled.

## Uses

- [../../protocol/api.md](../../protocol/api.md): `build_cancel_body`, `split_request_id`, `CANCEL_PATH`, `CANCEL_METHOD`, canceller status classification
- [../../sampling/api.md](../../sampling/api.md): `derive_seed`, `rng_new`, `exponential_ns`, `uniform_int`, `STREAM_CANCELLER`
- [../inflight_map/api.md](../inflight_map/api.md): `snapshot`, `InFlightEntry`
- [../upstream_pool/api.md](../upstream_pool/api.md): `UPSTREAM_DIAL_TIMEOUT_MS`, and the same `open_h2c_connection`
