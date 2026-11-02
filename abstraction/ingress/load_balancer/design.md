# Load Balancer — Design

`B` is the number of backends. All counters are atomic u64, incremented and decremented with relaxed ordering;
`stats_snapshot` runs after every forward has ended or been abandoned, so its loads are final.

## LoadBalancer

```
lb = { policy, stats: array[B] of BackendStats (all 0), rr: atomic u64 = 0,
       rng: rng_new(derive_seed(seed, STREAM_INGRESS_LB, 0)), rng_lock: mutex }

function pick(lb):
    s = choose(lb)                                   # LbPolicy
    lb.stats[s].outstanding.fetch_add(1)
    lb.stats[s].forwarded.fetch_add(1)
    return s

function finish(lb, s, outcome):
    lb.stats[s].outstanding.fetch_sub(1)
    lb.stats[s][outcome].fetch_add(1)                # ok | rejected | bad_request | bad_gateway | reset
```

`outstanding` is incremented in `pick`, not when the forward actually starts, so the next `pick` already sees the
load it is about to add. That narrows, but does not close, the race described below.

## LbPolicy

```
function choose(lb):
    if B == 1: return 0                              # every policy; no random draw, no counter change
    match lb.policy:
        round_robin:
            return lb.rr.fetch_add(1) mod B          # u64 counter: cannot wrap in any realistic run
        least_outstanding:
            best = 0
            for s in 1 ..= B-1:
                if outstanding[s].load() < outstanding[best].load(): best = s      # strict <: ties go to lower id
            return best
        power_of_two:
            with lb.rng_lock:
                i = uniform_int(lb.rng, 0, B-1)
                j = uniform_int(lb.rng, 0, B-2)
            if j >= i: j = j + 1                     # j is uniform over the B-1 backends other than i
            a, b = min(i, j), max(i, j)
            return b if outstanding[b].load() < outstanding[a].load() else a       # ties go to lower id
```

- **Concurrent picks race.** Two picks at the same moment may read the same counters and choose the same backend.
  That is accepted: the policies are heuristics, and the counters correct themselves on the next pick. No lock is
  taken around the read and the increment.
- **Reproducibility.** `round_robin` and `least_outstanding` use no randomness. `power_of_two` draws two numbers
  per pick (when `B > 1`), in pick order; with concurrent clients the pick order, and so the draw-to-request
  assignment, depends on timing.
- `least_outstanding` reads each counter once per comparison; the counters may change during the scan. The
  result is the minimum among the values seen.

## Worked examples

`round_robin`, `B = 2`: picks return `0, 1, 0, 1, …` (rr = 0, 1, 2, 3, …).

`least_outstanding`, `B = 3`:

| `outstanding` before | Choice | Why |
|---|---|---|
| (0, 0, 0) | 0 | all tied: lowest id |
| (1, 0, 0) | 1 | 0 < 1; tie between 1 and 2 goes to 1 |
| (1, 1, 0) | 2 | strictly smallest |
| (2, 1, 1) | 1 | tie between 1 and 2 goes to 1 |

`power_of_two`, `B = 3`, `outstanding = (2, 0, 1)`:

| draws `(i, j)` | `j` after skip | Pair `(a, b)` | Choice | Why |
|---|---|---|---|---|
| (0, 0) | 1 | (0, 1) | 1 | 0 < 2 |
| (2, 1) | 1 | (1, 2) | 1 | 0 < 1 |
| (0, 1) | 2 | (0, 2) | 2 | 1 < 2 |
| (2, 0) | 0 | (0, 2) | 2 | 1 < 2 |

`B = 1`: every policy returns 0 and draws nothing.
