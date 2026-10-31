# Load Canceller — API

## Program parameters

Same as [../api.md#canceller-parameters](../api.md#canceller-parameters):

| Parameter | Type / unit | Default |
|---|---|---|
| `target` | `host:port` | `127.0.0.1:8080` |
| `num_clients` (`N`) | integer, `1 ..= 2^32-1` | required |
| `cancel_rate` | float, cancels/s, `> 0` | required |
| `duration_s` | float, s, `> 0` | required |
| `seed` | u64 | `0` |
| `output_path` | path prefix | `cancels` |

## Constants

| Name | Value | Meaning |
|---|---|---|
| `INCR_RAND_MAX` | `4` | Largest per-client counter step. Steps are uniform in `[1, INCR_RAND_MAX]` ([design.md#canceltargetchoice](design.md#canceltargetchoice)) |
| `CANCELLER_DRAIN_TIMEOUT_MS` | `1000` | Longest wait for outstanding responses after the sending phase ([design.md#cancellerdrain](design.md#cancellerdrain)) |

## Types

### `CancellerConfig`

An immutable record holding the parameters above after validation, plus `duration_ns =
round(duration_s * 1e9)` ([design.md#configuration-validation](design.md#configuration-validation)).

### `CancelRecord`

One per arrival. Created by [design.md#cancelschedule](design.md#cancelschedule), resolved by
[design.md#response-collection](design.md#response-collection) or
[design.md#cancellerdrain](design.md#cancellerdrain).

| Field | Type | Meaning |
|---|---|---|
| `arrival` | u64 | Index of the arrival, from 0, in schedule order |
| `cancel_id` | u64 or empty | RequestId sent. Empty for `skipped` |
| `client_id` | u32 | Chosen client |
| `target_seq` | u32 or empty | Low 32 bits of `cancel_id`. Empty for `skipped` |
| `outcome` | `accepted` \| `rejected` \| `bad_request` \| `error` \| `timeout` \| `skipped` | See [design.md#outcome-classification](design.md#outcome-classification) |
| `http_status` | integer or empty | Status received. Empty for `error` without a status, `timeout` and `skipped` |
| `scheduled_ns` | integer | Scheduled arrival time, ns since `run_start` |
| `send_ns` | integer or empty | When the stream was opened. Empty for `skipped` |
| `end_ns` | integer or empty | When the response completed or failed. Empty for `timeout` and `skipped` |

## Outputs

### `<output_path>.cancels.csv`
UTF-8, comma-separated, `\n` line endings, one header line, rows in `arrival` order:

```
arrival,cancel_id,client_id,target_seq,outcome,http_status,scheduled_ns,send_ns,end_ns
```

Integers are written in decimal. Empty fields are written as nothing between the commas. Rows are
the CancelRecords from [design.md#cancelschedule](design.md#cancelschedule).

### `<output_path>.summary.json`

```
{
  "config":      { every CancellerConfig parameter, by its parameter name },
  "run_end_s":   float,
  "counts":      { "scheduled", "sent", "accepted", "rejected", "bad_request", "error", "timeout", "skipped" },
  "rates_per_s": { "offered", "sent", "accepted" },
  "clients_targeted": integer
}
```

Definitions are in [design.md#summary](design.md#summary).

## Exit status

`0` after both outputs are written. Non-zero on invalid config, if the connection cannot be
established, or if the output files cannot be written.

## Uses

- [../protocol/api.md](../protocol/api.md): `make_request_id`, `build_cancel_body`, `CANCEL_PATH`, `CANCEL_METHOD`, canceller status classification
- [../sampling/api.md](../sampling/api.md): `derive_seed`, `rng_new`, `exponential_ns`, `uniform_int`, `STREAM_CANCELLER`
