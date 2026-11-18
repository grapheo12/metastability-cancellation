# Retry Timeout — Design

All arithmetic is IEEE 754 double precision, with the operations in the order written. Times inside the
computation are in seconds; inputs and outputs are in ms.

## Model

```
k   = workers                         # integer >= 1
mu  = 1000 / mean_service_ms          # service rate of one worker, 1/s
lam = rate_per_s                      # offered rate of the whole system, 1/s
a   = lam / mu                        # offered load in Erlangs
rho = lam / (k * mu)                  # utilization, 0 < rho < 1 (Validation)
theta = k * mu - lam                  # drain rate of the queue when all k workers are busy, > 0
```

The model is one FCFS **M/M/k** queue: Poisson arrivals at `lam`, `k` identical workers, exponential service with
rate `mu`. The **sojourn time** `T` of a request is its queueing wait `W` plus its service `S ~ Exp(mu)`, with `W`
and `S` independent.

## ErlangC

`C` is the probability that an arriving request finds all `k` workers busy and waits (Erlang C). It is computed from
the Erlang B recursion, which never forms `a^k` or `k!` and so cannot overflow:

```
function erlang_c(k, a):
    B = 1.0
    for n = 1 .. k:
        B = a * B / (n + a * B)        # Erlang B with n servers
    return k * B / (k - a * (1 - B))   # Erlang C from Erlang B, valid for a < k
```

## SojournTail

`W = 0` with probability `1 − C`, and otherwise `W ~ Exp(theta)`. So

```
function sojourn_tail(t, k, mu, lam):          # Pr(T > t), t >= 0 in seconds
    C = erlang_c(k, lam / mu); theta = k * mu - lam
    if abs(theta - mu) <= 1e-12 * mu:          # limit theta -> mu
        return exp(-mu * t) * (1 + C * mu * t)
    r = C * mu / (theta - mu)
    return (1 + r) * exp(-mu * t) - r * exp(-theta * t)
```

- `sojourn_tail(0) = 1`, and it decreases strictly to `0` as `t` grows, for every valid input.
- The mean is `E[T] = 1/mu + C / theta` (`mean_sojourn_ms = 1000 × E[T]`).
- The branch for `theta = mu` is the limit of the general formula. Near it the general formula subtracts two large
  terms, but stays accurate to well under 1 µs for `|theta − mu| >= 1e-12 × mu` (see the worked examples).

## TimeoutSolve

The **RetryTimeout** is the `t` with `sojourn_tail(t) = 1 − rho`.

```
function retry_timeout(workers, mean_service_ms, rate_per_s):
    validate(workers, mean_service_ms, rate_per_s)            # Validation
    compute k, mu, lam, rho, theta as in Model
    target = 1 - rho                                          # in (0, 1)
    lo = 0.0; hi = 1 / mu
    while sojourn_tail(hi) > target: hi = 2 * hi              # bracket: tail(lo) > target >= tail(hi)
    repeat 200 times:                                         # bisection
        mid = (lo + hi) / 2
        if sojourn_tail(mid) > target: lo = mid else: hi = mid
    C = erlang_c(k, lam / mu)
    return RetryTimeoutResult(rate_per_s, rho, C, 1000 * (1/mu + C/theta), 1000 * hi)
```

- The bracket loop ends because the tail tends to `0` and `target > 0`. 200 halvings shrink the bracket below one
  ulp of `hi`, so the result is the smallest representable `t` (to one ulp) with `sojourn_tail(t) <= target`.
- The result is never rounded inside the computation. Rounding happens only in the output.

## Validation

Checked in this order; the first failure raises `ValueError` with the message shown.

| Check | Message |
|---|---|
| `workers` is an integer (not a boolean) and `>= 1` | `workers must be an integer >= 1` |
| `mean_service_ms` is a number and `> 0` | `mean_service_ms must be a number > 0` |
| `rate_per_s` is a number and `> 0` | `rate must be a number > 0` |
| `rho < 1`, i.e. `rate_per_s < workers × 1000 / mean_service_ms` | `rate <rate_per_s> req/s needs rho < 1: capacity is <workers × 1000 / mean_service_ms> req/s` |

Numbers in messages are written in their shortest round-trip decimal form.

## Output format

```
function format_results(results):
    lines = ["rate_per_s\trho\terlang_c\tmean_sojourn_ms\tretry_timeout_ms"]
    for r in results (in the order given):
        lines.append(shortest(r.rate_per_s) + "\t" + fixed(r.rho, 4) + "\t" + fixed(r.erlang_c, 6) + "\t"
                     + fixed(r.mean_sojourn_ms, 3) + "\t" + fixed(r.retry_timeout_ms, 3))
    lines.append("retry_timeout_ms = [" + ", ".join(shortest(round(r.retry_timeout_ms, 3)) for r in results) + "]")
    return join(lines, "\n") + "\n"
```

- `shortest(x)` is the shortest decimal that parses back to the same double; an integral value is written without
  a fraction (`60`, not `60.0`). `fixed(x, d)` rounds to `d` decimals, half to even on the double's exact value.
- `round(x, 3)` rounds to the nearest 0.001 ms (1 µs). The TOML line pastes directly into `[load_gen]`.

## Worked examples

`workers = 20`, `mean_service_ms = 200` (`mu = 5/s`, `k mu = 100/s`), the 2 × 10-worker experiments:

| `rate_per_s` | `rho` | `erlang_c` | `mean_sojourn_ms` | `retry_timeout_ms` | Check: `sojourn_tail(T)` vs `1 − rho` |
|---|---|---|---|---|---|
| 60 | 0.6000 | 0.024134 | 200.603 | 183.945 | `1 − 0.6 = 0.4`; little queueing, so close to the service-only `200 × −ln(0.4) = 183.258` |
| 70 | 0.7000 | 0.093561 | 203.119 | 244.494 | `0.3`; service-only would be `240.795` |
| 80 | 0.8000 | 0.256078 | 212.804 | 338.171 | `0.2`; service-only would be `321.888` |
| 90 | 0.9000 | 0.550769 | 255.077 | 543.521 | `0.1`; service-only would be `460.517` |

The TOML line is `retry_timeout_ms = [183.945, 244.494, 338.171, 543.521]`.

Boundary and closed-form cases:

| Inputs | Result | Check |
|---|---|---|
| `workers = 1`, `mean_service_ms = 200`, rate `4` (M/M/1) | `erlang_c = 0.8`, `mean_sojourn_ms = 1000.000`, `retry_timeout_ms = 1609.438` | M/M/1: `T ~ Exp(mu − lam)`, so `T = −ln(1 − rho) / (mu − lam) = −ln(0.2) / 1 s = 1609.438 ms`; `C = rho = 0.8`; `E[T] = 1/(5 − 4) s` |
| `workers = 10`, `mean_service_ms = 200`, rate `45` (`theta = 5 = mu`) | `erlang_c = 0.668732`, `mean_sojourn_ms = 333.746`, `retry_timeout_ms = 702.181` | the `theta = mu` branch; rates `45 ± 1e-7` give `702.181` through the general formula |
| `workers = 1`, `mean_service_ms = 1`, rate `1e-6` | `rho = 0.0000`, `retry_timeout_ms = 0.000` | `1 − rho → 1`, so `T → 0` |
| `workers = 20`, `mean_service_ms = 200`, rate `100` | ValueError `rate 100 req/s needs rho < 1: capacity is 100 req/s` | `rho = 1` |
| `workers = 0` | ValueError `workers must be an integer >= 1` | |

## Failure and edge cases

- No rate given, or a flag missing, is a usage error of the command (exit `2`), reported before any computation.
- A rate that fails validation stops the command before anything is printed, even if other rates are valid.
- Very large `workers` (thousands) are fine: the Erlang B recursion is `O(k)` and stays in `[0, 1]`.

## Design specific to Python

Shared tool `implementation/scripts/` (tooling language Python 3.11+; see `AGENTS.md` "Shared tooling"). Standard
library only (`math`), so `bench retry-timeout` runs without plotly.
Tests: `python3 -B -m unittest discover -s implementation/scripts/tests -t implementation/scripts`.

### Python choices

- File `benchrunner/retry_timeout.py`. `RetryTimeoutResult` is a `dataclass`. `erlang_c`, `sojourn_tail`, `validate`, `retry_timeout` and `format_results` follow the design's functions; `BISECTION_STEPS = 200` and `THETA_EQ_MU_REL = 1e-12` name its constants.
- Python floats are IEEE 754 doubles; `math.exp` is the exponential. `fixed(x, d)` is the format spec `.{d}f`, which rounds the double's exact value correctly. `shortest(x)` is `repr(x)`, or `str(int(x))` for integral values; `round(x, 3)` is Python's `round`.
- `workers` must be an `int` that is not a `bool`; numbers are `int` or `float` (not `bool`) and finite.
- `bench.py` adds the argparse subcommand `retry-timeout` (`--workers` as `int`, `--mean_service_ms` and the rates as `float`, `nargs="+"`). Argparse reports usage errors with exit `2`. `bench_retry_timeout` computes every rate before printing anything and turns a `ValueError` into `error: <message>` on stderr with exit `2`.
- Tests: `tests/test_retry_timeout.py` checks the worked examples (to ±0.0005 ms and the defining equation), M/M/1 against `−ln(1 − ρ)/(μ − λ)`, the `theta = mu` branch and its neighbors, the low-load limit and monotonic tail, a large `k`, every validation message, and the exact CLI output and error exit.

### Traceability

Generated from the `# Design:` tags in the code by `implementation/spec_tools/tracegen.py` (see `AGENTS.md`). Each link points at the first code line after the tag.
Regenerate from the repository root with `python3 implementation/spec_tools/tracegen.py --lang Python --ext .py --comment '#' --impl implementation/scripts --write`.

<!-- trace:begin -->
| Design subsection | Implementation (file:line) |
|---|---|
| [Model](#model) | [retry_timeout.py:68](../../../implementation/scripts/benchrunner/retry_timeout.py#L68) |
| [ErlangC](#erlangc) | [retry_timeout.py:23](../../../implementation/scripts/benchrunner/retry_timeout.py#L23), [test_retry_timeout.py:53](../../../implementation/scripts/tests/test_retry_timeout.py#L53) |
| [SojournTail](#sojourntail) | [retry_timeout.py:32](../../../implementation/scripts/benchrunner/retry_timeout.py#L32), [test_retry_timeout.py:30](../../../implementation/scripts/tests/test_retry_timeout.py#L30), [test_retry_timeout.py:37](../../../implementation/scripts/tests/test_retry_timeout.py#L37) |
| [TimeoutSolve](#timeoutsolve) | [retry_timeout.py:13](../../../implementation/scripts/benchrunner/retry_timeout.py#L13), [retry_timeout.py:68](../../../implementation/scripts/benchrunner/retry_timeout.py#L68), [test_retry_timeout.py:46](../../../implementation/scripts/tests/test_retry_timeout.py#L46) |
| [Validation](#validation) | [retry_timeout.py:53](../../../implementation/scripts/benchrunner/retry_timeout.py#L53), [test_retry_timeout.py:58](../../../implementation/scripts/tests/test_retry_timeout.py#L58) |
| [Output format](#output-format) | [retry_timeout.py:88](../../../implementation/scripts/benchrunner/retry_timeout.py#L88), [test_retry_timeout.py:72](../../../implementation/scripts/tests/test_retry_timeout.py#L72) |
| [Worked examples](#worked-examples) | [test_retry_timeout.py:17](../../../implementation/scripts/tests/test_retry_timeout.py#L17) |
| [Failure and edge cases](#failure-and-edge-cases) | [bench.py:150](../../../implementation/scripts/bench.py#L150) |
<!-- trace:end -->
