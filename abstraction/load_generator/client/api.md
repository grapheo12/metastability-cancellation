# Client — API

## Types

### `Outcome`

| Value | Meaning |
|---|---|
| `ok` | `202` with matching RequestId, fully received before the SLO |
| `rejected` | `503` with matching RequestId, fully received before the SLO |
| `cancelled` | The SLO ran out first. The client sent `RST_STREAM(CANCEL)` |
| `error` | Any other status, a body or id mismatch, or a stream/connection error before the SLO |
| `not_sent` | Scheduled before `duration_ns` but not yet sendable when the active phase ended |

See [design.md#outcome-classification](design.md#outcome-classification) and, for `not_sent`,
[design.md#closedlooppoissonclient](design.md#closedlooppoissonclient).

## Functions

### `run_client(client_id: u32, conn: H2Connection, config: LoadGenConfig, rng: Rng, run_start) -> list of RequestRecord`

Runs one ClosedLoopPoissonClient until its schedule passes `duration_ns` and its last in-flight
request has resolved. Returns its records ordered by `seq`. Called by the Run Controller
([../run_controller/design.md#startup](../run_controller/design.md#startup)). See
[design.md#closedlooppoissonclient](design.md#closedlooppoissonclient).

- `rng` must be `rng_new(derive_seed(seed, STREAM_CLIENT_ARRIVALS, client_id))`.
- `conn` (`H2Connection`) is an open h2c connection owned only by this client, opened by the Run
  Controller.
- `run_start` is the shared monotonic time origin ([../design.md#time-base](../design.md#time-base)).

## Uses

- [../../protocol/api.md](../../protocol/api.md): `make_request_id`, `build_body`, `parse_body`, headers
- [../../sampling/api.md](../../sampling/api.md): `exponential_ns`
- [../metrics/api.md](../metrics/api.md): `RequestRecord`
