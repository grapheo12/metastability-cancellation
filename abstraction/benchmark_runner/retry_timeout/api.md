# Retry Timeout — API

## Parameters

| Parameter | Type / unit / constraint | Default | Meaning |
|---|---|---|---|
| `workers` | integer `>= 1` | required | `k`: workers of the modelled queue (server instances × `n_worker_threads` for the pooled system, `n_worker_threads` for one instance) |
| `mean_service_ms` | float, ms, `> 0` | required | Mean exponential service time of one request (`1000 / response_rate` for `cpu_burn` and `sleep`) |
| `rate_per_s` | float, requests/s, `> 0`, and `rate_per_s < workers × 1000 / mean_service_ms` | required, one or more | Offered rate `λ` into the modelled queue |
| `eta` | float, `>= 0` | `1` | EtaSlack target load `η` ([design.md#etaslack](design.md#etaslack)); `retry-timeout` only |

## Constants

| Name | Value | Meaning |
|---|---|---|
| `BISECTION_STEPS` | `200` | Bisection steps of TimeoutSolve |
| `FEEDBACK_GRID` | `20000` | Grid intervals of the RetryFeedback scan |
| `RHO_MAX` | `1 − 1e-9` | Right end of the RetryFeedback scan |
| `ROOT_STEPS` | `100` | Bisection steps refining one RetryFeedback crossing |
| `ETA_STEPS` | `40` | Bisection steps of the MaxStableEta search |
| `NO_RETRY_TIMEOUT_MS` | `86400000` (one day) | How an infinite RetryTimeout is written in the TOML line |

## Types

### `RetryTimeoutResult`

| Field | Type | Meaning |
|---|---|---|
| `rate_per_s` | float | The rate it was computed for |
| `rho` | float | Utilization `λ / (k μ)` ([design.md#model](design.md#model)) |
| `erlang_c` | float | Probability that a request waits ([design.md#erlangc](design.md#erlangc)) |
| `mean_sojourn_ms` | float, ms | Mean sojourn time `1/μ + C / (kμ − λ)` |
| `target` | float in `[0, 1]` | `Pr(sojourn > T)` asked for: `max(0, min(1, eta − rho))` ([design.md#etaslack](design.md#etaslack)) |
| `retry_timeout_ms` | float, ms, or infinite | The RetryTimeout: `Pr(sojourn > retry_timeout_ms) = target`; infinite when `target = 0` ([design.md#timeoutsolve](design.md#timeoutsolve)) |
| `stable_rho` | float or none | StableLoad of RetryFeedback at this RetryTimeout; none means the load grows past 1 ([design.md#retryfeedback](design.md#retryfeedback)) |
| `tipping_rho` | float or none | TippingLoad; none means there is none below `RHO_MAX` |

### `StableEtaResult`

| Field | Type | Meaning |
|---|---|---|
| `rate_per_s` | float | The rate it was computed for |
| `rho` | float | Utilization `λ / (k μ)` |
| `max_stable_eta` | float or infinite | The MaxStableEta ([design.md#maxstableeta](design.md#maxstableeta)); infinite when every `η` is stable |
| `target` | float | EtaSlack target at `max_stable_eta` (`1` when infinite) |
| `retry_timeout_ms` | float, ms | RetryTimeout at that target |
| `stable_rho` | float | StableLoad at that RetryTimeout |

## Functions

### `retry_timeout(workers: integer, mean_service_ms: float, rate_per_s: float, eta: float = 1) -> RetryTimeoutResult | ValueError`

Validates the inputs ([design.md#validation](design.md#validation)), solves for the RetryTimeout
([design.md#timeoutsolve](design.md#timeoutsolve)) and its RetryFeedback points
([design.md#retryfeedback](design.md#retryfeedback)). Called once per rate by `bench retry-timeout`.

### `max_stable_eta(workers: integer, mean_service_ms: float, rate_per_s: float) -> StableEtaResult | ValueError`

Validates the inputs and searches for the MaxStableEta ([design.md#maxstableeta](design.md#maxstableeta)). Called
once per rate by `bench stable-eta`.

### `format_results(results: list of RetryTimeoutResult) -> string`

The text printed by `bench retry-timeout` ([design.md#output-format](design.md#output-format)).

### `format_stable_eta(results: list of StableEtaResult) -> string`

The text printed by `bench stable-eta` ([design.md#output-format](design.md#output-format)).

## Outputs

`bench retry-timeout` prints, on standard output, a header line, one line per rate in the order given, and a final
TOML line ([design.md#output-format](design.md#output-format)):

```
rate_per_s	rho	erlang_c	mean_sojourn_ms	target	retry_timeout_ms	stable_rho	tipping_rho
30	0.6000	0.101299	205.065	0.200000	328.482	0.7343	0.9452
40	0.8000	0.409180	240.918	0.000000	inf	0.8000	none
retry_timeout_ms = [328.482, 86400000]
```

`bench stable-eta` prints a header line and one line per rate, with no TOML line:

```
rate_per_s	rho	max_stable_eta	target	retry_timeout_ms	stable_rho
30	0.6000	0.887554	0.287554	255.770	0.8609
```

## Lifecycle / exit status

| Status | When |
|---|---|
| `0` | Every rate was solved and printed |
| `2` | Usage error (missing flag or rate) or a validation error, printed as `error: <message>` on standard error; nothing is printed on standard output |

## Uses

Nothing: standard math only.
