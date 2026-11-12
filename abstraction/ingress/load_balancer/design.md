# Load Balancer — Design

`B` is the number of backends. All counters are atomic u64, incremented and decremented with relaxed ordering.
Each backend's `ewma_latency_ns` is an atomic u64 holding the bits of an IEEE-754 binary64 value
([EwmaLatency](#ewmalatency)). `stats_snapshot` runs after every forward has ended or been abandoned, so its loads
are final. `monotonic_now_ns()` is the Ingress's monotonic clock ([../design.md#time-base](../design.md#time-base)).

## LoadBalancer

```
lb = { policy, beta: ewma_beta,
       stats: array[B] of BackendStats (counters 0, ewma_latency_ns = EWMA_INITIAL_LATENCY_NS),
       rr: atomic u64 = 0,
       rng: rng_new(derive_seed(seed, STREAM_INGRESS_LB, 0)), rng_lock: mutex }

function pick(lb):
    s = choose(lb)                                   # LbPolicy
    lb.stats[s].outstanding.fetch_add(1)
    lb.stats[s].forwarded.fetch_add(1)
    return ForwardTicket(server_id = s, start_ns = monotonic_now_ns())

function finish(lb, ticket, outcome):
    s = ticket.server_id
    lb.stats[s].outstanding.fetch_sub(1)
    lb.stats[s][outcome].fetch_add(1)                # ok | rejected | bad_request | bad_gateway | reset
    if outcome == ok:
        ewma_update(lb, s, monotonic_now_ns() - ticket.start_ns)       # EwmaLatency
```

`outstanding` is incremented in `pick`, not when the forward actually starts, so the next `pick` already sees the
load it is about to add. That narrows, but does not close, the race described below.

## ForwardTicket

- `start_ns` is read in `pick` after the counters are incremented, so the latency covers the upstream connection,
  the backend's work and the response, and excludes the choice itself.
- `finish` reads the clock once, after the counters. The latency is `now - start_ns` as unsigned 64-bit; the clock
  is monotonic, so it is never negative.
- The ticket is a plain value: the LoadBalancer keeps no per-forward state, and a lost ticket only loses a sample.

## EwmaLatency

```
function ewma_update(lb, s, sample_ns):              # sample_ns: u64
    x = float(sample_ns)                             # binary64, round to nearest
    loop:
        old_bits = lb.stats[s].ewma_latency_ns.load()
        old = binary64_from_bits(old_bits)
        new = old * lb.beta + x * (1 - lb.beta)      # binary64, evaluated left to right, round to nearest
        if lb.stats[s].ewma_latency_ns.compare_and_swap(old_bits, bits_of(new)): return
```

- **Which samples.** Only forwards that end `ok` (a `202` relayed) are samples. `rejected`, `bad_request`,
  `bad_gateway` and `reset` leave the EwmaLatency unchanged: a fast `503` or a failed dial would make a busy or dead
  backend look fast, and a reset forward has no complete response time.
- **Every policy.** The update runs under every LbPolicy, so the summary's `ewma_latency_ns` is meaningful for all of
  them. Only `ewma` reads it to choose.
- **Start value.** `EWMA_INITIAL_LATENCY_NS = 1_000_000`, not 0, so that `ewma` compares `outstanding + 1` between
  backends without samples instead of seeing every score as 0.
- **Range.** `0 <= beta <= 1`. `beta = 0` keeps only the newest sample. `beta = 1` never changes the start value,
  which makes `ewma` compare `outstanding` alone on the power_of_two pair.
- **Concurrent updates.** The compare-and-swap loop applies each sample exactly once. When several finish at once,
  they apply in the order the compare-and-swaps succeed, which depends on timing. No lock is taken.

## EwmaScore

```
function ewma_score(lb, s):                          # binary64
    o = lb.stats[s].outstanding.load()
    e = binary64_from_bits(lb.stats[s].ewma_latency_ns.load())
    return float(o + 1) * e
```

`+ 1` counts the forward that is about to be added, so a backend with nothing in progress is still scored by its
latency. `o` and `e` are read separately and may change between the two loads; the score is a heuristic and the
race is accepted, as for the other counters.

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
            (a, b) = random_pair(lb)
            return b if outstanding[b].load() < outstanding[a].load() else a       # ties go to lower id
        ewma:
            (a, b) = random_pair(lb)
            return b if ewma_score(lb, b) < ewma_score(lb, a) else a               # EwmaScore; ties go to lower id

function random_pair(lb):                            # two different backends, a < b
    with lb.rng_lock:
        i = uniform_int(lb.rng, 0, B-1)
        j = uniform_int(lb.rng, 0, B-2)
    if j >= i: j = j + 1                             # j is uniform over the B-1 backends other than i
    return (min(i, j), max(i, j))
```

- **Concurrent picks race.** Two picks at the same moment may read the same counters and choose the same backend.
  That is accepted: the policies are heuristics, and the counters correct themselves on the next pick. No lock is
  taken around the read and the increment.
- **Reproducibility.** `round_robin` and `least_outstanding` use no randomness. `power_of_two` and `ewma` draw two
  numbers per pick (when `B > 1`), in pick order; with concurrent clients the pick order, and so the
  draw-to-request assignment, depends on timing. `ewma` also depends on measured latencies, so its choices are not
  reproducible across runs even with one client.
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

`ewma_update`, binary64 (`1 - 0.7` is `0.30000000000000004`; every result below is exact after rounding):

| `old` (ns) | `sample_ns` | `beta` | `new` (ns) | Check |
|---|---|---|---|---|
| 1_000_000 | 2_000_000 | 0.7 | 1_300_000 | 700_000 + 600_000 |
| 1_000_000 | 500_000 | 0.7 | 850_000 | 700_000 + 150_000 |
| 1_000_000 | 0 | 0.7 | 700_000 | 700_000 + 0 |
| 1_300_000 | 1_300_000 | 0.7 | 1_300_000 | a sample equal to the average leaves it unchanged |
| 1_000_000 | 3_000_000 | 0.5 | 2_000_000 | 500_000 + 1_500_000 |
| 1_000_000 | 3_000_000 | 0 | 3_000_000 | `beta = 0`: newest sample only |
| 1_000_000 | 3_000_000 | 1 | 1_000_000 | `beta = 1`: never changes |

`ewma`, `B = 3`, `outstanding = (2, 0, 1)`, `ewma_latency_ns = (1_000_000, 4_000_000, 1_500_000)`, so the
EwmaScores are `(3 × 1_000_000, 1 × 4_000_000, 2 × 1_500_000) = (3_000_000, 4_000_000, 3_000_000)`:

| draws `(i, j)` | `j` after skip | Pair `(a, b)` | Choice | Why |
|---|---|---|---|---|
| (0, 0) | 1 | (0, 1) | 0 | 3_000_000 < 4_000_000; `power_of_two` would choose 1 |
| (2, 1) | 1 | (1, 2) | 2 | 3_000_000 < 4_000_000 |
| (0, 1) | 2 | (0, 2) | 0 | 3_000_000 = 3_000_000: tie goes to lower id |
| (2, 0) | 0 | (0, 2) | 0 | same pair, same tie |

`ewma` at start, `B = 3`, `outstanding = (0, 0, 0)`, every `ewma_latency_ns = 1_000_000`: every score is
`1 × 1_000_000`, so every pair is a tie and the choice is its lower id. With `outstanding = (0, 1, 0)` and draws
`(0, 0)`: pair `(0, 1)`, scores `1_000_000 < 2_000_000`, choice 0.

`B = 1`: every policy returns 0 and draws nothing.

## Design specific to Go

Implementation: `implementation/go` (module `testbed`, Go 1.21, `golang.org/x/net v0.17.0`).
Build and test with `CGO_ENABLED=0 go build ./...` / `CGO_ENABLED=0 go test ./...`. On recent macOS the dynamic
loader rejects cgo-linked Go 1.21 binaries (missing `LC_UUID`), so cgo is disabled.

### Go choices

- File `internal/ingress/balancer.go`. BackendStats fields are `atomic.Int64` (`outstanding`) and `atomic.Uint64` (the rest); `ForwardOutcome` is an `int` enum.
- EwmaLatency is `BackendStats.EwmaBits atomic.Uint64` holding `math.Float64bits`; `ewmaUpdate` is a `Load`/`CompareAndSwap` loop around the pure function `ewmaNext(old, sampleNs, beta)` (`old*beta + float64(sampleNs)*(1-beta)`), so the update worked example is tested without concurrency.
- `ForwardTicket` is `{ServerID uint32; Start time.Time}`. `time.Now()` carries Go's monotonic reading, and `time.Since(t.Start)` uses it, so the sample is never negative. The sample in ns is `uint64(time.Since(...).Nanoseconds())`.
- `round_robin` is `rr.Add(1) - 1` mod B. The `power_of_two` and `ewma` draws happen in `randomPair` under `rngMu`; mapping the draws to a backend is the pure function `twoChoice(i, j, less)`, where `less(b, a)` is the policy's comparison (`outstanding` or EwmaScore), so the worked examples are tested without randomness.
- Go 1.21 built-in `min`/`max` order the pair.
- Tests: `balancer_test.go` checks the worked example of every policy and of `ewmaNext`, that `B = 1` picks 0 without drawing, that `pick`/`finish` keep the counters, and that only `ok` moves the EwmaLatency.

### Traceability

Generated from the `// Design:` tags in the code by `implementation/spec_tools/tracegen.py` (see `AGENTS.md`). Each link points at the first code line after the tag.
Regenerate from the repository root with `python3 implementation/spec_tools/tracegen.py --lang Go --ext .go --comment // --write`.

<!-- trace:begin -->
| Design subsection | Implementation (file:line) |
|---|---|
| [LoadBalancer](#loadbalancer) | [balancer.go:73](../../../implementation/go/internal/ingress/balancer.go#L73), [balancer.go:86](../../../implementation/go/internal/ingress/balancer.go#L86), [balancer.go:105](../../../implementation/go/internal/ingress/balancer.go#L105), [balancer.go:115](../../../implementation/go/internal/ingress/balancer.go#L115), [balancer.go:176](../../../implementation/go/internal/ingress/balancer.go#L176), [balancer_test.go:79](../../../implementation/go/internal/ingress/balancer_test.go#L79) |
| [ForwardTicket](#forwardticket) | [balancer.go:51](../../../implementation/go/internal/ingress/balancer.go#L51), [balancer.go:105](../../../implementation/go/internal/ingress/balancer.go#L105), [balancer.go:122](../../../implementation/go/internal/ingress/balancer.go#L122), [balancer_test.go:105](../../../implementation/go/internal/ingress/balancer_test.go#L105), [frontend.go:75](../../../implementation/go/internal/ingress/frontend.go#L75) |
| [EwmaLatency](#ewmalatency) | [balancer.go:40](../../../implementation/go/internal/ingress/balancer.go#L40), [balancer.go:137](../../../implementation/go/internal/ingress/balancer.go#L137), [balancer.go:151](../../../implementation/go/internal/ingress/balancer.go#L151), [balancer_test.go:105](../../../implementation/go/internal/ingress/balancer_test.go#L105), [balancer_test.go:127](../../../implementation/go/internal/ingress/balancer_test.go#L127) |
| [EwmaScore](#ewmascore) | [balancer.go:158](../../../implementation/go/internal/ingress/balancer.go#L158), [balancer_test.go:156](../../../implementation/go/internal/ingress/balancer_test.go#L156) |
| [LbPolicy](#lbpolicy) | [balancer.go:189](../../../implementation/go/internal/ingress/balancer.go#L189), [balancer.go:218](../../../implementation/go/internal/ingress/balancer.go#L218), [balancer.go:230](../../../implementation/go/internal/ingress/balancer.go#L230), [balancer_test.go:32](../../../implementation/go/internal/ingress/balancer_test.go#L32), [balancer_test.go:43](../../../implementation/go/internal/ingress/balancer_test.go#L43), [balancer_test.go:59](../../../implementation/go/internal/ingress/balancer_test.go#L59), [balancer_test.go:156](../../../implementation/go/internal/ingress/balancer_test.go#L156), [config.go:26](../../../implementation/go/internal/ingress/config.go#L26) |
| [Worked examples](#worked-examples) | [balancer_test.go:32](../../../implementation/go/internal/ingress/balancer_test.go#L32), [balancer_test.go:43](../../../implementation/go/internal/ingress/balancer_test.go#L43), [balancer_test.go:59](../../../implementation/go/internal/ingress/balancer_test.go#L59), [balancer_test.go:127](../../../implementation/go/internal/ingress/balancer_test.go#L127), [balancer_test.go:156](../../../implementation/go/internal/ingress/balancer_test.go#L156) |
<!-- trace:end -->
