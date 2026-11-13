# Load Generator — API

## Program parameters

Same as [../api.md#load-generator-parameters](../api.md#load-generator-parameters):

| Parameter | Type / unit | Default |
|---|---|---|
| `target` | `host:port` | `127.0.0.1:8080` |
| `num_clients` (`N`) | integer, `1 ..= 2^32-1` | required |
| `global_rate` | float, requests/s, `> 0` | required |
| `request_bytes` | integer, bytes, `>= 76` | required |
| `slo_ms` | float, ms, `> 0` | required |
| `duration_s` | float, s, `> 0` | required |
| `seed` | u64 | `0` |
| `n_waiting_requests` | integer, `1 ..= MAX_WAITING_REQUESTS` (1000) | `100` |
| `retry_timeout_ms` | float, ms, `> 0` | `150` |
| `max_retries` | integer, `>= 0`, `n_waiting_requests × (1 + max_retries) <= MAX_WAITING_REQUESTS` | `1` |
| `initial_stall_ms` | integer, ms, `>= 0`, `< duration_s × 1000` | `1000` |
| `output_path` | path prefix | `results` |

## Types

### `LoadGenConfig`

An immutable record holding the parameters above after validation
([run_controller/design.md#configuration-validation](run_controller/design.md#configuration-validation)),
plus derived values:

| Derived | Formula |
|---|---|
| `per_client_rate` | `global_rate / N` (requests/s) |
| `slo_ns` | `round(slo_ms * 1e6)` |
| `retry_timeout_ns` | `round(retry_timeout_ms * 1e6)` |
| `initial_stall_ns` | `initial_stall_ms * 1_000_000` |
| `duration_ns` | `round(duration_s * 1e9)` |

## Outputs

`<output_path>.requests.csv` and `<output_path>.summary.json`. Formats are in
[metrics/api.md](metrics/api.md#outputs).

## Exit status

`0` after both outputs are written. Non-zero on invalid config, if any connection fails at startup,
or if the output files cannot be written.

## Subcomponents

| Subcomponent | Spec |
|---|---|
| Run Controller | [run_controller/api.md](run_controller/api.md) |
| Client | [client/api.md](client/api.md) |
| Metrics | [metrics/api.md](metrics/api.md) |
