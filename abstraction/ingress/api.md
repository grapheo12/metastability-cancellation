# Ingress — API

## Program parameters

Same as [../api.md#ingress-parameters](../api.md#ingress-parameters). Each is passed as `--<name> <value>`. Invalid
or missing required parameters make the program exit non-zero with a one-line error before any network activity
([design.md#configuration-validation](design.md#configuration-validation)).

| Parameter | Type / unit | Default | Meaning |
|---|---|---|---|
| `listen_addr` | `host:port` | `0.0.0.0:9090` | Address the Frontend accepts client connections on |
| `backends` | comma-separated list of `host:port`, at least 1, at most `MAX_BACKENDS` | required | The server instances. A backend's `server_id` is its 0-based position in this list |
| `num_clients` (`N`) | integer, `1 ..= MAX_CLIENTS` | required | Number of load-generator clients. Client ids must be `< N`. Sizes the InFlightMap |
| `lb_policy` | `round_robin` \| `least_outstanding` \| `power_of_two` | `least_outstanding` | The LoadBalancer policy |
| `cancel_rate` | float, cancels/s, `>= 0` | `0` | Rate of the LoadCanceller's Poisson process. `0` disables the LoadCanceller |
| `duration_s` | float, s, `> 0` | required if `cancel_rate > 0`, else ignored | Length of the LoadCanceller's sending phase |
| `seed` | unsigned 64-bit integer | `0` | Base seed for the LoadBalancer's and the LoadCanceller's random choices |
| `output_path` | path prefix | `ingress` | Outputs go to `<output_path>.cancels.csv` and `<output_path>.summary.json` |

## Constants

| Name | Value | Meaning |
|---|---|---|
| `MAX_CLIENTS` | `2^20` | Largest `num_clients`; bounds the InFlightMap's memory |
| `MAX_BACKENDS` | `64` | Largest number of backends |
| `INGRESS_DRAIN_TIMEOUT_MS` | `2000` | Longest wait at shutdown for forwards still in progress ([design.md#shutdown](design.md#shutdown)) |

## Types

### `IngressConfig`

An immutable record of the parameters above after validation, with `backends` as a list of `host:port` strings
and `duration_ns = round(duration_s * 1e9)` when the LoadCanceller is enabled
([design.md#configuration-validation](design.md#configuration-validation)).

### `BackendStats`

One record per backend, all fields atomic u64 starting at 0, kept by the LoadBalancer.

| Field | Meaning |
|---|---|
| `outstanding` | Forwards to this backend in progress right now |
| `forwarded` | Forwards started |
| `ok` | Relayed `202` |
| `rejected` | Relayed `503` |
| `bad_request` | Relayed `400` |
| `bad_gateway` | Answered `502` (upstream failure) |
| `reset` | Ended because the client reset its stream |

## Behavior visible on the wire

- Speaks HTTP/2 over cleartext TCP with prior knowledge to clients and to backends.
- Serves `POST /ping` exactly as in [../api.md#wire-contract-ping](../api.md#wire-contract-ping): the backend's
  status and body are relayed unchanged. `502` with an empty body means the backend could not be reached or its
  stream failed ([../protocol/api.md](../protocol/api.md): `STATUS_BAD_GATEWAY`).
- A body that does not parse, or whose client id is `>= num_clients`, gets `400` with an empty body. Other paths
  get `404`, other methods on `/ping` get `405`. The Ingress does not serve `/cancel`.
- Requests on one client connection are handled concurrently. A load-generator client keeps at most one
  outstanding, so in practice each client has at most one forward in progress.

## Outputs

### `<output_path>.cancels.csv`

The LoadCanceller's CancelRecords, one per arrival. The format is in
[load_canceller/api.md](load_canceller/api.md#output_pathcancelscsv). It is written even when the LoadCanceller is
disabled (header only).

### `<output_path>.summary.json`

```
{
  "config":      { every IngressConfig parameter, by its parameter name; backends as an array of strings },
  "run_end_s":   float,
  "counts":      { "scheduled", "sent", "accepted", "rejected", "bad_request", "error", "timeout", "skipped" },
  "rates_per_s": { "offered", "sent", "accepted" },
  "clients_targeted": integer,
  "backends": [ { "server_id", "addr", "forwarded", "ok", "rejected", "bad_request", "bad_gateway", "reset" }, ... ]
}
```

`counts`, `rates_per_s` and `clients_targeted` describe the LoadCanceller and keep the names they had when it was a
separate program, so result tools can read them unchanged (all zero when it is disabled). `backends` is in
`server_id` order and holds the final BackendStats. Written by [design.md#shutdown](design.md#shutdown).

## Lifecycle

- **Start:** validate the config, create the InFlightMap, the LoadBalancer and the UpstreamPool, bind
  `listen_addr`, print one line `listening on <addr>` to standard output. Backends are not contacted yet.
- **Run:** accept client connections until stopped. The LoadCanceller starts with the first `/ping`.
- **Stop:** on interrupt or termination signal, see [design.md#shutdown](design.md#shutdown). Exit `0` after the
  outputs are written.
- **Error:** an invalid config or a failed bind exits non-zero before accepting. A failure to write the outputs
  exits non-zero.

## Uses

- [../protocol/api.md](../protocol/api.md): `parse_body`, `split_request_id`, status constants, `PING_PATH`, `CANCEL_PATH`
- [../sampling/api.md](../sampling/api.md): `derive_seed`, `rng_new`, `exponential_ns`, `uniform_int`, `STREAM_CANCELLER`, `STREAM_INGRESS_LB`
- [../server/api.md](../server/api.md): the backends' wire behavior (one client per connection, served serially)

## Subcomponents

| Subcomponent | Spec |
|---|---|
| In-Flight Map | [inflight_map/api.md](inflight_map/api.md) |
| Load Balancer | [load_balancer/api.md](load_balancer/api.md) |
| Upstream Pool | [upstream_pool/api.md](upstream_pool/api.md) |
| Load Canceller | [load_canceller/api.md](load_canceller/api.md) |
| Frontend | [frontend/api.md](frontend/api.md) |
