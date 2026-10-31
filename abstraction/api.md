# HTTP/2 Cancellation Testbed — API

The system ships as two programs, **server** and **loadgen**. Each takes parameters on the command
line as `--<parameter_name> <value>`. Invalid or missing required parameters make the program exit
with a non-zero status and a one-line error message, before any network activity.

## Server parameters

| Parameter | Type / unit | Default | Meaning |
|---|---|---|---|
| `listen_addr` | `host:port` | `0.0.0.0:8080` | Address to accept connections on |
| `response_rate` | float, requests/s, `> 0` | required | Rate of the exponential ServiceTime. Mean CPU time per request is `1/response_rate` s |
| `response_bytes` | integer, bytes, `>= 42` | required | Exact byte length of every `/ping` response body |
| `cancel_mode` | `ignore` \| `cooperative` | `cooperative` | `ignore`: always burn the full ServiceTime. `cooperative`: stop early once the stream is reset |
| `cancel_check_interval_us` | integer, µs, `>= 1` | `50` | How often CooperativeCpuBurn checks the cancel flag |
| `seed` | unsigned 64-bit integer | `0` | Base seed for ServiceTime sampling |

The server runs until it receives an interrupt or termination signal.

## Load generator parameters

| Parameter | Type / unit | Default | Meaning |
|---|---|---|---|
| `target` | `host:port` | `127.0.0.1:8080` | Server address |
| `num_clients` (`N`) | integer, `1 ..= 2^32-1` | required | Number of simulated clients. Each gets its own connection |
| `global_rate` | float, requests/s, `> 0` | required | Total offered rate. Each client's arrival rate is `global_rate / N` |
| `request_bytes` | integer, bytes, `>= 42` | required | Exact byte length of every request body |
| `slo_ms` | float, ms, `> 0` | required | A request not fully answered within `slo_ms` of being sent is cancelled |
| `duration_s` | float, s, `> 0` | required | Length of the active phase. No request is sent at or after this time |
| `seed` | unsigned 64-bit integer | `0` | Base seed for arrival sampling |
| `output_path` | path prefix | `results` | Outputs go to `<output_path>.requests.csv` and `<output_path>.summary.json` |

The minimum body size of 42 bytes comes from [protocol/design.md](protocol/design.md#minimum-body-size).

## Wire contract: `/ping`

Transport is HTTP/2 over cleartext TCP with prior knowledge (h2c, no HTTP/1.1 upgrade). Full
details are in [protocol/api.md](protocol/api.md).

- **Request:** `POST /ping`, `content-type: application/json`, body is a PaddedJsonBody of exactly
  `request_bytes` bytes carrying the RequestId.
- **Response:** status `202` (success) or `503` (reserved, never sent in v1), `content-type:
  application/json`, body is a PaddedJsonBody of exactly `response_bytes` bytes echoing the same RequestId.
- **Other routes:** `/ping` with any method other than `POST` returns `405`. Any other path returns `404`.
  A body that cannot be parsed returns `400`. These responses have an empty body.
- **Cancellation:** the client cancels with `RST_STREAM` using error code `CANCEL` (0x8).

## Outputs (load generator)

- `<output_path>.requests.csv`: one RequestRecord per scheduled arrival. Columns are in
  [load_generator/metrics/api.md](load_generator/metrics/api.md#requests-csv).
- `<output_path>.summary.json`: outcome counts, rates and latency percentiles. Fields are in
  [load_generator/metrics/api.md](load_generator/metrics/api.md#summary-json).

The load generator exits with status `0` after writing both files. It exits non-zero if any
connection cannot be established at startup.
