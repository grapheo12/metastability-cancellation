# HTTP/2 Cancellation Testbed — API

The system ships as three programs: **server**, **loadgen** and **ingress**. Each takes parameters
on the command line as `--<parameter_name> <value>`. Invalid or missing required parameters make the
program exit with a non-zero status and a one-line error message, before any network activity.

A fourth program, **bench** (the Benchmark Runner), runs on an operator machine and drives the other
three on remote machines. Its interface is in [Benchmark runner](#benchmark-runner).

## Server parameters

| Parameter | Type / unit | Default | Meaning |
|---|---|---|---|
| `listen_addr` | `host:port` | `0.0.0.0:8080` | Address to accept connections on |
| `response_rate` | float, requests/s, `> 0` | required | Rate of the exponential ServiceTime. Mean CPU time per request is `1/response_rate` s |
| `response_bytes` | integer, bytes, `>= 42` | required | Exact byte length of every `/ping` response body (`202` and `503`) |
| `cancel_mode` | `ignore` \| `cooperative` | `cooperative` | `ignore`: never cancel work (RST and `/cancel` both ignored). `cooperative`: stop work on RST or on a matching `/cancel` |
| `cancel_check_interval_us` | integer, µs, `>= 1` | `50` | How often CooperativeCpuBurn checks the RST flag |
| `seed` | unsigned 64-bit integer | `0` | Base seed for ServiceTime sampling |

The server runs until it receives an interrupt or termination signal. On shutdown it prints its
CancelCounters ([Server outputs](#server-outputs)).

## Load generator parameters

| Parameter | Type / unit | Default | Meaning |
|---|---|---|---|
| `target` | `host:port` | `127.0.0.1:8080` | Server address, or an Ingress address (the wire contract is the same) |
| `num_clients` (`N`) | integer, `1 ..= 2^32-1` | required | Number of simulated clients. Each gets its own connection |
| `global_rate` | float, requests/s, `> 0` | required | Total offered rate. Each client's arrival rate is `global_rate / N` |
| `request_bytes` | integer, bytes, `>= 42` | required | Exact byte length of every request body |
| `slo_ms` | float, ms, `> 0` | required | A request not fully answered within `slo_ms` of being sent is cancelled |
| `duration_s` | float, s, `> 0` | required | Length of the active phase. No request is sent at or after this time |
| `seed` | unsigned 64-bit integer | `0` | Base seed for arrival sampling |
| `output_path` | path prefix | `results` | Outputs go to `<output_path>.requests.csv` and `<output_path>.summary.json` |

The minimum body size of 42 bytes comes from [protocol/design.md](protocol/design.md#minimum-body-size).

## Ingress parameters

| Parameter | Type / unit | Default | Meaning |
|---|---|---|---|
| `listen_addr` | `host:port` | `0.0.0.0:9090` | Address the Frontend accepts client connections on |
| `backends` | comma-separated list of `host:port`, at least 1, at most `MAX_BACKENDS` | required | The server instances. A backend's `server_id` is its 0-based position in this list |
| `num_clients` (`N`) | integer, `1 ..= MAX_CLIENTS` | required | Number of load-generator clients. Client ids must be `< N`. Bounds the InFlightMap key space |
| `lb_policy` | `round_robin` \| `least_outstanding` \| `power_of_two` | `least_outstanding` | The LoadBalancer policy |
| `cancel_rate` | float, cancels/s, `>= 0` | `0` | Rate of the LoadCanceller's Poisson process. `0` disables the LoadCanceller |
| `duration_s` | float, s, `> 0` | required if `cancel_rate > 0`, else ignored | Length of the LoadCanceller's sending phase |
| `seed` | unsigned 64-bit integer | `0` | Base seed for the LoadBalancer's and the LoadCanceller's random choices |
| `output_path` | path prefix | `ingress` | Outputs go to `<output_path>.cancels.csv` and `<output_path>.summary.json` |

The Ingress's constants, outputs and lifecycle are in [ingress/api.md](ingress/api.md).

## Wire contract: `/ping`

Transport is HTTP/2 over cleartext TCP with prior knowledge (h2c, no HTTP/1.1 upgrade). Full
details are in [protocol/api.md](protocol/api.md).

- **Request:** `POST /ping`, `content-type: application/json`, body is a PaddedJsonBody of exactly
  `request_bytes` bytes carrying the RequestId.
- **Response:** status `202` (completed) or `503` (cancelled by `/cancel`), `content-type:
  application/json`, body is a PaddedJsonBody of exactly `response_bytes` bytes echoing the same
  RequestId.
- **Other routes:** a method other than `POST` on `/ping` returns `405`. Any path other than `/ping`
  and `/cancel` returns `404`. A body that cannot be parsed, or a RequestId whose client id differs
  from the one this connection is bound to, returns `400`. These responses have an empty body.
- **Client cancellation:** the client cancels with `RST_STREAM` using error code `CANCEL` (0x8). The
  server writes nothing for a reset stream.

## Wire contract: `/cancel`

- **Request:** `DELETE /cancel`, `content-type: application/json`, body is a CancelBody
  `{"id":"<decimal RequestId>"}`. The client-id half must name a real client for the cancel to have
  any effect. The sequence half can be anything.
- **Response:** one of these statuses:
  - `202`: the id was recorded (or, with `cancel_mode=ignore`, accepted and thrown away). The body is a
    CancelReplyBody `{"finished":"<decimal RequestId>"}` or `{"finished":null}`, with
    `content-type: application/json`. It gives the target client's FinishedWatermark: the highest RequestId of that
    client the server has finished, with any outcome. See
    [protocol/design.md#cancelreplybody](protocol/design.md#cancelreplybody).
  - `400`: the body could not be parsed. Empty body.
  - `503`: the CancelRegistry is full. Empty body.
  - A method other than `DELETE` on `/cancel` returns `405`. Empty body.

## Outputs

### Server outputs
On shutdown, the server prints one line to standard output:
`{"cancelled_before_processing":<u64>,"cancelled_during_processing":<u64>,"completed_before_cancel":<u64>}`.
See [server/api.md](server/api.md#cancelcounters-output). Counter semantics are in
[server/cancel_tracker/design.md#cancelcounters](server/cancel_tracker/design.md#cancelcounters).

### Load generator outputs
- `<output_path>.requests.csv`: one RequestRecord per scheduled arrival. Columns are in
  [load_generator/metrics/api.md](load_generator/metrics/api.md#requests-csv).
- `<output_path>.summary.json`: outcome counts, rates and latency percentiles. Fields are in
  [load_generator/metrics/api.md](load_generator/metrics/api.md#summary-json).
- Both are produced by [load_generator/metrics/design.md#collection](load_generator/metrics/design.md#collection)
  and [load_generator/metrics/design.md#summary](load_generator/metrics/design.md#summary).

### Ingress outputs
- `<output_path>.cancels.csv`: one CancelRecord per LoadCanceller arrival. Columns are in
  [ingress/load_canceller/api.md](ingress/load_canceller/api.md#output_pathcancelscsv).
- `<output_path>.summary.json`: LoadCanceller counts and rates, plus BackendStats per backend. Fields are in
  [ingress/api.md](ingress/api.md#output_pathsummaryjson). Written at shutdown by
  [ingress/design.md#shutdown](ingress/design.md#shutdown).

## Benchmark runner

`bench <command> ...`, with commands:

| Command | Effect |
|---|---|
| `bench run <config.toml>` | Setup both machines, run every SweepPoint, then plot |
| `bench setup <config.toml>` | Setup both machines only |
| `bench plot <output_dir>` | Rebuild the PointTable and plots from a finished or partial run |

The config format is in [benchmark_runner/config/api.md](benchmark_runner/config/api.md). The output
directory layout is in [benchmark_runner/api.md](benchmark_runner/api.md#output-directory). Produced by
[benchmark_runner/design.md](benchmark_runner/design.md#run-flow).

## Exit status

The load generator exits with status `0` after writing its files, and non-zero if a connection cannot be
established at startup. The server's lifecycle is in [server/api.md#lifecycle](server/api.md#lifecycle). The
Ingress's lifecycle is in [ingress/api.md#lifecycle](ingress/api.md#lifecycle): like the server, it runs until it
is interrupted, then writes its outputs and exits `0`.

## Components

| Component | API |
|---|---|
| Protocol | [protocol/api.md](protocol/api.md) |
| Sampling | [sampling/api.md](sampling/api.md) |
| Server | [server/api.md](server/api.md) |
| Load Generator | [load_generator/api.md](load_generator/api.md) |
| Ingress | [ingress/api.md](ingress/api.md) |
| Benchmark Runner | [benchmark_runner/api.md](benchmark_runner/api.md) |
