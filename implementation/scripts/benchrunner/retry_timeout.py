# Tooling language: Python 3.11+ (shared tool implementation/scripts)
# Source: abstraction/benchmark_runner/retry_timeout/design.md (retry_timeout v0.1.0)
"""RetryTimeoutCalculator: the RetryTimeout T with Pr(sojourn > T) = 1 - rho in a pooled M/M/k model."""

import math
from dataclasses import dataclass

BISECTION_STEPS = 200
THETA_EQ_MU_REL = 1e-12  # |theta - mu| <= this * mu uses the theta = mu limit


# Design: abstraction/benchmark_runner/retry_timeout/design.md#timeoutsolve
@dataclass
class RetryTimeoutResult:
    rate_per_s: float
    rho: float
    erlang_c: float
    mean_sojourn_ms: float
    retry_timeout_ms: float


# Design: abstraction/benchmark_runner/retry_timeout/design.md#erlangc
def erlang_c(k, a):
    """Probability of waiting in M/M/k with offered load a < k (Erlang C), via the Erlang B recursion."""
    b = 1.0
    for n in range(1, k + 1):
        b = a * b / (n + a * b)
    return k * b / (k - a * (1 - b))


# Design: abstraction/benchmark_runner/retry_timeout/design.md#sojourntail
def sojourn_tail(t, k, mu, lam):
    """Pr(T > t) for the sojourn time T (wait + service) of FCFS M/M/k; t in seconds."""
    c = erlang_c(k, lam / mu)
    theta = k * mu - lam
    if abs(theta - mu) <= THETA_EQ_MU_REL * mu:  # limit theta -> mu
        return math.exp(-mu * t) * (1 + c * mu * t)
    r = c * mu / (theta - mu)
    return (1 + r) * math.exp(-mu * t) - r * math.exp(-theta * t)


def _shortest(x):
    """Shortest round-trip decimal; integral values without a fraction."""
    x = float(x)
    return str(int(x)) if x.is_integer() else repr(x)


def _is_number(v):
    return isinstance(v, (int, float)) and not isinstance(v, bool) and math.isfinite(v)


# Design: abstraction/benchmark_runner/retry_timeout/design.md#validation
def validate(workers, mean_service_ms, rate_per_s):
    if not isinstance(workers, int) or isinstance(workers, bool) or workers < 1:
        raise ValueError("workers must be an integer >= 1")
    if not _is_number(mean_service_ms) or mean_service_ms <= 0:
        raise ValueError("mean_service_ms must be a number > 0")
    if not _is_number(rate_per_s) or rate_per_s <= 0:
        raise ValueError("rate must be a number > 0")
    mu = 1000 / mean_service_ms
    if rate_per_s / (workers * mu) >= 1:
        raise ValueError(f"rate {_shortest(rate_per_s)} req/s needs rho < 1: "
                         f"capacity is {_shortest(workers * 1000 / mean_service_ms)} req/s")


# Design: abstraction/benchmark_runner/retry_timeout/design.md#model
# Design: abstraction/benchmark_runner/retry_timeout/design.md#timeoutsolve
def retry_timeout(workers, mean_service_ms, rate_per_s):
    validate(workers, mean_service_ms, rate_per_s)
    k, mu, lam = workers, 1000 / mean_service_ms, rate_per_s
    rho = lam / (k * mu)
    theta = k * mu - lam
    target = 1 - rho
    lo, hi = 0.0, 1 / mu
    while sojourn_tail(hi, k, mu, lam) > target:  # bracket: tail(lo) > target >= tail(hi)
        hi = 2 * hi
    for _ in range(BISECTION_STEPS):
        mid = (lo + hi) / 2
        if sojourn_tail(mid, k, mu, lam) > target:
            lo = mid
        else:
            hi = mid
    c = erlang_c(k, lam / mu)
    return RetryTimeoutResult(rate_per_s, rho, c, 1000 * (1 / mu + c / theta), 1000 * hi)


# Design: abstraction/benchmark_runner/retry_timeout/design.md#output-format
def format_results(results):
    lines = ["rate_per_s\trho\terlang_c\tmean_sojourn_ms\tretry_timeout_ms"]
    for r in results:
        lines.append(f"{_shortest(r.rate_per_s)}\t{r.rho:.4f}\t{r.erlang_c:.6f}\t"
                     f"{r.mean_sojourn_ms:.3f}\t{r.retry_timeout_ms:.3f}")
    lines.append("retry_timeout_ms = [" + ", ".join(_shortest(round(r.retry_timeout_ms, 3)) for r in results) + "]")
    return "\n".join(lines) + "\n"
