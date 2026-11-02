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

## Design specific to Go

Implementation: `implementation/go` (module `testbed`, Go 1.21, `golang.org/x/net v0.17.0`).
Build and test with `CGO_ENABLED=0 go build ./...` / `CGO_ENABLED=0 go test ./...`. On recent macOS the dynamic
loader rejects cgo-linked Go 1.21 binaries (missing `LC_UUID`), so cgo is disabled.

### Go choices

- File `internal/ingress/balancer.go`. BackendStats fields are `atomic.Int64` (`outstanding`) and `atomic.Uint64` (the rest); `ForwardOutcome` is an `int` enum.
- `round_robin` is `rr.Add(1) - 1` mod B. The `power_of_two` draws happen under `rngMu`; mapping the draws to a backend is the pure function `powerOfTwoChoice(i, j, outstanding)`, so the worked example is tested without randomness.
- Go 1.21 built-in `min`/`max` order the pair.
- Tests: `balancer_test.go` checks the worked example of every policy, that `B = 1` picks 0 without drawing, and that `pick`/`finish` keep the counters.

### Traceability

Generated from the `// Design:` tags in the code by `implementation/spec_tools/tracegen.py` (see `AGENTS.md`). Each link points at the first code line after the tag.
Regenerate from the repository root with `python3 implementation/spec_tools/tracegen.py --lang Go --ext .go --comment // --write`.

<!-- trace:begin -->
| Design subsection | Implementation (file:line) |
|---|---|
| [LoadBalancer](#loadbalancer) | [balancer.go:52](../../../implementation/go/internal/ingress/balancer.go#L52), [balancer.go:64](../../../implementation/go/internal/ingress/balancer.go#L64), [balancer.go:76](../../../implementation/go/internal/ingress/balancer.go#L76), [balancer.go:86](../../../implementation/go/internal/ingress/balancer.go#L86), [balancer.go:115](../../../implementation/go/internal/ingress/balancer.go#L115), [balancer_test.go:69](../../../implementation/go/internal/ingress/balancer_test.go#L69) |
| [LbPolicy](#lbpolicy) | [balancer.go:128](../../../implementation/go/internal/ingress/balancer.go#L128), [balancer.go:158](../../../implementation/go/internal/ingress/balancer.go#L158), [balancer_test.go:22](../../../implementation/go/internal/ingress/balancer_test.go#L22), [balancer_test.go:33](../../../implementation/go/internal/ingress/balancer_test.go#L33), [balancer_test.go:49](../../../implementation/go/internal/ingress/balancer_test.go#L49), [config.go:26](../../../implementation/go/internal/ingress/config.go#L26) |
| [Worked examples](#worked-examples) | [balancer_test.go:22](../../../implementation/go/internal/ingress/balancer_test.go#L22), [balancer_test.go:33](../../../implementation/go/internal/ingress/balancer_test.go#L33), [balancer_test.go:49](../../../implementation/go/internal/ingress/balancer_test.go#L49) |
<!-- trace:end -->
