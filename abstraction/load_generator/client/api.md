# Client — API

## Types

### `Outcome`

| Value | Meaning |
|---|---|
| `ok` | `202` with matching RequestId on the newest RequestCopy, fully received before the SLO |
| `rejected` | `503` with matching RequestId on the newest RequestCopy, fully received before the SLO |
| `cancelled` | The SLO ran out first. The client sent `RST_STREAM(CANCEL)` on every RequestCopy |
| `dropped` | The RetryTimer of the last RequestCopy (copy `max_retries`) fired first. No reset was sent. Never for a StallWindowRequest ([design.md#stallwindow](design.md#stallwindow)) |
| `error` | Any other status, a body or id mismatch, or a stream/connection error of the newest RequestCopy before the SLO |
| `not_sent` | Scheduled before `duration_ns` but not yet sendable when the active phase ended |

See [design.md#outcome-classification](design.md#outcome-classification), for `dropped`
[design.md#retrytimer](design.md#retrytimer), and, for `not_sent`,
[design.md#semiopenloopclient](design.md#semiopenloopclient).

## Functions

### `run_client(client_id: u32, conn: H2Connection, config: LoadGenConfig, rng: Rng, run_start) -> list of RequestRecord`

Runs one SemiOpenLoopClient, with at most `n_waiting_requests` requests outstanding, until its schedule passes
`duration_ns` and every request it sent has resolved. Returns its records ordered by `seq`. Called by the Run Controller
([../run_controller/design.md#startup](../run_controller/design.md#startup)). See
[design.md#semiopenloopclient](design.md#semiopenloopclient).

- `rng` must be `rng_new(derive_seed(seed, STREAM_CLIENT_ARRIVALS, client_id))`.
- `conn` (`H2Connection`) is an open h2c connection owned only by this client, opened by the Run
  Controller. It carries up to `n_waiting_requests × (1 + max_retries)` concurrent streams of unresolved requests
  ([design.md#retrytimer](design.md#retrytimer)).
- `run_start` is the shared monotonic time origin ([../design.md#time-base](../design.md#time-base)).

## Uses

- [../../protocol/api.md](../../protocol/api.md): `make_request_id`, `build_body`, `parse_body`, headers
- [../../sampling/api.md](../../sampling/api.md): `exponential_ns`
- [../metrics/api.md](../metrics/api.md): `RequestRecord`
