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

## EtaSlack

The target probability of the RetryTimeout comes from a target load `eta`:

```
function eta_target(rho, eta):
    return min(1.0, max(0.0, eta - rho))
```

| `eta` vs `rho` | Target | Meaning |
|---|---|---|
| `eta = 1` (default) | `1 − rho` | The original rule: no slack |
| `rho < eta < 1` | `eta − rho` | Slack: fewer healthy requests retry than with `eta = 1` |
| `eta <= rho` | `0` | Infinite RetryTimeout: no request retries |
| `eta >= rho + 1` | `1` | RetryTimeout `0`: every request retries |

`eta − rho` is computed in double precision, so `eta = 0.9, rho = 0.9` gives exactly `0`.

## TimeoutSolve

The **RetryTimeout** is the `t` with `sojourn_tail(t) = target`.

```
function solve_timeout(k, mu, lam, target):                  # seconds
    if target <= 0: return +infinity
    lo = 0.0; hi = 1 / mu
    while sojourn_tail(hi) > target: hi = 2 * hi              # bracket: tail(lo) > target >= tail(hi)
    repeat BISECTION_STEPS times:                             # bisection, BISECTION_STEPS = 200
        mid = (lo + hi) / 2
        if sojourn_tail(mid) > target: lo = mid else: hi = mid
    return hi

function retry_timeout(workers, mean_service_ms, rate_per_s, eta = 1):
    validate(workers, mean_service_ms, rate_per_s, eta)       # Validation
    compute k, mu, lam, rho, theta as in Model
    target = eta_target(rho, eta)                             # EtaSlack
    t = solve_timeout(k, mu, lam, target)
    (stable, tipping) = feedback_points(k, mu, rho, t)        # RetryFeedback
    C = erlang_c(k, lam / mu)
    return RetryTimeoutResult(rate_per_s, rho, C, 1000 * (1/mu + C/theta), target, 1000 * t, stable, tipping)
```

- The bracket loop ends because the tail tends to `0` and `target > 0`. 200 halvings shrink the bracket below one
  ulp of `hi`, so the result is the smallest representable `t` (to one ulp) with `sojourn_tail(t) <= target`.
- `target = 1`: `sojourn_tail(t) > 1` never holds, so the bisection drives `hi` to `2^-200 / mu`, which prints as
  `0.000`.
- The result is never rounded inside the computation. Rounding happens only in the output.

## RetryFeedback

A request whose sojourn time exceeds the RetryTimeout `T` sends one retry copy. The original copy keeps running, so
the copy adds one full service time of work, arriving like the others (Poisson). With `rho0` the utilization
without retries and `r` the utilization the queue actually runs at, the retries raise it to

```
RetryFeedback(r) = rho0 * (1 + sojourn_tail(T, k, mu, r * k * mu))        # T fixed, computed at rho0
```

`sojourn_tail` grows with the load, so `RetryFeedback` is increasing, and `RetryFeedback(rho0) >= rho0`. Iterating
`r = RetryFeedback(r)` from `rho0` therefore climbs to the smallest fixed point above `rho0` if one exists below
`1`, the **StableLoad**, and past `1` otherwise. When `RetryFeedback(1) > 1`, a StableLoad is followed by a second,
unstable fixed point, the **TippingLoad**: a load pushed above it (a burst) keeps growing past `1` instead of
returning to the StableLoad.

The fixed points are found as sign changes of `RetryFeedback(r) − r` on a fixed grid, refined by bisection:

```
function feedback_points(k, mu, rho0, T):                    # -> (stable_rho | none, tipping_rho | none)
    if T = +infinity: return (rho0, none)                     # no retries: the load stays at rho0
    above(r) = rho0 * (1 + sojourn_tail(T, k, mu, r * k * mu)) > r
    refine(lo, hi):                                           # above(lo) != above(hi)
        side = above(lo)
        repeat ROOT_STEPS times:                              # ROOT_STEPS = 100
            mid = (lo + hi) / 2
            if above(mid) = side: lo = mid else: hi = mid
        return hi
    x[i] = rho0 + i * (RHO_MAX - rho0) / FEEDBACK_GRID, i = 0 .. FEEDBACK_GRID    # RHO_MAX = 1 - 1e-9
    if not above(x[0]): stable = x[0]; i = 0
    else:
        i = smallest i >= 1 with not above(x[i]); if none: return (none, none)       # collapse
        stable = refine(x[i-1], x[i])
    j = smallest j > i with above(x[j]); if none: return (stable, none)
    return (stable, refine(x[j-1], x[j]))
```

- In exact arithmetic `above(x[0])` is always true: `T` was solved so that `sojourn_tail(T)` at `rho0` equals the
  target, which is `> 0` here, so `RetryFeedback(rho0) = rho0 × (1 + target) > rho0`. In double precision it is false
  only when `rho0 × (1 + target)` rounds to `rho0`, i.e. for a target below about `1.1e-16` (`1 + target` rounds to
  `1`), as for `eta` one ulp above `rho0`. The StableLoad is then `rho0` itself, which is right to that precision.
- Why the iteration converges, and what the scan and the refinement guarantee in exact arithmetic:
  [Convergence](#convergence).
- Two crossings closer together than one grid interval (`(RHO_MAX − rho0) / FEEDBACK_GRID`, about `2e-5` at
  `rho0 = 0.6`) are not seen, and count as no StableLoad. That happens only for an `eta` just below the
  MaxStableEta, where the two fixed points are about to meet, so it can only make the MaxStableEta slightly lower.

## MaxStableEta

A larger `eta` gives a larger target, a shorter RetryTimeout, more retries and a higher `RetryFeedback`, so the set of
stable `eta` is an interval starting at `rho0`. Its upper end, the **MaxStableEta**, is found by bisection:

```
function max_stable_eta(workers, mean_service_ms, rate_per_s):
    validate(workers, mean_service_ms, rate_per_s, 1)
    compute k, mu, lam, rho as in Model
    stable(eta) = feedback_points(k, mu, rho, solve_timeout(k, mu, lam, eta_target(rho, eta))).stable is not none
    lo = rho; hi = rho + 1                                    # target 0 (stable) and target 1
    if stable(hi):                                            # 2 rho < 1: even "retry everything" settles at 2 rho
        best = +infinity; target = 1
    else:
        repeat ETA_STEPS times:                               # ETA_STEPS = 40, final bracket 2^-40
            mid = (lo + hi) / 2
            if stable(mid): lo = mid else: hi = mid
        best = lo; target = eta_target(rho, lo)
    t = solve_timeout(k, mu, lam, target)
    return StableEtaResult(rate_per_s, rho, best, target, 1000 * t, feedback_points(k, mu, rho, t).stable)
```

- At the MaxStableEta the StableLoad and the TippingLoad meet, so there is no margin against bursts: an operating
  `eta` has to sit below it.
- `target = 1` is set directly in the unbounded case, because `rho + 1 − rho` may round below `1`.
- Why the stable `eta` form one interval, so that bisection finds its end: [Convergence](#convergence).

## Convergence

Statements in exact real arithmetic about the RetryFeedback iteration, `feedback_points` and `max_stable_eta`. The
algorithms above do not depend on them; they say why the algorithms find what they claim to. Results marked
**Lean** are machine-checked in [proofs/](proofs/RetryTimeout.lean) (Lean 4 + Mathlib, see `AGENTS.md`
"Machine-checked proofs"); results marked **prose** are argued here only.

Notation: `f(r) = RetryFeedback(r) = rho0 × (1 + p_r(T))` for a fixed RetryTimeout `T`, where
`p_r(T) = sojourn_tail(T, k, mu, r × k × mu)`, and `g(r) = f(r) − r`. A fixed point of `f` is a zero of `g`.

### Assumptions

The Lean proofs hold for every `f` with H1–H5 (the fields of `FeedbackHyp`) and every family `F(eta, r)` with H6.
That the M/M/k RetryFeedback has them is the following **prose** argument (Lemma 1), not machine-checked.

| Id | Assumption | Lean | Why it holds for M/M/k |
|---|---|---|---|
| H1 | `0 < rho0 < 1` | `FeedbackHyp.pos`, `.lt_one` | Validation |
| H2 | `f` continuous on `[rho0, 1]` | `FeedbackHyp.cont` | the tail formula is continuous in `lam < k mu` (including the `theta = mu` limit); at `1`, H5 is the limit |
| H3 | `f` nondecreasing on `[rho0, 1]` | `FeedbackHyp.mono` | Lemma 1(b) |
| H4 | `rho0 <= f(rho0)` | `FeedbackHyp.start` | `f(rho0) = rho0 (1 + target)`, `target >= 0` |
| H5 | `f(1) = 2 rho0` | `FeedbackHyp.at_one` | Lemma 1(c): defines `f` at `1` as its limit |
| H6 | `F(eta, r)` nondecreasing in `eta` at every `r` | hypothesis `hF`; derived from monotone parts by `F_mono_of_parts` | the target is nondecreasing in `eta` (`eta_target_mono`), `T` is decreasing in the target (Lemma 1(a)), the tail is decreasing in `T` (Lemma 1(a)) |

**Lemma 1 (prose).** Write the sojourn time as `S + W` with `S ~ Exp(mu)`, and `W = 0` with probability `1 − C`,
otherwise `W ~ Exp(theta)`, `theta = k mu − lam`. Then
`p = Pr(S > t) + C × D(theta)` with `D(theta) = Pr(S + E_theta > t) − Pr(S > t)`. For `t > 0`:
- (a) `p` is continuous and strictly decreasing in `t`, from `1` at `t = 0` to `0`.
- (b) `p` is strictly increasing in `lam`: `D > 0` and grows as `theta` falls (`E_theta` grows stochastically),
  `theta` falls as `lam` grows, and Erlang C is strictly increasing in the offered load. A product of positive
  increasing factors increases.
- (c) `p → 1` as `lam → k mu`: `C → 1` and `theta → 0`, so `W → ∞` in probability.

### Results

| Result | Statement | Lean declaration |
|---|---|---|
| Theorem 1, IVT | `a <= b`, `f` continuous on `[a, b]`, `a <= f(a)`, `f(b) <= b` give a fixed point in `[a, b]` | [`exists_fixed_between`](proofs/RetryTimeout/Iteration.lean) |
| Theorem 1, monotone | From `a <= f(a)` below a fixed point `s`, the iterates rise and stay `<= s` | [`iter_mono_bdd`](proofs/RetryTimeout/Iteration.lean) |
| Theorem 1(a) | If a fixed point exists in `[rho0, 1]`, a least one (the StableLoad) exists, and the iteration from `rho0` increases to it | [`iter_tendsto_least_fixed`](proofs/RetryTimeout/Iteration.lean) |
| Theorem 1(b) | `2 rho0 > 1` and no fixed point in `[rho0, 1)`: `g` has a minimum `delta > 0` on `[rho0, 1]`, and the iteration passes `1` within `floor((1 − rho0) / delta) + 1` steps (collapse) | [`iter_exceeds_one`](proofs/RetryTimeout/Iteration.lean) |
| Theorem 1(c) | `2 rho0 <= 1`: a fixed point always exists in `[rho0, 1]` | [`fixed_exists_of_two_rho_le_one`](proofs/RetryTimeout/Iteration.lean) |
| Theorem 1(e) | Basin: from `x0 > s` with `f(r) <= r` on `[s, x0]` and no other fixed point there (below the TippingLoad), the iterates fall back to `s` | [`basin_down`](proofs/RetryTimeout/Iteration.lean) |
| Stability test | A fixed point exists in `[rho0, 1]` exactly when `f(r) <= r` for some `r` there (what `above` and MaxStableEta test) | [`stable_iff_exists_le`](proofs/RetryTimeout/Iteration.lean) |
| Theorem 2, invariant | `refine` keeps `above(lo) = side ≠ above(hi)`, stays in the start bracket, and halves the width each step | [`refine_invariant`](proofs/RetryTimeout/Bisection.lean) |
| Theorem 2, soundness | With `g` continuous and `above` different at the ends, the final bracket holds a zero `z` and `|hi_n − z| <= (hi − lo) / 2^n` | [`refine_sound`](proofs/RetryTimeout/Bisection.lean) |
| Theorem 2, scan | If grid point `j > 0` is the first not `above`, all earlier ones are `above` and the cell `[x[j−1], x[j]]` holds a zero | [`scan_first_cell`](proofs/RetryTimeout/Bisection.lean) |
| H6, parts | The EtaSlack target is nondecreasing in `eta`; monotone parts compose to H6 | [`eta_target_mono`](proofs/RetryTimeout/EtaSearch.lean), [`F_mono_of_parts`](proofs/RetryTimeout/EtaSearch.lean) |
| Theorem 3(a) | The stable `eta` form a down-set: one interval from `rho0` up to the MaxStableEta | [`stab_antitone`](proofs/RetryTimeout/EtaSearch.lean) |
| Theorem 3(c) | The `eta` bisection keeps `stable(lo)`, `not stable(hi)`, width `(hi − lo) / 2^n`, and `lo_n <= sup{eta stable} <= hi_n` | [`etaBisect_brackets`](proofs/RetryTimeout/EtaSearch.lean) |
| Theorem 3(d) | The grid test is a down-set too and implies the exact test, so the grid MaxStableEta never exceeds the exact one | [`stabGrid_antitone`](proofs/RetryTimeout/EtaSearch.lean), [`stabGrid_imp_stab`](proofs/RetryTimeout/EtaSearch.lean), [`maxStableEta_grid_le`](proofs/RetryTimeout/EtaSearch.lean) |
| Rate | If `f'(s) < 1` the iteration converges linearly, `s − r_n = O(q^n)` for any `q` in `(f'(s), 1)`; at a tangency (`f'(s) = 1`, `f''(s) ≠ 0`) only `Θ(1/n)` | prose |
| Grid error | With a nondegenerate fold at the MaxStableEta (`g''(r*) = 2a > 0`, `∂g/∂eta >= c > 0` near it), the grid result is at least `eta* − a h² / (4c) − o(h²)`, `h` the grid spacing | prose |
| Fixed-point count | At most two fixed points (StableLoad, TippingLoad) below `1` | open: holds on a numerical scan of M/M/10 (ρ₀ 0.1–0.98, 199 values of `eta`), not proven |

The Lean statements use `[rho0, 1]` with `f(1) = 2 rho0`. When `2 rho0 > 1` the point `1` is never a fixed point, so
this is the same as the `[rho0, 1)` of RetryFeedback; when `2 rho0 = 1` the fixed point may be `1` itself (the
`rate = 50` edge of the `workers = 10` examples).

Check, from `proofs/`: `lake exe cache get && lake build`, then `lake env lean CheckAxioms.lean`, which prints only
`[propext, Classical.choice, Quot.sound]` for each theorem.

## Validation

`validate(workers, mean_service_ms, rate_per_s, eta)` checks in this order; the first failure raises `ValueError`
with the message shown.

| Check | Message |
|---|---|
| `workers` is an integer (not a boolean) and `>= 1` | `workers must be an integer >= 1` |
| `mean_service_ms` is a number and `> 0` | `mean_service_ms must be a number > 0` |
| `rate_per_s` is a number and `> 0` | `rate must be a number > 0` |
| `eta` is a number and `>= 0` | `eta must be a number >= 0` |
| `rho < 1`, i.e. `rate_per_s < workers × 1000 / mean_service_ms` | `rate <rate_per_s> req/s needs rho < 1: capacity is <workers × 1000 / mean_service_ms> req/s` |

Numbers in messages are written in their shortest round-trip decimal form.

## Output format

```
function format_results(results):
    lines = ["rate_per_s\trho\terlang_c\tmean_sojourn_ms\ttarget\tretry_timeout_ms\tstable_rho\ttipping_rho"]
    for r in results (in the order given):
        lines.append(shortest(r.rate_per_s) + "\t" + fixed(r.rho, 4) + "\t" + fixed(r.erlang_c, 6) + "\t"
                     + fixed(r.mean_sojourn_ms, 3) + "\t" + fixed(r.target, 6) + "\t"
                     + fixed_or(r.retry_timeout_ms, 3, "inf") + "\t"
                     + fixed_or(r.stable_rho, 4, "none") + "\t" + fixed_or(r.tipping_rho, 4, "none"))
    toml = [NO_RETRY_TIMEOUT_MS if r.retry_timeout_ms = +infinity else round(r.retry_timeout_ms, 3) for r in results]
    lines.append("retry_timeout_ms = [" + ", ".join(shortest(v) for v in toml) + "]")
    return join(lines, "\n") + "\n"

function format_stable_eta(results):
    lines = ["rate_per_s\trho\tmax_stable_eta\ttarget\tretry_timeout_ms\tstable_rho"]
    for r in results (in the order given):
        lines.append(shortest(r.rate_per_s) + "\t" + fixed(r.rho, 4) + "\t" + fixed_or(r.max_stable_eta, 6, "inf")
                     + "\t" + fixed(r.target, 6) + "\t" + fixed(r.retry_timeout_ms, 3) + "\t" + fixed(r.stable_rho, 4))
    return join(lines, "\n") + "\n"
```

- `shortest(x)` is the shortest decimal that parses back to the same double; an integral value is written without
  a fraction (`60`, not `60.0`). `fixed(x, d)` rounds to `d` decimals, half to even on the double's exact value.
  `fixed_or(x, d, word)` is `word` when `x` is none or infinite, and `fixed(x, d)` otherwise.
- `round(x, 3)` rounds to the nearest 0.001 ms (1 µs). The TOML line pastes directly into `[load_gen]`. An
  infinite RetryTimeout is written as `NO_RETRY_TIMEOUT_MS` (one day), which is longer than any `slo_ms` or run, so
  its RetryTimer never fires; BenchConfig accepts only finite values.

## Worked examples

`workers = 20`, `mean_service_ms = 200` (`mu = 5/s`, `k mu = 100/s`), default `eta = 1`, the 2 × 10-worker
experiments pooled:

| `rate_per_s` | `rho` | `erlang_c` | `mean_sojourn_ms` | `target` | `retry_timeout_ms` | `stable_rho` | Check: `sojourn_tail(T)` vs `1 − rho` |
|---|---|---|---|---|---|---|---|
| 60 | 0.6000 | 0.024134 | 200.603 | 0.400000 | 183.945 | none | little queueing, so close to the service-only `200 × −ln(0.4) = 183.258` |
| 70 | 0.7000 | 0.093561 | 203.119 | 0.300000 | 244.494 | none | service-only would be `240.795` |
| 80 | 0.8000 | 0.256078 | 212.804 | 0.200000 | 338.171 | none | service-only would be `321.888` |
| 90 | 0.9000 | 0.550769 | 255.077 | 0.100000 | 543.521 | none | service-only would be `460.517` |

The TOML line is `retry_timeout_ms = [183.945, 244.494, 338.171, 543.521]`. `tipping_rho` is `none` on every row.
No row has a StableLoad: with `eta = 1`, `RetryFeedback(rho0) = rho0 (2 − rho0)`, which is already `0.84` at
`rho0 = 0.6`, and the load keeps climbing past `1`.

`workers = 10`, `mean_service_ms = 200` (one server instance of the same experiments, at half the global rate),
with EtaSlack:

| `rate_per_s` | `eta` | `target` | `retry_timeout_ms` | `stable_rho` | `tipping_rho` | Check |
|---|---|---|---|---|---|---|
| 30 | 0.8 | 0.200000 | 328.482 | 0.7343 | 0.9452 | `0.6 × (1 + 0.2) = 0.72` after one step; settles at `0.7343` |
| 35 | 0.8 | 0.100000 | 481.383 | 0.7816 | 0.9288 | `0.7 × 1.1 = 0.77` after one step |
| 40 | 0.8 | 0.000000 | inf | 0.8000 | none | `eta <= rho`: no retries; TOML value `86400000` |
| 30 | 0.90000001 | 0.300000 | 247.276 | none | none | `0.6 × 1.3 = 0.78` after one step, then past `1` |
| 45 | 0.90000001 | 0.000000 | 4227.544 | 0.9000 | 0.9887 | target `1e-8` prints as `0.000000` |

MaxStableEta, same `workers = 10`, `mean_service_ms = 200`:

| `rate_per_s` | `rho` | `max_stable_eta` | `target` | `retry_timeout_ms` | `stable_rho` | Check |
|---|---|---|---|---|---|---|
| 30 | 0.6000 | 0.887554 | 0.287554 | 255.770 | 0.8609 | `target = eta − rho`; `eta = 0.8` (above) is stable, `0.90000001` is not |
| 35 | 0.7000 | 0.842597 | 0.142597 | 410.244 | 0.8628 | |
| 40 | 0.8000 | 0.857214 | 0.057214 | 638.392 | 0.8848 | |
| 45 | 0.9000 | 0.912216 | 0.012216 | 1203.908 | 0.9261 | |
| 20 | 0.4000 | inf | 1.000000 | 0.000 | 0.8000 | `2 rho = 0.8 < 1`: every request retrying still settles |

Boundary and closed-form cases:

| Inputs | Result | Check |
|---|---|---|
| `workers = 1`, `mean_service_ms = 200`, rate `4` (M/M/1) | `erlang_c = 0.8`, `mean_sojourn_ms = 1000.000`, `retry_timeout_ms = 1609.438` | M/M/1: `T ~ Exp(mu − lam)`, so `T = −ln(1 − rho) / (mu − lam) = −ln(0.2) / 1 s = 1609.438 ms`; `C = rho = 0.8`; `E[T] = 1/(5 − 4) s` |
| `workers = 10`, `mean_service_ms = 200`, rate `45` (`theta = 5 = mu`) | `erlang_c = 0.668732`, `mean_sojourn_ms = 333.746`, `retry_timeout_ms = 702.181` | the `theta = mu` branch; rates `45 ± 1e-7` give `702.181` through the general formula |
| `workers = 1`, `mean_service_ms = 1`, rate `1e-6` | `rho = 0.0000`, `retry_timeout_ms = 0.000` | `1 − rho → 1`, so `T → 0` |
| `workers = 1`, `mean_service_ms = 200`, rate `0.5`, `eta = 2` | `target = 1.000000`, `retry_timeout_ms = 0.000`, `stable_rho = 0.2000`, `tipping_rho = none` | every request retries: `RetryFeedback = 2 rho0 = 0.2` |
| `workers = 20`, `mean_service_ms = 200`, rate `100` | ValueError `rate 100 req/s needs rho < 1: capacity is 100 req/s` | `rho = 1` |
| `workers = 0` | ValueError `workers must be an integer >= 1` | |
| `eta = −0.1` | ValueError `eta must be a number >= 0` | |

## Failure and edge cases

- No rate given, or a flag missing, is a usage error of the command (exit `2`), reported before any computation.
- A rate that fails validation stops the command before anything is printed, even if other rates are valid. The
  same holds for `bench stable-eta`.
- Very large `workers` (thousands) are fine: the Erlang B recursion is `O(k)` and stays in `[0, 1]`. RetryFeedback
  costs `O(FEEDBACK_GRID × k)` per call, and MaxStableEta calls it up to `ETA_STEPS + 2` times, so large `k` is slow
  but correct.
- `rho0` close to `1`: the grid still spans `[rho0, RHO_MAX]` with `FEEDBACK_GRID` intervals; a StableLoad above
  `RHO_MAX` is reported as none.
