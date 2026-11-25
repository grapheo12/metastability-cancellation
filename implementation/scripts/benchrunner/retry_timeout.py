# Tooling language: Python 3.11+ (shared tool implementation/scripts)
# Source: abstraction/benchmark_runner/retry_timeout/design.md (retry_timeout v0.3.0)
"""RetryTimeoutCalculator: the RetryTimeout T with Pr(sojourn > T) = EtaSlack target in an M/M/k model,
the RetryFeedback fixed points at that T, and the MaxStableEta search."""

import math
from dataclasses import dataclass

BISECTION_STEPS = 200
THETA_EQ_MU_REL = 1e-12  # |theta - mu| <= this * mu uses the theta = mu limit
FEEDBACK_GRID = 20000  # grid intervals of the RetryFeedback scan
RHO_MAX = 1 - 1e-9  # right end of the RetryFeedback scan
ROOT_STEPS = 100  # bisection steps refining one RetryFeedback crossing
ETA_STEPS = 40  # bisection steps of the MaxStableEta search
NO_RETRY_TIMEOUT_MS = 86400000  # an infinite RetryTimeout in the TOML line: one day


# Design: abstraction/benchmark_runner/retry_timeout/design.md#timeoutsolve
@dataclass
class RetryTimeoutResult:
    rate_per_s: float
    rho: float
    erlang_c: float
    mean_sojourn_ms: float
    target: float
    retry_timeout_ms: float  # math.inf when target = 0
    stable_rho: float | None
    tipping_rho: float | None


# Design: abstraction/benchmark_runner/retry_timeout/design.md#maxstableeta
@dataclass
class StableEtaResult:
    rate_per_s: float
    rho: float
    max_stable_eta: float  # math.inf when every eta is stable
    target: float
    retry_timeout_ms: float
    stable_rho: float


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
def validate(workers, mean_service_ms, rate_per_s, eta=1.0):
    if not isinstance(workers, int) or isinstance(workers, bool) or workers < 1:
        raise ValueError("workers must be an integer >= 1")
    if not _is_number(mean_service_ms) or mean_service_ms <= 0:
        raise ValueError("mean_service_ms must be a number > 0")
    if not _is_number(rate_per_s) or rate_per_s <= 0:
        raise ValueError("rate must be a number > 0")
    if not _is_number(eta) or eta < 0:
        raise ValueError("eta must be a number >= 0")
    mu = 1000 / mean_service_ms
    if rate_per_s / (workers * mu) >= 1:
        raise ValueError(f"rate {_shortest(rate_per_s)} req/s needs rho < 1: "
                         f"capacity is {_shortest(workers * 1000 / mean_service_ms)} req/s")


# Design: abstraction/benchmark_runner/retry_timeout/design.md#etaslack
def eta_target(rho, eta):
    return min(1.0, max(0.0, eta - rho))


# Design: abstraction/benchmark_runner/retry_timeout/design.md#timeoutsolve
def solve_timeout(k, mu, lam, target):
    """Smallest t (seconds, to one ulp) with sojourn_tail(t) <= target; math.inf when target = 0."""
    if target <= 0:
        return math.inf
    lo, hi = 0.0, 1 / mu
    while sojourn_tail(hi, k, mu, lam) > target:  # bracket: tail(lo) > target >= tail(hi)
        hi = 2 * hi
    for _ in range(BISECTION_STEPS):
        mid = (lo + hi) / 2
        if sojourn_tail(mid, k, mu, lam) > target:
            lo = mid
        else:
            hi = mid
    return hi


# Design: abstraction/benchmark_runner/retry_timeout/design.md#retryfeedback
def feedback_points(k, mu, rho0, timeout_s):
    """(stable_rho, tipping_rho) of r -> rho0 * (1 + Pr(sojourn > timeout_s | load r)); None when absent."""
    if timeout_s == math.inf:
        return rho0, None

    def above(r):  # RetryFeedback(r) > r
        return rho0 * (1 + sojourn_tail(timeout_s, k, mu, r * k * mu)) > r

    def refine(lo, hi):  # above(lo) != above(hi)
        side = above(lo)
        for _ in range(ROOT_STEPS):
            mid = (lo + hi) / 2
            if above(mid) == side:
                lo = mid
            else:
                hi = mid
        return hi

    xs = [rho0 + i * (RHO_MAX - rho0) / FEEDBACK_GRID for i in range(FEEDBACK_GRID + 1)]
    if not above(xs[0]):
        stable, i = xs[0], 0
    else:
        i = next((i for i in range(1, len(xs)) if not above(xs[i])), None)
        if i is None:
            return None, None
        stable = refine(xs[i - 1], xs[i])
    j = next((j for j in range(i + 1, len(xs)) if above(xs[j])), None)
    return stable, (None if j is None else refine(xs[j - 1], xs[j]))


# Design: abstraction/benchmark_runner/retry_timeout/design.md#model
# Design: abstraction/benchmark_runner/retry_timeout/design.md#timeoutsolve
def retry_timeout(workers, mean_service_ms, rate_per_s, eta=1.0):
    validate(workers, mean_service_ms, rate_per_s, eta)
    k, mu, lam = workers, 1000 / mean_service_ms, rate_per_s
    rho = lam / (k * mu)
    theta = k * mu - lam
    target = eta_target(rho, eta)
    t = solve_timeout(k, mu, lam, target)
    stable, tipping = feedback_points(k, mu, rho, t)
    c = erlang_c(k, lam / mu)
    return RetryTimeoutResult(rate_per_s, rho, c, 1000 * (1 / mu + c / theta), target, 1000 * t, stable, tipping)


# Design: abstraction/benchmark_runner/retry_timeout/design.md#maxstableeta
def max_stable_eta(workers, mean_service_ms, rate_per_s):
    validate(workers, mean_service_ms, rate_per_s)
    k, mu, lam = workers, 1000 / mean_service_ms, rate_per_s
    rho = lam / (k * mu)

    def stable(eta):
        return feedback_points(k, mu, rho, solve_timeout(k, mu, lam, eta_target(rho, eta)))[0] is not None

    lo, hi = rho, rho + 1  # eta = rho: no retries, stable; eta = rho + 1: target 1, every request retries
    if stable(hi):
        best, at = math.inf, hi  # reported at target 1 exactly (rho + 1 - rho may round below 1)
    else:
        for _ in range(ETA_STEPS):
            mid = (lo + hi) / 2
            if stable(mid):
                lo = mid
            else:
                hi = mid
        best = at = lo
    target = 1.0 if best == math.inf else eta_target(rho, at)
    t = solve_timeout(k, mu, lam, target)
    return StableEtaResult(rate_per_s, rho, best, target, 1000 * t, feedback_points(k, mu, rho, t)[0])


def _fixed_or(x, d, absent):
    return absent if x is None or x == math.inf else f"{x:.{d}f}"


# Design: abstraction/benchmark_runner/retry_timeout/design.md#output-format
def format_results(results):
    lines = ["rate_per_s\trho\terlang_c\tmean_sojourn_ms\ttarget\tretry_timeout_ms\tstable_rho\ttipping_rho"]
    for r in results:
        lines.append(f"{_shortest(r.rate_per_s)}\t{r.rho:.4f}\t{r.erlang_c:.6f}\t{r.mean_sojourn_ms:.3f}\t"
                     f"{r.target:.6f}\t{_fixed_or(r.retry_timeout_ms, 3, 'inf')}\t"
                     f"{_fixed_or(r.stable_rho, 4, 'none')}\t{_fixed_or(r.tipping_rho, 4, 'none')}")
    toml = [NO_RETRY_TIMEOUT_MS if r.retry_timeout_ms == math.inf else round(r.retry_timeout_ms, 3) for r in results]
    lines.append("retry_timeout_ms = [" + ", ".join(_shortest(v) for v in toml) + "]")
    return "\n".join(lines) + "\n"


# Design: abstraction/benchmark_runner/retry_timeout/design.md#output-format
def format_stable_eta(results):
    lines = ["rate_per_s\trho\tmax_stable_eta\ttarget\tretry_timeout_ms\tstable_rho"]
    for r in results:
        lines.append(f"{_shortest(r.rate_per_s)}\t{r.rho:.4f}\t{_fixed_or(r.max_stable_eta, 6, 'inf')}\t"
                     f"{r.target:.6f}\t{r.retry_timeout_ms:.3f}\t{r.stable_rho:.4f}")
    return "\n".join(lines) + "\n"
