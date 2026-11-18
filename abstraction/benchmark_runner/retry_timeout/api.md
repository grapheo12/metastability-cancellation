# Retry Timeout — API

## Parameters

| Parameter | Type / unit / constraint | Default | Meaning |
|---|---|---|---|
| `workers` | integer `>= 1` | required | `k`: total workers of the pooled model (server instances × `n_worker_threads`) |
| `mean_service_ms` | float, ms, `> 0` | required | Mean exponential service time of one request (`1000 / response_rate` for `cpu_burn`) |
| `rate_per_s` | float, requests/s, `> 0`, and `rate_per_s < workers × 1000 / mean_service_ms` | required, one or more | Offered rate `λ` of the whole system |

## Types

### `RetryTimeoutResult`

| Field | Type | Meaning |
|---|---|---|
| `rate_per_s` | float | The rate it was computed for |
| `rho` | float | Utilization `λ / (k μ)` ([design.md#model](design.md#model)) |
| `erlang_c` | float | Probability that a request waits ([design.md#erlangc](design.md#erlangc)) |
| `mean_sojourn_ms` | float, ms | Mean sojourn time `1/μ + C / (kμ − λ)` |
| `retry_timeout_ms` | float, ms | The RetryTimeout: `Pr(sojourn > retry_timeout_ms) = 1 − rho` ([design.md#timeoutsolve](design.md#timeoutsolve)) |

## Functions

### `retry_timeout(workers: integer, mean_service_ms: float, rate_per_s: float) -> RetryTimeoutResult | ValueError`

Validates the three inputs ([design.md#validation](design.md#validation)) and solves for the RetryTimeout
([design.md#timeoutsolve](design.md#timeoutsolve)). Called once per rate by `bench retry-timeout`.

### `format_results(results: list of RetryTimeoutResult) -> string`

The text printed by `bench retry-timeout` ([design.md#output-format](design.md#output-format)).

## Outputs

`bench retry-timeout` prints, on standard output, a header line, one line per rate in the order given, and a final
TOML line ([design.md#output-format](design.md#output-format)):

```
rate_per_s	rho	erlang_c	mean_sojourn_ms	retry_timeout_ms
60	0.6000	0.024134	200.603	183.945
retry_timeout_ms = [183.945]
```

## Lifecycle / exit status

| Status | When |
|---|---|
| `0` | Every rate was solved and printed |
| `2` | Usage error (missing flag or rate) or a validation error, printed as `error: <message>` on standard error; nothing is printed on standard output |

## Uses

Nothing: standard math only.
