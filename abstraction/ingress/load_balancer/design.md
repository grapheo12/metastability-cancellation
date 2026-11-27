# Load Balancer — Design

`B` is the number of backends. All counters are atomic u64, incremented and decremented with relaxed ordering.
Each backend's `ewma_latency_ns` is an atomic u64 holding the bits of an IEEE-754 binary64 value
([EwmaLatency](#ewmalatency)), and so are its `ewma_exec_ns` ([EwmaExecTime](#ewmaexectime)) and its
`ewma_anti_exec_ns` ([EwmaAntiServiceTime](#ewmaantiservicetime)). `stats_snapshot` runs after every forward has ended or been abandoned, so its loads
are final. `monotonic_now_ns()` is the Ingress's monotonic clock ([../design.md#time-base](../design.md#time-base)).

## LoadBalancer

```
lb = { policy, filter: lb_filter, beta: ewma_beta, filter_margin: lb_filter_margin, monitor: HeartbeatMonitor or none,   # BackendLiveness
       filter_dropped: atomic u64 = 0,                                     # DeadlineFilter drops
       stats: array[B] of BackendStats (counters 0, ewma_latency_ns = EWMA_INITIAL_LATENCY_NS,
                                        ewma_exec_ns = bits of EXEC_NO_SAMPLE,
                                        ewma_anti_exec_ns = bits of EXEC_NO_SAMPLE,
                                        worker_count = 0),                 # WorkerCount; 0: not known yet
       rr: atomic u64 = 0,
       seq: atomic u64 = 0,                                                # TicketSeq source
       rng: rng_new(derive_seed(seed, STREAM_INGRESS_LB, 0)), rng_lock: mutex,
       log: list of ForwardRecord (empty), log_lock: mutex,                     # ForwardLog
       picks: list of PickRecord (empty),                                       # PickLog, also under log_lock
       peak: array[B] (PeakEwmaRtt), pending: array[B] (PendingStarts),         # lb_policy = peak_ewma only
       last_sent: array[B] of u64 = 0, probe_tau_ms: lb_probe_tau_ms,          # FilterProbe
       bucket: array[B] of TokenBucket,                                         # TokenBucketFilter (lb_filter = token_bucket)
       last_fold_ns: u64 per Ingress worker = 0 }

function pick(lb, remaining_ns, attempt, record = true, exclude = none, avoid = {}):   # remaining_ns: i64 or none (the request's time to its deadline);
                                                     # attempt: LbRetries before this one (0 for the first);
                                                     # record: false for a FilterWaitQueue re-check
                                                     # exclude: a backend this pick must not choose (RetryElsewhere), or none
                                                     # avoid: backends to leave out unless that leaves none (HedgedForwarding)
    if record: record_pick(lb)                       # PickLog: what the choice below is about to see
    s = choose(lb, remaining_ns, exclude, avoid)     # LbPolicy over the DeadlineFilter's set
    if s is Dropped:
        return Dropped                               # no counter changes; no finish follows (callers count)
    n = lb.stats[s].outstanding.fetch_add(1)         # the value before this pick's increment
    lb.stats[s].forwarded.fetch_add(1)
    q = lb.seq.fetch_add(1)                          # TicketSeq: 0, 1, 2, ... in pick order
    return ForwardTicket(server_id = s, start_ns = monotonic_now_ns(), attempt = attempt,
                         seq = q, queue_at_send = max(n, 0),
                         estimate_ns = estimated_finish(lb, s, max(n, 0)))      # EstimatedFinish

function finish(lb, ticket, outcome, exec_ns, worker_count):   # exec_ns, worker_count: from the answer, or none
                                                                # -> sampled: boolean
    s = ticket.server_id
    if outcome in {ok, rejected} and worker_count is not none:
        lb.stats[s].worker_count.store(worker_count)  # the backend's latest WorkerCount
    lb.stats[s].outstanding.fetch_sub(1)
    lb.stats[s][outcome].fetch_add(1)                # ok | rejected | bad_request | bad_gateway | reset
    end_ns = monotonic_now_ns()
    latency_ns = end_ns - ticket.start_ns
    if outcome == ok:
        e = ewma_update(lb, s, latency_ns)           # EwmaLatency after this sample
    else:
        e = binary64_from_bits(lb.stats[s].ewma_latency_ns.load())
    sampled = outcome in {ok, rejected} and exec_ns is not none
    if sampled:
        x = ewma_exec_update(lb, s, exec_ns)         # EwmaExecTime after this sample
        for b in 0 ..= B-1 except s:
            ewma_anti_update(lb, b, exec_ns)         # EwmaAntiServiceTime of every other backend
    else:
        x = exec_value(lb, s)                        # unchanged; none before the first sample
    with lb.log_lock:
        lb.log.append(ForwardRecord(end_ns, s, outcome, latency_ns, e,
                                    exec_ns if outcome in {ok, rejected} else none, x, ticket.attempt,
                                    ticket.estimate_ns))                   # ForwardLog
    return sampled                                   # true: both EWMAs above took this sample
```

`outstanding` is incremented in `pick`, not when the forward actually starts, so the next `pick` already sees the
load it is about to add. That narrows, but does not close, the race described below.

## ForwardTicket

- `start_ns` is read in `pick` after the counters are incremented, so the latency covers the upstream connection,
  the backend's work and the response, and excludes the choice itself.
- `attempt` is passed through untouched: `pick` stores it, the ForwardRecord copies it. Each LbRetry has its own
  ticket, so every attempt is picked, counted in `outstanding` and finished on its own.
- `finish` reads the clock once, after the counters. The latency is `now - start_ns` as unsigned 64-bit; the clock
  is monotonic, so it is never negative.
- The ticket is a plain value: the LoadBalancer keeps no per-forward state, and a lost ticket only loses a sample.
- **TicketSeq.** `seq` is unique per Ingress run and increases in pick order (u64, starting at 0; it cannot wrap in a
  run). It names one attempt, even when several ForwardTasks share a RequestId (client RequestCopies).
- **QueueAtSend.** `queue_at_send` is the chosen backend's `outstanding` just before this pick's increment, clamped
  at 0: the `n` of the DeadlineFilter's `(n + 1) × e`, read atomically with the increment, so two concurrent picks of
  the same backend get different values. The LoadBalancer itself does not use it; the `anxious` RetryPolicy does
  ([../retry_policy/design.md#retrypolicyregistry](../retry_policy/design.md#retrypolicyregistry)).

## EstimatedFinish

At each pick the LoadBalancer records, on the ForwardTicket, the time it expects the forward to take: the
**EstimatedFinish** `(queue_at_send + 1) × EwmaExecTime / k` of the chosen backend, where `k` is its WorkerCount
(read as 1 while unknown, i.e. 0): the `deadline_mean` DeadlineFilter's `(n + 1) × e`, divided by the number of
requests the backend serves in parallel (as `deadline_mean_per_worker`).

```
function estimated_finish(lb, s, q):                 # binary64 ns, or none
    e = exec_value(lb, s)                            # EwmaExecTime as read now; none before the first sample
    if e is none: return none
    k = lb.stats[s].worker_count.load(); if k == 0: k = 1    # WorkerCount; unknown before the first answer
    return (float(q + 1) * e) / float(k)             # binary64: the product rounded, then the quotient rounded
```

- **What it is compared with.** The ForwardRecord carries it to the ForwardLog (`estimate_ns` in `forwards.csv`),
  next to the forward's `latency_ns` (pick to answer). The **EstimateError** of a forward is
  `(latency_ns − estimate_ns) / latency_ns`: `0` is exact, positive means the forward took longer than estimated,
  negative shorter. The ResultPlotter draws its CDF
  ([../../benchmark_runner/result_plotter/design.md](../../benchmark_runner/result_plotter/design.md), EstimateErrorCdf).
- **Not used to choose.** Recording it changes no pick: `choose` already ran.
- **Races.** `exec_value` is read after the pick's increment; a concurrent `finish` may update EwmaExecTime in between,
  as for every LoadBalancer heuristic.

Worked example: backend with EwmaExecTime 400 ms (`400_000_000` ns).

| `queue_at_send` | WorkerCount `k` | `estimate_ns` | Check |
|---|---|---|---|
| 0 | 1 | 400_000_000 | `1 × 400 ms / 1` |
| 3 | 1 | 1_600_000_000 | `4 × 400 ms / 1` |
| 3 | 4 | 400_000_000 | `4 × 400 ms / 4` |
| 15 | 40 | 160_000_000 | `16 × 400 ms / 40` |
| 3 | 0 (unknown) | 1_600_000_000 | `k` read as 1 |
| any | any, no EwmaExecTime yet | none | empty in `forwards.csv` |

- **Why divide by `k`.** A backend with `k` workers serves `k` requests at once, so `q` requests ahead take about
  `q / k` service times. Without the division the estimate was about 19× the real latency on a 40-worker backend at
  40% load (median EstimateError −18), see the EstimateErrorCdf. It is still an approximation: with
  `q + 1 <= k` the request starts at once and takes about one `e`, which `(q + 1) × e / k` underestimates.

## EwmaLatency

```
function ewma_update(lb, s, sample_ns):              # sample_ns: u64
    x = float(sample_ns)                             # binary64, round to nearest
    loop:
        old_bits = lb.stats[s].ewma_latency_ns.load()
        old = binary64_from_bits(old_bits)
        new = old * lb.beta + x * (1 - lb.beta)      # binary64, evaluated left to right, round to nearest
        if lb.stats[s].ewma_latency_ns.compare_and_swap(old_bits, bits_of(new)): return new
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
- **Returned value.** `ewma_update` returns the value it stored, which is the EwmaLatency right after this sample
  even if another sample lands a moment later. `finish` puts it in the ForwardRecord.

## EwmaExecTime

Per backend, the **EwmaExecTime** is an exponentially weighted moving average of the ExecutionTimes the backend
reported in its ExecReports ([../../protocol/design.md#execreport](../../protocol/design.md#execreport)). It uses
the same `beta` as the EwmaLatency. No LbPolicy reads it yet.

```
EXEC_NO_SAMPLE = the binary64 quiet NaN with bits 0x7FF8000000000000    # "no sample yet"

function ewma_exec_update(lb, s, sample_ns):         # sample_ns: u64
    x = float(sample_ns)                             # binary64, round to nearest
    loop:
        old_bits = lb.stats[s].ewma_exec_ns.load()
        if old_bits == bits_of(EXEC_NO_SAMPLE):
            new = x                                  # the first sample is the average
        else:
            old = binary64_from_bits(old_bits)
            new = old * lb.beta + x * (1 - lb.beta)  # binary64, evaluated left to right, round to nearest
        if lb.stats[s].ewma_exec_ns.compare_and_swap(old_bits, bits_of(new)): return new

function exec_value(lb, s):                          # binary64 or none
    bits = lb.stats[s].ewma_exec_ns.load()
    return none if bits == bits_of(EXEC_NO_SAMPLE) else binary64_from_bits(bits)
```

- **Which samples.** Forwards that end `ok` or `rejected` and whose relayed body carried a valid `exec_ns`. A `503`
  is a sample here, unlike for the EwmaLatency: its ExecutionTime is the work the backend really did before the
  cancel. `bad_request`, `bad_gateway` and `reset` have no ExecReport.
- **Start value.** None, not a guess: the first sample becomes the average. A stored binary64 is never NaN (samples
  are finite and `beta` is in `[0, 1]`), so the sentinel cannot collide with a value.
- **Concurrency** is as for the EwmaLatency: a lock-free compare-and-swap loop that applies each sample once.

## EwmaAntiServiceTime

Per backend `b`, the **EwmaAntiServiceTime** is an exponentially weighted moving average of the ExecutionTimes
reported by every backend **other than** `b`: the service time a request would expect if it were not served by `b`.
It takes exactly the samples the EwmaExecTime takes (same rule, same `beta`), each folded into every backend except
the one that served it.

```
function ewma_anti_update(lb, b, sample_ns):         # sample_ns: u64; b != the serving backend
    x = float(sample_ns)                             # binary64, round to nearest
    loop:
        old_bits = lb.stats[b].ewma_anti_exec_ns.load()
        if old_bits == bits_of(EXEC_NO_SAMPLE):
            new = x                                  # the first sample is the average
        else:
            old = binary64_from_bits(old_bits)
            new = old * lb.beta + x * (1 - lb.beta)  # binary64, evaluated left to right, round to nearest
        if lb.stats[b].ewma_anti_exec_ns.compare_and_swap(old_bits, bits_of(new)): return new

function anti_value(lb, b):                          # binary64 or none
    bits = lb.stats[b].ewma_anti_exec_ns.load()
    return none if bits == bits_of(EXEC_NO_SAMPLE) else binary64_from_bits(bits)
```

- **Order.** `finish` updates the serving backend's EwmaExecTime first, then the other backends' EwmaAntiServiceTime
  in increasing `b`. Each is its own compare-and-swap, so a reader may see one updated and not yet the other.
- **One backend.** With `B = 1` no backend is ever "other", so its EwmaAntiServiceTime stays none.
- **Who reads it.** No LbPolicy and no DeadlineStrategy. The `anxious` RetryPolicy does, through the BackendView
  ([../retry_policy/design.md#retrypolicyregistry](../retry_policy/design.md#retrypolicyregistry)), and the summary reports its final value.

Worked example, `beta = 0.5`, `B = 3`, all values none at first (ns):

| Finish | Sample | `anti[0]` | `anti[1]` | `anti[2]` | Check |
|---|---|---|---|---|---|
| backend 0 ok | 100 | none | 100 | 100 | first sample of 1 and 2 |
| backend 1 rejected | 300 | 300 | 100 | 200 | `anti[2] = 100·0.5 + 300·0.5 = 200`; `anti[0]` first sample |
| backend 2 reset | none | 300 | 100 | 200 | no sample: unchanged |
| backend 2 ok | 500 | 400 | 300 | 200 | `300·0.5 + 500·0.5 = 400`; `100·0.5 + 500·0.5 = 300`; `anti[2]` unchanged (its own sample) |

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

## PeakEwmaRtt

The `peak_ewma` LbPolicy (an OptionalValue) does not use EwmaLatency. It averages each backend's **PeakEwmaRtt**: the
time from sending a forward's first request byte to receiving its first response byte, for every answer whatever its
status, averaged with a weight that depends on the time between samples, not on their count.

```
PEAK_EWMA_DECAY_MS       = 10_000      # tau: a sample this old has weight 1 - 1/e
PEAK_EWMA_AGGREGATION_MS = 100         # new samples are folded in at most this often
PEAK_EWMA_MAX_SAMPLES    = 1_000       # samples kept per backend between two folds

peak[s] = { rtt_ms: binary64 = 0,      # the average; 0: no sample folded yet
            last_ns: u64 = 0,          # monotonic time of the last folded sample
            samples: ring of PEAK_EWMA_MAX_SAMPLES (rtt_ms, t_ns), written = 0, folded = 0 }   # shared by all workers

procedure peak_ewma_sample(lb, s, first_byte_sent_ns, first_byte_received_ns):   # Frontend, at the response headers
    rtt_ms = floor((first_byte_received_ns - first_byte_sent_ns) / 1_000_000)     # whole ms, truncated
    k = peak[s].written.fetch_add(1)
    peak[s].samples[k mod PEAK_EWMA_MAX_SAMPLES] = (float(rtt_ms), monotonic_now_ns())

procedure maybe_fold(lb):              # at each pick of a worker; lb.last_fold_ns is per Ingress worker
    now = monotonic_now_ns()
    if now - lb.last_fold_ns < PEAK_EWMA_AGGREGATION_MS * 1_000_000: return
    lb.last_fold_ns = now
    for s in 0 ..= B-1: fold(lb, s)

procedure fold(lb, s):
    w = peak[s].written; f = peak[s].folded
    if w - f > PEAK_EWMA_MAX_SAMPLES: f = w - PEAK_EWMA_MAX_SAMPLES   # overwritten samples are lost
    e = peak[s].rtt_ms; t_prev = peak[s].last_ns
    for k in f ..= w-1, in order:
        (x, t) = peak[s].samples[k mod PEAK_EWMA_MAX_SAMPLES]
        if x <= 0 or t == 0: continue                 # an RTT under 1 ms (truncated to 0) is not a sample
        a = 1 if probe_answer(lb, s, t) else time_alpha(t, t_prev)   # ProbeSample: a probe's answer replaces the average
        e = x if e == 0 else a * x + (1 - a) * e      # each product rounded, then the sum (binary64)
        t_prev = t
    peak[s].rtt_ms = e; peak[s].last_ns = t_prev; peak[s].folded = w

function time_alpha(t_ns, t_prev_ns):                 # binary64 in [0, 1]
    dt = signed(t_ns - t_prev_ns)                     # i64
    if dt <= 0: return 0                              # a concurrent or out-of-order sample has no weight
    return min(1, max(0, 1 - exp(-(dt / 1e9) / (PEAK_EWMA_DECAY_MS / 1e3))))
```

- **What is measured.** First byte to first byte, so a backend's queueing and service count, but not the time to send
  or receive the bodies. Every answer is a sample, `202`, `503`, `400` alike; a forward that ends without an answer is
  not.
- **Shared.** The samples and the average belong to the backend and are shared by all Ingress workers; each worker
  folds on its own picks, at most every `PEAK_EWMA_AGGREGATION_MS`. Two workers may fold at the same moment; the race
  is accepted (a sample is then folded once more or lost).
- **First sample.** The first folded sample becomes the average; until then `rtt_ms = 0` means "no sample".
- **Stale average.** A backend that answers nothing gets no sample, so its average stays where it was, however long it
  has been silent. [ProvisionalCurrentRtt](#provisionalcurrentrtt) is the testbed's answer to that.

Worked example, `tau = 10 s`:

| Δt since the last sample | `time_alpha` | Average before | Sample | Average after | Check |
|---|---|---|---|---|---|
| 100 ms | 0.009950166250831893 | 400 | 420 | 400.19900332501663 | 0.00995… × 420 + 0.99004… × 400 |
| 10 s | 0.6321205588285577 | — | — | — | 1 − e^(−1) |
| 60 s | 0.9975212478233336 | — | — | — | 1 − e^(−6) |
| 0 or negative | 0 | 400 | 9000 | 400 | no weight |
| — | — | 0 (no sample) | 420 | 420 | the first sample is the average |

## PeakEwmaCost

```
PEAK_EWMA_DEFAULT_RTT_MS = 10.0
PEAK_EWMA_PENALTY        = 1_000_000.0

function peak_ewma_cost(lb, s):                       # binary64
    rtt = peak[s].rtt_ms
    a = float(lb.stats[s].outstanding.load())         # forwards in progress on s, every attempt of every request
    if rtt > 0: return rtt * (a + 1)                  # PeakEwmaCost: latency times load
    if a > 0:   return PEAK_EWMA_PENALTY + a          # in use but never answered: worst
    return PEAK_EWMA_DEFAULT_RTT_MS * (a + 1)         # unused and unmeasured: assume 10 ms
```

| `rtt_ms` | `outstanding` | **PeakEwmaCost** | Check |
|---|---|---|---|
| 400 | 3 | 1600 | 400 × 4 |
| 400 | 0 | 400 | 400 × 1 |
| 0 | 2 | 1000002 | penalty + 2 |
| 0 | 0 | 10 | 10 × 1 |

## LbPolicy

```
function choose(lb, remaining_ns, exclude = none, avoid = {}):
    L = [ s for s in 0 ..= B-1 if heartbeat_monitor.is_live(lb.monitor, s) ]     # LiveSet, in id order
    if L is empty: L = [0 ..= B-1]                   # every backend silent: use them all, as without a monitor
    if exclude is not none:
        L = [ s for s in L if s != exclude ]         # ExcludedBackend: after the fallback, so it never comes back
        if L is empty: return Dropped
    A = [ s for s in L if s not in avoid ]           # AvoidedBackends (HedgedForwarding): left out ...
    if A is not empty: L = A                         # ... unless that leaves none, then all of L again
    if lb.policy == peak_ewma: maybe_fold(lb)        # PeakEwmaRtt: fold new RTT samples at most every 100 ms
    if lb.filter == deadline_provisional_rtt:
        return peak_ewma_deadline_choose(lb, L, remaining_ns)   # PeakEwmaDeadline: its own filter and choice
    if lb.filter == token_bucket:
        return token_bucket_choose(lb, L, remaining_ns)         # TokenBucketFilter: tokens instead of a deadline test
    if lb.filter != none and remaining_ns is not none:
        L = deadline_filter(lb, L, remaining_ns)     # DeadlineFilter
        if L is empty: return Dropped
    n = len(L)
    if n == 1: return L[0]                           # every policy; no random draw, no counter change
    match lb.policy:
        round_robin:
            return L[lb.rr.fetch_add(1) mod n]       # u64 counter: cannot wrap in any realistic run
        least_outstanding:
            best = L[0]
            for s in L[1 ..= n-1]:
                if outstanding[s].load() < outstanding[best].load(): best = s      # strict <: ties go to lower id
            return best
        power_of_two:
            (a, b) = random_pair(lb, L)
            return b if outstanding[b].load() < outstanding[a].load() else a       # ties go to lower id
        ewma:
            (a, b) = random_pair(lb, L)
            return b if ewma_score(lb, b) < ewma_score(lb, a) else a               # EwmaScore; ties go to lower id
        peak_ewma:                                   # OptionalValue
            r = native_random_u64()                  # the implementation's own random source, one draw per pick
            i = r mod n
            j = (r >> 16) mod n
            if j == i: j = (j + 1) mod n             # two different positions of L
            ci = peak_ewma_cost(lb, L[i]); cj = peak_ewma_cost(lb, L[j])     # PeakEwmaCost
            if ci == cj: return L[i] if (r & 1) == 1 else L[j]               # tie: the draw's low bit decides
            return L[i] if ci < cj else L[j]

function random_pair(lb, L):                         # two different members of L (n >= 2), a < b
    n = len(L)
    with lb.rng_lock:
        i = uniform_int(lb.rng, 0, n-1)
        j = uniform_int(lb.rng, 0, n-2)
    if j >= i: j = j + 1                             # j is uniform over the n-1 positions other than i
    return (L[min(i, j)], L[max(i, j)])              # L is in id order, so a < b
```

- **LiveSet.** `L` holds the backends the Heartbeat Monitor reports live
  ([../heartbeat_monitor/design.md#backendliveness](../heartbeat_monitor/design.md#backendliveness)). Without a
  monitor every backend is live, so `L = [0 ..= B-1]` and every policy is exactly as before: same choices, same
  draws, same counter. A dead backend gets no new forwards; forwards already in progress on it are left alone and
  end as they would have. When no backend is live, `L` falls back to all of them, so requests still go somewhere.
  `L` is read once per pick; a backend that changes state during the pick is seen in either state.
- **AvoidedBackends.** Under HedgedForwarding an LbRetry decided `RetryElsewhere` passes `avoid` = every backend its
  request has already been sent to ([../frontend/design.md#hedgedforwarding](../frontend/design.md#hedgedforwarding)).
  They are left out of `L` unless that would leave nothing, in which case `L` is kept whole: unlike `exclude`, `avoid`
  never makes a pick `Dropped`. With `avoid = {}` nothing changes.
- **ExcludedBackend.** A pick with `exclude = e` (an LbRetry decided `RetryElsewhere`,
  [../retry_policy/api.md#retrydecision](../retry_policy/api.md#retrydecision)) removes `e` from `L` after the
  all-silent fallback, so the retry never goes back to `e`, even when `e` is the only live backend. If that leaves
  `L` empty, the pick is `Dropped` with no counter change, like a DeadlineFilter refusal. With `exclude = none`
  nothing changes.
- **Concurrent picks race.** Two picks at the same moment may read the same counters and choose the same backend.
  That is accepted: the policies are heuristics, and the counters correct themselves on the next pick. No lock is
  taken around the read and the increment.
- **Reproducibility.** `round_robin` and `least_outstanding` use no randomness. `peak_ewma` draws from the
  implementation's own random source, not from `seed`, so its pairs differ between runs. `power_of_two` and `ewma` draw two
  numbers per pick (when `B > 1`), in pick order; with concurrent clients the pick order, and so the
  draw-to-request assignment, depends on timing. `ewma` also depends on measured latencies, so its choices are not
  reproducible across runs even with one client.
- `least_outstanding` reads each counter once per comparison; the counters may change during the scan. The
  result is the minimum among the values seen.

## DeadlineFilter

With `lb_filter` other than `none`, each request is only sent to a backend expected to finish it before its
RequestDeadline. The Frontend passes the request's time left, `remaining_ns`
([../frontend/design.md#forwardtask](../frontend/design.md#forwardtask)); `choose` keeps from the LiveSet the
backends that pass the **DeadlineStrategy** named by `lb_filter`, and the LbPolicy then chooses among them exactly as
it would among `L`. Everything but the per-backend test is shared by the strategies.

| `lb_filter` (DeadlineStrategy) | Keep backend `s` if | Character |
|---|---|---|
| `deadline_mean` | `(n + 1) × e <= T` | mean wait as if the backend ran one request at a time: about `k` times too pessimistic |
| `deadline_mean_per_worker` | `(n + 1) × e / k <= T` | mean wait with `k` workers draining the queue |
| `deadline_mean_queue_plus_share` | `(n + 1/k) × e <= T` | each request ahead at a full service time, the request's own at `1/k` of one |
| `deadline_mmk` | `Pr(t > T \| N = n) <= DEADLINE_FILTER_MISS` | M/M/k on-time probability `>= 0.99` (see [Model](#model)) |
| `deadline_provisional_rtt` (OptionalValue; needs `lb_policy = peak_ewma`) | `γ ×` ProvisionalCurrentRtt `<= T` (`γ = lb_filter_margin`) | the backend's RTT average, as if its oldest pending forward answered now; its own choice and wait queues ([PeakEwmaDeadline](#peakewmadeadline)) |
| `token_bucket` (OptionalValue; needs `lb_policy = peak_ewma`) | its TokenBucket holds a whole token | not a deadline test: a per-backend rate limit adapted by AIMD; the PeakEwmaDeadline's choice and wait queues ([TokenBucketFilter](#tokenbucketfilter)) |

`n`, `k`, `e` and `T` are as defined in the [Model](#model): `outstanding`, `max(WorkerCount, 1)`, EwmaExecTime and
`remaining_ns`.

### Model

Each backend is an M/M/k queue seen by the LoadBalancer, with:

| Symbol | Value | Source |
|---|---|---|
| `n` | `outstanding` of the backend at pick time: this Ingress's forwards in progress there (queued or running) | BackendStats |
| `k` | `max(worker_count, 1)`: requests the backend runs at once | WorkerCount ([../../protocol/design.md#workercount](../../protocol/design.md#workercount)) |
| `e` | EwmaExecTime, in ns: the mean service time, so `μ = 1/e` | [EwmaExecTime](#ewmaexectime) |
| `T` | `remaining_ns`: the request's time left | Frontend |

The quantity is the **conditional** probability `Pr(t <= T | N = n)` that the request's completion time `t` (its wait
plus its own service) is within `T`, given the `n` requests seen ahead of it. It is not the stationary M/M/k
response-time distribution, which averages over `N` and needs the arrival rate.

- **Given `N = n`.** Service is exponential, hence memoryless: every request in service has an `Exp(μ)` remainder,
  however long it has already run, so nothing else about the past matters. If `n < k` a worker is free and the wait
  `W` is 0. Otherwise all `k` workers are busy and the request starts after `m = n - k + 1` departures; while all
  are busy, departures form a Poisson process of rate `kμ`, so `W ~ Erlang(m, kμ)`. Later arrivals do not delay it
  (FIFO). Its own service `S ~ Exp(μ)` is independent, and `t = W + S`.
- **Late probability.** With `x = μT = T / e` and `Q(m, y) = Σ_{i=0}^{m-1} e^{-y} y^i / i!` (the probability that
  a Poisson(y) variable is `< m`, which equals `Pr(Erlang(m, rate) > y / rate)`):

| Case | `Pr(t > T \| N = n)` |
|---|---|
| `T <= 0` | 1 (already late) |
| `n < k` | `e^{-x}` (only the service) |
| `n >= k`, `k = 1` | `Q(m + 1, x)`: `W + S` is `Erlang(m + 1, μ)` |
| `n >= k`, `k >= 2` | `Q(m, kx) + e^{-x} (k / (k-1))^m (1 - Q(m, (k-1)x))` |

  The last row: `Pr(W + S > T) = Pr(W > T) + ∫_0^T f_W(w) e^{-μ(T-w)} dw`, with
  `f_W(w) = (kμ)^m w^{m-1} e^{-kμw} / (m-1)!`. The integral is
  `e^{-μT} (kμ)^m / (m-1)! ∫_0^T w^{m-1} e^{-(k-1)μw} dw = e^{-μT} (k/(k-1))^m Pr(Erlang(m, (k-1)μ) <= T)`.
  This derivation is argued here, not machine-checked; the worked example below was checked against direct numerical
  integration (agreeing to 50 digits) and a Monte Carlo of 10^6 samples per case.
- **Estimates, not truths.** `μ = 1/e` plugs the EwmaExecTime in as the true mean service time; its own uncertainty is
  not modeled, so 0.99 is a probability under the fitted model. `n` counts this Ingress's forwards, which include
  requests still in transit and superseded RequestCopies the server is still running, so it is the server's `N` only
  approximately. The order is FIFO; under the `edf` QueueDiscipline a request may overtake or be overtaken, and the
  law is an approximation.

### Filter

```
DEADLINE_FILTER_CONFIDENCE = 0.99
DEADLINE_FILTER_MISS       = 0.01            # 1 - DEADLINE_FILTER_CONFIDENCE, written as its own constant

function deadline_filter(lb, L, remaining_ns):      # L in id order -> sublist of L, in id order
    F = []
    for s in L:
        e = exec_value(lb, s)                        # EwmaExecTime, binary64 or none
        if e is none: F.append(s); continue          # no ExecutionTime sample yet: eligible
        n = lb.stats[s].outstanding.load()
        k = max(lb.stats[s].worker_count.load(), 1)  # WorkerCount; not known yet: 1
        T = float(remaining_ns)                      # i64 -> binary64, round to nearest
        match lb.filter:                             # DeadlineStrategy
            deadline_mean:            keep = float(n + 1) * e <= T
            deadline_mean_per_worker: keep = float(n + 1) * e / float(k) <= T    # left to right, each step rounded
            deadline_mean_queue_plus_share: keep = float(n) * e + e / float(k) <= T   # each product/quotient rounded, then the sum
            deadline_mmk:             keep = late_probability(n, k, e, T) <= DEADLINE_FILTER_MISS
        if keep: F.append(s)
    return F

function late_probability(n, k, e, T):               # binary64 throughout; -> Pr(t > T | N = n) in [0, 1]
    if T <= 0: return 1
    x = T / e
    if n < k: return exp(-x)
    m = n - k + 1
    if k == 1: return poisson_lower_sum(m + 1, x)
    a = poisson_lower_sum(m, k * x)
    log_b = -x + m * log1p(1 / (k - 1)) + log_poisson_upper(m, (k - 1) * x)   # ln of e^{-x} (k/(k-1))^m (1 - Q)
    return min(a + exp(log_b), 1)

function poisson_lower_sum(m, y):                    # Q(m, y), m >= 1, y > 0
    s = 0
    for i in 0 ..= m-1:                              # in increasing i
        s = s + exp(-y + i * ln(y) - lgamma(i + 1))  # each Poisson term computed in log space
    return min(s, 1)

function log_poisson_upper(m, y):                    # ln(1 - Q(m, y)), m >= 1, y > 0
    if y < m:                                        # lower regularized gamma series: no cancellation
        t = 1; s = 1; j = 1
        loop:
            t = t * y / (m + j); s = s + t; j = j + 1
            if t < 1e-17 * s: break
        return -y + m * ln(y) - lgamma(m + 1) + ln(s)
    q = poisson_lower_sum(m, y)
    return log1p(-q) if q < 1 else -infinity        # then exp(log_b) = 0
```

- **Numerics.** Every Poisson term and the `(k/(k-1))^m` factor are formed in log space, so no intermediate overflows
  or underflows even for `n` in the thousands. `1 - Q(m, y)` is never formed by subtraction when it is small (`y < m`);
  computing it as `1 - Q` there loses everything (e.g. `n = 1000`, `k = 2`, `e = 10 ms`, `T = 5 s` needs
  `1 - Q(999, 500) ≈ e^{-196}`, multiplied by `2^999`). Each product inside a sum is rounded on its own (no fused
  multiply-add). Implementations must agree with the worked example to a relative `1e-12`.
- **No backend passes.** When no backend of `L` passes, `pick` returns `Dropped`; no backend is contacted and no
  BackendStats counter, ForwardRecord or `finish` follows. For a first attempt the request then **waits** in the
  [FilterWaitQueue](#filterwaitqueue), which re-checks it every 10 ms and drops it only once its RequestDeadline has
  passed. A request whose deadline has already passed (`remaining_ns <= 0`) passes nowhere.
- **Off.** With `lb_filter = none`, or a request without a RequestDeadline (`remaining_ns` none), `L` is used as
  before: same choices and same random draws.
- **Races.** `outstanding`, WorkerCount and EwmaExecTime are read without a lock and may change during the scan, as
  for EwmaScore; the filter is a heuristic.

Worked example, `e = 200_000_000` ns (`μ = 5/s`), `T = 5_000_000_000` ns (`x = 25`); `Pr(late)` to 17 significant
digits:

| `k` | `n` | `m` | `Pr(t > T \| N = n)` | Eligible (`<= 0.01`) |
|---|---|---|---|---|
| 10 | 0 | — | 1.3887943864964040e-11 (`e^{-25}`) | yes |
| 10 | 9 | — | 1.3887943864964040e-11 | yes |
| 10 | 10 | 1 | 1.5431048738848933e-11 | yes |
| 10 | 50 | 41 | 1.0439869390746449e-9 | yes |
| 10 | 150 | 141 | 3.9304667021466753e-5 | yes |
| 10 | 200 | 191 | 0.0075996341805018727 | yes |
| 10 | 202 | 193 | 0.0093662549934202290 | yes: the largest admitted `n` |
| 10 | 203 | 194 | 0.010395504157208201 | no |
| 10 | 220 | 211 | 0.057518866812073766 | no |
| 1 | 0 | — | 1.3887943864964040e-11 | yes |
| 1 | 10 | 10 | 0.00058646162975308126 | yes |
| 1 | 13 | 13 | 0.0064674843658217432 | yes: the largest admitted `n` |
| 1 | 14 | 14 | 0.012402060718900588 | no |

Other cases: `T <= 0` gives 1 (dropped); a backend without an EwmaExecTime sample is eligible; WorkerCount 0
(unknown) is treated as `k = 1`; `lb_filter = none` or no RequestDeadline uses `L`.

Worked example of the three mean strategies, `B = 2`, both live, `e = 140_000_000` ns for both,
`outstanding = (10, 40)`, WorkerCount `(10, 10)`; the waits are `(n + 1) × e = (1_540_000_000, 5_740_000_000)`,
divided by `k` `(154_000_000, 574_000_000)`, and `n × e + e / k = (1_414_000_000, 5_614_000_000)`, all exact in
binary64:

| `remaining_ns` | `deadline_mean` keeps | `deadline_mean_per_worker` keeps | `deadline_mean_queue_plus_share` keeps |
|---|---|---|---|
| 6_000_000_000 | [0, 1] | [0, 1] | [0, 1] |
| 3_000_000_000 | [0] | [0, 1] | [0] |
| 1_540_000_000 | [0] (`<=`: exactly fits) | [0, 1] | [0] |
| 1_414_000_000 | [] | [0, 1] | [0] (`<=`: exactly fits) |
| 1_000_000_000 | [] (`Dropped`) | [0, 1] | [] |
| 500_000_000 | [] | [0] | [] |
| 154_000_000 | [] | [0] (`<=`) | [] |
| 100_000_000 | [] | [] | [] |

**Admit edges compared**, at `e = 200 ms`, `T = 5 s`: `deadline_mean` keeps `n <= 24` whatever `k` is;
`deadline_mean_queue_plus_share` keeps `n <= 24` at `k = 10` (`n <= T/e - 1/k = 24.9`) and at `k = 1`;
`deadline_mean_per_worker` keeps `n <= 249` at `k = 10` (`n <= 24` at `k = 1`); `deadline_mmk` keeps `n <= 202` at
`k = 10` and `n <= 13` at `k = 1`, stricter than the per-worker mean because it bounds the tail, not the mean.

## FilterWaitQueue

A first attempt that no backend passes under the DeadlineFilter waits here instead of being dropped. Every
`FILTER_QUEUE_TICK_MS` the queue re-checks its requests with the same `pick` (LiveSet, DeadlineFilter, LbPolicy) and
sends those that now fit; a request still waiting once its RequestDeadline has passed is dropped.

```
FILTER_QUEUE_TICK_MS = 10

wq = { waiting: FIFO of WaitEntry, lock: mutex,
       queued: u64, sent_from_queue: u64, dropped: u64, max_len: u64 }      # summary counters
WaitEntry = { deadline: u64 (Unix ns), state: waiting | sent(ticket) | dropped | gone, signal: one-shot }

function admit(lb, deadline):                        # attempt 0; deadline: RequestDeadline or none
    -> Sent(ticket) | Wait(entry)
    if lb.filter == none or deadline is none:
        return Sent(pick(lb, none, 0))               # never filtered, so never Dropped
    with wq.lock:
        if wq.waiting is empty:
            t = pick(lb, deadline - wall_clock_unix_ns(), 0)
            if t is not Dropped: return Sent(t)
        e = WaitEntry(deadline, state = waiting)     # FIFO: behind every request already waiting
        wq.waiting.push_back(e); wq.queued += 1
        return Wait(e)

procedure scan(lb):                                  # every FILTER_QUEUE_TICK_MS while lb.filter != none
    with wq.lock:
        wq.max_len = max(wq.max_len, len(wq.waiting))
        for e in wq.waiting, oldest first:
            if e.state == gone: remove e; continue                    # its client reset the stream
            remaining = e.deadline - wall_clock_unix_ns()             # exact signed, saturated to i64
            if remaining <= 0:                                        # past its SLO
                e.state = dropped; wq.dropped += 1; remove e; e.signal.set(); continue
            t = pick(lb, remaining, 0, record = false)                # a failed re-check writes no PickRecord
            if t is not Dropped:
                e.state = sent(t); wq.sent_from_queue += 1; remove e; e.signal.set()
            # else: stays in place; younger entries are still tried
```

- **FIFO admission.** A newcomer joins behind a non-empty queue even if a backend would pass it now, so it never
  overtakes a waiting request at admission. Inside a scan every entry is tried, oldest first; a younger entry (more
  time left) may be sent while an older one still does not fit.
- **Load is seen at once.** A request sent from the queue increments its backend's `outstanding` in `pick` before the
  next entry is tried, so later entries see it.
- **Waiting counts.** The time spent waiting is part of the client's latency and shrinks `remaining_ns`; the LbRetry
  `timeout` counts from the attempt's `pick`, after the wait.
- **Counters (summary).** `filter_queued`: requests that waited; `filter_sent_from_queue`: those later sent;
  `filter_dropped`: those dropped past their deadline; `filter_queue_max`: the longest queue a scan saw. `pick` itself
  counts nothing.
- **Off.** With `lb_filter = none`, or a request without a RequestDeadline, nothing waits and no scanner runs.
- **Concurrency.** `wq.lock` is held for one admission or one whole scan. `admit` holding it for its `pick` keeps
  newcomers from overtaking a scan in progress.

```mermaid
stateDiagram-v2
  [*] --> Sent: admit, queue empty and pick passes
  [*] --> Waiting: admit, queue non-empty or nothing passes
  Waiting --> Waiting: scan, nothing passes yet
  Waiting --> Sent: scan, pick passes
  Waiting --> Dropped: scan, deadline passed (filter_dropped += 1)
  Waiting --> Gone: client reset (removed at the next scan)
```

Worked example, `deadline_mean`, one backend, `e = 200 ms`, `outstanding = 24` throughout except as stated, so
`(n + 1) × e = 5 s`; times in ms, every request has a 5 s SLO:

| Time | Event | Queue after | Result |
|---|---|---|---|
| 0 | A arrives, 5000 left: `5000 <= 5000` passes | [] | A sent |
| 1 | B arrives (n = 25), 4999 left: `5200 > 4999` | [B] | B waits |
| 2 | C arrives with 5000 left; queue non-empty | [B, C] | C waits (FIFO), not tried |
| 10 | scan: n = 25, B (4990 left) and C (4992 left) fail | [B, C] | — |
| 300 | two forwards finish, n = 23; scan: B `4800 <= 4701`? no; C `4800 <= 4702`? no | [B, C] | both wait |
| 600 | n = 21; scan: B `4400 <= 4401` yes, sent (n = 22); C `4600 <= 4402` no | [C] | B sent; C still waits |
| 5002 | C still not sent; remaining 0 | [] | C dropped past its SLO, `503` |

## ProvisionalCurrentRtt

A backend that stops answering keeps its PeakEwmaRtt forever ([PeakEwmaRtt](#peakewmartt)). The
**ProvisionalCurrentRtt** folds in, provisionally, the forward that has waited longest on the backend, as if it
answered now. It is not stored: it is computed at each decision and then forgotten.

```
pending[s] = multiset of u64 (monotonic ns)   # PendingStarts: one per forward picked for s whose client request
                                              # has not ended yet; shared by all workers, under its own lock

procedure pending_add(lb, s, request):        # at each pick that returns s, under deadline_provisional_rtt or token_bucket
    t = monotonic_now_ns(); pending[s].insert(t); request.pending.append((s, t))
    last_sent[s] = t                            # LastSentTime (FilterProbe)

procedure pending_release(lb, request):       # when the client request ends (answered, dropped or reset)
    for (s, t) in request.pending: pending[s].remove_one(t)

function provisional_rtt(lb, s, now_ns):      # -> (binary64 ms, has_pending)
    e = peak[s].rtt_ms
    if pending[s] is empty: return (e, false)
    x = float(now_ns - min(pending[s])) / 1e6         # the oldest pending forward's age, ms
    a = time_alpha(now_ns, peak[s].last_ns)           # the weight it would have as a sample now
    return (x if e == 0 else a * x + (1 - a) * e, true)
```

- **Until the request ends.** A forward stays pending until its whole client request ends, not only its own attempt:
  under HedgedForwarding the losing attempts are released when the winner answers.
- **Healthy backend.** Its oldest pending forward is about as old as a normal answer, and the time since its last sample
  is short, so `a` is small and the value stays near the average.
- **Stalled backend.** Both the age and `a` grow; after a few `tau` the value is about the oldest forward's age.

Worked example, `tau = 10 s`, PeakEwmaRtt 400 ms whose last sample was at 50 s, oldest pending forward started at
59.9 s (the backend stalled at 60 s):

| now | age (ms) | Δt (s) | `a` | ProvisionalCurrentRtt (ms) | Check |
|---|---|---|---|---|---|
| 70 s | 10100 | 20 | 0.8646647167633873 | 8787.247752604857 | 0.86466… × 10100 + 0.13533… × 400 |
| 80 s | 20100 | 30 | 0.950212931632136 | 19119.194753153082 | 0.95021… × 20100 + 0.04978… × 400 |
| any, nothing pending | — | — | — | 400 | the average itself |

**FilterMargin.** `lb_filter_margin = γ >= 1` makes the test stricter: a backend is kept only if `γ ×` its
ProvisionalCurrentRtt fits in the time left, leaving `1 − 1/γ` of it as a margin for the queue the RTT average has not
seen yet. With `γ = 2` the backend above is already refused at 70 s (`2 × 8787 = 17574 > 17000`); with `γ = 1` it is
kept (`8787 <= 17000`).

With 17 s left, the backend is kept at 70 s (8787 <= 17000) and refused at 80 s (19119 > 17000).

## PeakEwmaDeadline

`lb_filter = deadline_provisional_rtt` (with `lb_policy = peak_ewma`) replaces the shared DeadlineFilter and
FilterWaitQueue with its own **PeakEwmaDeadline**: a filter over every backend, the lowest PeakEwmaCost among the kept
ones, and one wait queue per Ingress worker. The RequestDeadline comes from the RequestDeadlineHeader
([../../protocol/design.md#requestdeadlineheader](../../protocol/design.md#requestdeadlineheader)).

```
function peak_ewma_deadline_choose(lb, L, remaining_ns):   # L after LiveSet, exclude and avoid; -> s | Dropped
    now = monotonic_now_ns()
    best = +infinity; keep = {}; probe = {}
    for s in L:
        (p, has_pending) = provisional_rtt(lb, s, now)
        if remaining_ns is none:            ok = true                       # no RequestDeadline: never filtered
        else if remaining_ns <= 0:          ok = false                      # already late: no backend
        else if peak[s].rtt_ms <= 0 and not has_pending: ok = true          # unmeasured and idle: eligible
        else:                               ok = not (lb.filter_margin * p * 1e6 > float(remaining_ns))   # FilterMargin γ; products rounded left to right
        if ok: keep[s] = peak_ewma_cost(lb, s); best = min(best, keep[s])
        else if filter_probe(lb, s, now, remaining_ns, has_pending): probe[s] = peak_ewma_cost(lb, s)   # FilterProbe
    if probe is not empty:                                # a probe wins over the kept backends
        keep = probe; best = min(probe values)
    T = [ s for s in L if s in keep and keep[s] == best ]
    if T is empty: return Dropped
    if len(T) == 1: return T[0]
    return T[native_random_u64() mod len(T)]           # ties: the implementation's random source
```

Waiting, per Ingress worker `w` (a client connection is served by one worker for its whole life):

```
FILTER_QUEUE_TICK_MS = 10
wq[w]      = FIFO of WaitEntry          # first attempts that no backend took
retries[w] = list of WaitEntry          # LbRetries that no backend took; never dropped

function admit_peak(lb, w, request, deadline):        # attempt 0
    if deadline is none: s = choose(...); pending_add(lb, s, request); return Sent(s)
    if wq[w] is not empty: wq[w].push_back(entry); return Wait     # FIFO: never overtakes at admission
    s = choose(lb, deadline - wall_clock_unix_ns(), ...)
    if s is Dropped: wq[w].push_back(entry); return Wait
    pending_add(lb, s, request); return Sent(s)

function retry_peak(lb, w, request, deadline, exclude, avoid):     # an LbRetry
    s = choose(lb, deadline - wall_clock_unix_ns() if deadline is not none else none, exclude, avoid)
    if s is Dropped: retries[w].append(entry); return Wait         # re-tried at every scan, never dropped
    pending_add(lb, s, request); return Sent(s)

procedure scan_peak(lb, w):                            # every FILTER_QUEUE_TICK_MS while wq[w] or retries[w] is not empty
    for e in wq[w], oldest first:
        if e is gone: remove e; continue
        remaining = e.deadline - wall_clock_unix_ns()
        if remaining <= 0: remove e; answer 503 (dropped, filter_dropped += 1); continue
        s = choose(lb, remaining, ...)
        if s is not Dropped: remove e; pending_add(lb, s, e.request); send e to s; continue
    for e in retries[w], oldest first:
        if e is gone: remove e; continue
        s = choose(lb, e.deadline - wall_clock_unix_ns(), e.exclude, e.avoid)   # late: Dropped, so it stays
        if s is not Dropped: remove e; pending_add(lb, s, e.request); send e to s
        else: e.refused += 1                           # counted into lb_retry_refused when it leaves
```

- **Every backend, lowest cost.** Unlike the shared DeadlineFilter, which hands the kept set to the LbPolicy's random
  pair, every kept backend is compared, so the cheapest one always wins.
- **Per worker.** Each worker has its own queue, retry list and 10 ms timer; FIFO order holds within a worker only.
  Backend state (PeakEwmaRtt, `outstanding`, PendingStarts) is shared.
- **Retries never queue behind first attempts** and are never dropped: a refused LbRetry is re-tried every scan until
  a backend is kept or its request ends (its earlier attempt keeps running meanwhile), as the `relay` Frontend
  re-decides a refused retry on every tick.
- **Dropped answer.** A request dropped past its deadline is answered `503` with a short plain-text body chosen by the
  implementation, not the `{"id":"<dec>"}` body of the shared FilterWaitQueue; a client classifies it `rejected` all
  the same.
- **Counters.** `filter_queued`, `filter_sent_from_queue`, `filter_dropped` and `filter_queue_max` as for the
  FilterWaitQueue, summed over workers; refused retry re-checks go to `lb_retry_refused`.

Worked example, two backends, 17 s SLO, one decision at each time shown; backend 0 stalls at 60 s (average
400 ms, last sample at 50 s, oldest pending forward from 59.9 s, as in [ProvisionalCurrentRtt](#provisionalcurrentrtt));
backend 1 stays at an average of 420 ms with 30 forwards in progress:

| Time | Backend 0: ProvisionalCurrentRtt, `outstanding`, cost | Backend 1: cost | Kept | Choice | Check |
|---|---|---|---|---|---|
| 59 s | 400 ms, 30, 400 × 31 = 12400 | 420 × 31 = 13020 | both | 0 | 12400 < 13020 |
| 70 s | 8787 ms, 300, 400 × 301 = 120400 | 13020 | both (8787 <= 17000) | 1 | 13020 < 120400 |
| 80 s | 19119 ms, 500, — | 13020 | 1 only (19119 > 17000) | 1 | the filter |

## FilterProbe

A backend the PeakEwmaDeadline refuses gets no request, so no answer can lower its RTT average: a healthy backend whose
average rose once (a burst, a short queue) could be refused for good. The **FilterProbe** sends it a request now and
then anyway, more likely the longer it has received none, so it can prove it is healthy again. Parameter
`lb_probe_tau_ms` (τ, ms, `>= 0`, default `0` = off).

```
last_sent[s]: u64 monotonic ns = 0       # LastSentTime: set by every pick that returns s (pending_add), shared by workers
probe_sent[s]: u64 monotonic ns or none = none   # set when a probe is sent to s; cleared by its answer (ProbeSample)

function filter_probe(lb, s, now, remaining_ns, has_pending):   # s was refused by the test; -> boolean
    if lb.probe_tau_ms == 0 or remaining_ns is none or remaining_ns <= 0: return false
    if has_pending: return false                      # only a backend with nothing pending is probed
    dt = now - last_sent[s]                           # monotonic ns, unsigned; never sent: last_sent 0, dt huge
    p  = 1 - exp(-(float(dt) / 1e6) / lb.probe_tau_ms)    # binary64
    u  = float(native_random_u64() >> 11) / 2^53      # one draw per refused backend, in L order, before the tie draw
    return u < p
```

- **Every decision flips.** A coin is flipped for each refused backend at every decision: a first attempt's
  admission, each `FILTER_QUEUE_TICK_MS` re-check of a waiting request, an LbRetry pick and its re-checks.
- **ProbeSample.** When a probe is sent to `s`, `probe_sent[s] = now`. The first RTT sample of `s` taken at or after
  that time is the probe's answer (a probed backend had nothing pending, so nothing else of `s` can answer first), and
  [PeakEwmaRtt](#peakewmartt)'s fold gives it weight 1: the average becomes the probe's RTT, instead of moving by
  `1 − e^(−Δt/τ_ewma)` (about 4% for a 400 ms answer). A backend refused because its average rose can thus be readmitted
  by one quick answer.

```
function probe_answer(lb, s, t):          # in fold, for a sample of s at monotonic time t; -> boolean
    p = probe_sent[s]
    if p is none or t < p: return false
    probe_sent[s] = none                  # compare-and-swap: of two workers folding at once, exactly one sees true
    return true
```

- **Heads sends the request there.** Probed backends win over the backends that passed the test; among several, the
  lowest PeakEwmaCost (ties random). The probe is a normal pick: it sets `last_sent`, so `dt` starts again from 0.
- **No flip** for an already-late request, a request without a RequestDeadline (never filtered), an avoided
  backend (it is not in `L`), or a backend with something pending (PendingStarts non-empty). A stalled backend keeps
  the forwards it never answers pending, so it is not probed; a healthy one that was refused drains its forwards, has
  nothing pending, and is probed.
- **Many decisions.** With requests waiting, a refused backend is checked about every 10 ms per waiting request, so it
  is probed soon after `dt` grows past a few ms, then again after the next quiet stretch; probes come in bursts.

| τ | `dt` | `p = 1 - e^(-dt/τ)` | Check |
|---|---|---|---|
| 4 s | 0 | 0 | just sent: never probed |
| 4 s | 0.4 s | 0.09516258196404048 | 1 − e^(−0.1) |
| 4 s | 4 s | 0.6321205588285577 | 1 − e^(−1) |
| 4 s | 8 s | 0.8646647167633873 | 1 − e^(−2) |
| 0 (off) | any | — | no flip: refused stays refused |
| 4 s | 8 s, something pending | — | no flip: only a backend with nothing pending is probed |

ProbeSample, worked example: average 2604 ms (refused with 4 s left at `γ = 2`); the probe answers in 401 ms 0.40 s after
the last sample. With the time-based weight the average would become `0.0392 × 401 + 0.9608 × 2604 = 2517.6` ms (still
refused); as a ProbeSample it becomes 401 ms, and the backend passes again (`2 × 401 = 802 <= 4000`).

## TokenBucketFilter

`lb_filter = token_bucket` (OptionalValue, with `lb_policy = peak_ewma`) is the **TokenBucketFilter**. It limits how
fast each backend is sent attempts instead of testing a deadline. Each backend `s` has one **TokenBucket**, kept by
the Ingress and shared by all Ingress workers. Every attempt sent to `s` (a first attempt, one sent from the wait
queue, an LbRetry) takes one token. The bucket refills at the **TokenRate** `r` tokens/s, which adapts by AIMD from
the latencies of the attempts `s` answers.

Parameters (Ingress, [api.md](api.md#parameters)): `lb_tb_slo_ms` (`slo`, required), `lb_tb_init_rate` (`init_r`,
default 50), `lb_tb_max_tokens` (`max_tokens`, default 10), `lb_tb_mult_dec` (default 2), `lb_tb_add_inc_limit`
(default 2), `lb_tb_add_inc_frac` (default 0.1) and `lb_tb_min_rate_frac` (default 0.1). Derived:
`add_inc = lb_tb_add_inc_frac × init_r`, `r_min = lb_tb_min_rate_frac × init_r`, `slo_ns = round(slo × 1e6)`.

```
TokenBucket = { tokens: binary64 = 0,               # starts empty
                rate: binary64 = init_r,            # TokenRate, tokens/s
                last_refill_ns: u64 = Ingress start (monotonic ns),
                last_decrease_ns: u64 or none = none,
                last_increase_ns: u64 or none = none,
                lock: mutex }                       # every function below holds bucket[s].lock throughout

procedure refill(b, now_ns):                        # monotonic now_ns >= b.last_refill_ns
    b.tokens = min(max_tokens, b.tokens + b.rate * float(now_ns - b.last_refill_ns) / 1e9)
    b.last_refill_ns = now_ns

function has_token(lb, s, now_ns):                  # -> boolean
    refill(lb.bucket[s], now_ns); return lb.bucket[s].tokens >= 1

function take_token(lb, s, now_ns):                 # -> boolean
    refill(lb.bucket[s], now_ns)
    if lb.bucket[s].tokens < 1: return false
    lb.bucket[s].tokens = lb.bucket[s].tokens - 1; return true

procedure rate_decrease(b, now):                    # a slow signal: at most one halving per SLO window
    if b.last_decrease_ns is none or now - b.last_decrease_ns >= slo_ns:
        b.rate = max(r_min, b.rate / lb_tb_mult_dec); b.last_decrease_ns = now

procedure token_rate_sample(lb, s, latency_ns):     # TokenRate: an attempt at s answered (response headers)
    b = lb.bucket[s]; now = monotonic_now_ns()
    refill(b, now)                                  # the old rate holds up to now
    if latency_ns > slo_ns:                                                       # slow
        rate_decrease(b, now)
    else if float(latency_ns) < float(slo_ns) / lb_tb_add_inc_limit:              # fast
        if b.last_increase_ns is none or now - b.last_increase_ns >= slo_ns:      # at most once per SLO window
            b.rate = b.rate + add_inc; b.last_increase_ns = now
    # otherwise (slo / add_inc_limit <= latency <= slo): no change

procedure stuck_attempt(lb, s, now):                # StuckAttempt: at every token_bucket_choose, for each s in L
    if pending[s] is not empty and now - min(pending[s]) > slo_ns:   # the oldest PendingStart at s is past the SLO
        refill(lb.bucket[s], now); rate_decrease(lb.bucket[s], now)

function token_bucket_choose(lb, L, remaining_ns):  # L after LiveSet, exclude and avoid; -> s | Dropped
    now = monotonic_now_ns()
    for s in L: stuck_attempt(lb, s, now)                                        # before any token test
    if remaining_ns is not none and remaining_ns <= 0: return Dropped            # already late: no backend
    if remaining_ns is none: K = L                                               # no RequestDeadline: never filtered
    else: K = [ s for s in L if has_token(lb, s, now) ]
    if K is empty: return Dropped
    best = min(peak_ewma_cost(lb, s) for s in K)
    T = [ s for s in K if peak_ewma_cost(lb, s) == best ]
    s = T[0] if len(T) == 1 else T[native_random_u64() mod len(T)]               # ties: the implementation's random source
    if not take_token(lb, s, now):
        if remaining_ns is none: return s           # never filtered: sent without a token
        return Dropped                              # another worker took it first: re-checked at the next scan
    return s
```

- **Latency of an attempt.** `token_rate_sample` is called by the Frontend when an attempt's response headers arrive,
  whatever the status. `latency_ns` runs from the attempt's request being sent to its response headers (monotonic
  ns). An attempt that ends without response headers (cancelled because another attempt answered first, reset, a
  connection error) gives no sample.
- **StuckAttempt.** A backend that stops answering gives no answers and so no slow samples. An attempt still pending
  at `s` longer than `slo` therefore counts as a slow signal, read from the [PendingStarts](#provisionalcurrentrtt) at
  each decision. The once-per-window rule applies, so a stalled backend's rate halves every `slo` down to `r_min`.
- **Increase at most once per SLO.** At about 100 answers/s, adding `add_inc` per fast answer raised the rate by
  hundreds of tokens/s every second, far past what a backend serves, and a once-per-window halving could not bring it
  back. Fast answers therefore add at most once per `slo` (their own window, kept apart from the decrease's).
- **Decrease at most once per SLO.** One stall makes every pending attempt at `s` slow at once. Halving on each of them
  would drive `r` to the floor within milliseconds, so a slow answer halves `r` only if `slo` has passed since the last
  halving. Others in that window change nothing.
- **Floor.** `r` never drops below `r_min > 0`, so a backend keeps receiving some attempts. With no attempts there
  would be no answers and no increase, and a recovered backend would stay shut out.
- **Waiting.** `admit_peak`, `retry_peak` and `scan_peak` of the [PeakEwmaDeadline](#peakewmadeadline) are used
  unchanged with `choose = token_bucket_choose`:
  - a first attempt that finds no token waits in its worker's FIFO and is answered `503` once its RequestDeadline
    passes;
  - a refused LbRetry is re-tried at every 10 ms scan;
  - `pending_add` and `pending_release` run as there.
- **Race.** Two workers may both see a token at `s` and only one can take it. `has_token` and `take_token` are
  separate locked steps, so the second take fails and that decision is `Dropped`. Its request waits, as if no backend
  had a token, and is re-checked at the next scan. It never takes a token from another backend in the same decision.
- **Ordering.** The tie-break draw happens only when `T` has more than one backend, after every `has_token`.
  `token_bucket_choose` makes no other random draw.
- **Not used:** FilterMargin and FilterProbe. `lb_filter_margin` and `lb_probe_tau_ms` are validated but ignored.

Worked example, refill (`max_tokens = 10`):

| Before | `r` | Elapsed | After | Check |
|---|---|---|---|---|
| 0 | 50 | 30 ms | 1.5 | 0 + 50 × 0.030 = 1.5: one take leaves 0.5 |
| 0.5 | 50 | 0 ms | 0.5 | no token: Dropped (waits) |
| 0.5 | 50 | 10 ms | 1.0 | 0.5 + 0.5 = 1.0 >= 1: a token |
| 0 | 50 | 300 ms | 10 | min(10, 15) = 10: the cap |

In the TokenRate example, "Last halving" is the time before the row's update. Increases have their own window (the
fast rows at 13 s and 17 s).

Worked example, TokenRate (`init_r = 50`, `slo = 4000 ms`, `mult_dec = 2`, `add_inc_limit = 2`, so fast is
`< 2000 ms`; `add_inc = 0.1 × 50 = 5`, `r_min = 0.1 × 50 = 5`):

| Answer at | Latency | Last halving | `r` before → after | Check |
|---|---|---|---|---|
| 0 s | 4500 ms | none | 50 → 25 | slow, no earlier halving |
| 1 s | 4500 ms | 0 s | 25 → 25 | slow, 1 s < 4 s since the last halving |
| 4 s | 4500 ms | 0 s | 25 → 12.5 | slow, 4 s >= 4 s |
| 8 s | 4500 ms | 4 s | 12.5 → 6.25 | 12.5 / 2 |
| 12 s | 4500 ms | 8 s | 6.25 → 5 | max(5, 3.125) = 5: the floor |
| 13 s | 1500 ms | 12 s | 5 → 10 | fast: 1500 < 2000, no earlier increase |
| 14 s | 1500 ms | 12 s | 10 → 10 | fast, 1 s < 4 s since the last increase |
| 15 s | 2000 ms | 12 s | 10 → 10 | not < 2000: no change |
| 16 s | 4000 ms | 12 s | 10 → 10 | not > 4000: no change |
| 17 s | 1500 ms | 12 s | 10 → 15 | fast, 17 − 13 = 4 s >= 4 s |
| 18 s | StuckAttempt: oldest pending sent at 13.5 s | 12 s | 15 → 7.5 | 18 − 13.5 = 4.5 s > 4 s; 18 − 12 = 6 s >= 4 s |
| 19 s | StuckAttempt, same attempt | 18 s | 7.5 → 7.5 | 1 s < 4 s since the last halving |

## PickLog

The **PickLog** records, at every `pick` (forwarded or dropped), what the LoadBalancer sees of every backend, so the
DeadlineFilter's inputs can be plotted over time. It is an optional output: an implementation may leave it empty
(the file then has only its header).

```
function record_pick(lb):
    t = monotonic_now_ns()
    rows = [ PickRecord(t, s, lb.stats[s].outstanding.load(), exec_value(lb, s), lb.stats[s].worker_count.load())
             for s in 0 ..= B-1 ]                     # every backend, live or not, in id order
    with lb.log_lock:
        lb.picks.extend(rows)

function pick_log(lb):                               # -> list of PickRecord, in append order
    with lb.log_lock:
        return copy of lb.picks
```

- **When.** Before `choose`, so `outstanding` excludes the request being placed: `(outstanding + 1) × EwmaExecTime`
  is the mean-based expected time to completion this request would face on that backend, and `outstanding`,
  WorkerCount and EwmaExecTime are the DeadlineFilter's `n`, `k` and `e`. Every policy and every `lb_filter` value
  records, so the log is comparable across runs.
- **Reads.** The loads are separate from those of `choose`, a few nanoseconds earlier; another pick may change a
  counter in between. The log describes the state, it does not replay the decision exactly.
- **Memory and lock.** `B` records per `/ping`, kept until shutdown (a 120 s run at 70 req/s with 2 backends is
  about 17_000 records). `log_lock` is held for one append of `B` records, as for the ForwardLog, and never nests.

Worked example, `B = 2`, backend 1 without an ExecutionTime sample yet, a pick at `t = 1_000`:

| `t_ns` | `server_id` | `outstanding` | `ewma_exec_ns` | `worker_count` | `(outstanding + 1) × ewma_exec_ns` |
|---|---|---|---|---|---|
| 1_000 | 0 | 12 | 140_000_000 | 10 | 1_820_000_000 |
| 1_000 | 1 | 0 | none | 0 | — (no sample) |

## ForwardLog

```
function forward_log(lb):                            # -> list of ForwardRecord, in append order
    with lb.log_lock:
        return copy of lb.log
```

- **One record per `finish`**, for every outcome and every policy, appended in the order the appends take the
  lock. A forward abandoned at shutdown never calls `finish` and has no record.
- **Fields.** `end_ns` is the monotonic time `finish` read, `latency_ns = end_ns - start_ns`, and
  `ewma_latency_ns` is the backend's EwmaLatency after this finish: the value `ewma_update` stored for `ok`, or the
  value read for the other outcomes, which do not change it. `exec_ns` is the ExecutionTime the backend reported
  (none unless `ok` or `rejected`), and `ewma_exec_ns` is the backend's EwmaExecTime after this finish (none before
  its first sample).
- **Lock.** `log_lock` is held only for one append or one copy, and never nests with another lock. The Ingress's
  concurrency model allows it ([../design.md#concurrency-model](../design.md#concurrency-model)).
- **Memory.** The log grows by one record per forward for the whole run, and is kept in memory until shutdown. A
  30 s run at 4000 forwards/s is 120000 records.
- `forward_log` is called by the Ingress's shutdown after it waited for the forwards in progress. The copy makes
  it safe even if an abandoned forward finishes during the write.
- **Recorded from outside the balancer.** An implementation whose balancer cannot keep this log (it is reused, not
  written for the testbed) may build it at shutdown from a per-attempt access log instead. Then `latency_ns` runs from
  the client request's arrival at the Ingress to the end of the attempt's upstream answer, `ewma_latency_ns` is
  recomputed from those latencies by [EwmaLatency](#ewmalatency), and `exec_ns`, `ewma_exec_ns` and `estimate_ns`
  are empty (ForwardLogFromAccessLog). The rows, outcomes and `attempt` numbers are the same.

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

ForwardLog, `ewma_beta = 0.7`, one backend starting at `1_000_000`, three forwards:

| `start_ns` | `end_ns` | outcome | `latency_ns` | `ewma_latency_ns` in the record | Check |
|---|---|---|---|---|---|
| 100 | 2_000_100 | `ok` | 2_000_000 | 1_300_000 | 1_000_000 × 0.7 + 2_000_000 × 0.3 |
| 200 | 300_200 | `rejected` | 300_000 | 1_300_000 | not a sample: unchanged |
| 300 | 500_300 | `ok` | 500_000 | 1_060_000 | 1_300_000 × 0.7 + 500_000 × 0.3 = 910_000 + 150_000 |

EwmaExecTime, `ewma_beta = 0.7`, one backend, in finish order:

| outcome | `exec_ns` given | `ewma_exec_ns` in the record | Check |
|---|---|---|---|
| `reset` | none | none | no sample yet |
| `ok` | 2_000_000 | 2_000_000 | first sample is the average |
| `rejected` | 1_000_000 | 1_700_000 | 2_000_000 × 0.7 + 1_000_000 × 0.3 = 1_400_000 + 300_000 |
| `bad_gateway` | none | 1_700_000 | not a sample: unchanged |
| `ok` | 500_000 | 1_340_000 | 1_700_000 × 0.7 + 500_000 × 0.3 = 1_190_000 + 150_000 |
| `ok` | none (old server, no ExecReport) | 1_340_000 | not a sample: unchanged |

`B = 1`: every policy returns 0 and draws nothing.

LiveSet, `B = 3`, backend 1 not live, so `L = [0, 2]` and `n = 2`:

| Policy | State | Draws `(i, j)` | Choice | Check |
|---|---|---|---|---|
| `round_robin` | rr = 0, 1, 2 | none | 0, 2, 0 | `L[0]`, `L[1]`, `L[2 mod 2]` |
| `least_outstanding` | `outstanding = (3, 0, 1)` | none | 2 | backend 1 is skipped although its 0 is smallest |
| `power_of_two` | `outstanding = (3, 0, 1)` | (0, 0) | 2 | `j` → 1; pair `(L[0], L[1]) = (0, 2)`; 1 < 3 |
| `ewma` | any | (1, 0) | — | pair `(L[0], L[1]) = (0, 2)`; the scores of 0 and 2 decide |
| any | only backend 2 live | none | 2 | `n = 1`: no draw |
| any | no backend live | as for `B = 3` | as without a monitor | `L = [0, 1, 2]` |

## Design specific to Envoy

Implementation: `implementation/envoy`: vendored Envoy (`envoy/`) and Nighthawk (`nighthawk/`) with the testbed's
changes, recorded in [VENDORED.md](../../../implementation/envoy/VENDORED.md) ([AGENTS.md](../../../AGENTS.md#vendored-code)),
built in Docker with Bazel by `implementation/envoy/build.sh` (C++ tests: `bazel test` on the targets VENDORED.md
names); Python 3 wrappers `bin/ingress.py`, `bin/loadgen.py` and `wrappers/convert.py` (tests: `cd
implementation/envoy/wrappers && python3 -B -m unittest test_convert`). Only code written or changed for the testbed
carries `Design:` tags; unchanged upstream code does not.

### Envoy choices

- LoadBalancer and LbPolicy: `lb_policy = peak_ewma` is Envoy's contrib `peak_ewma` policy (upstream
  `peak_ewma_lb.cc`); with `deadline_provisional_rtt` it is the testbed's `peak_ewma_deadline` policy
  (`deadline_lb.cc`, new), which reuses it. Our change to `peak_ewma_lb.cc` honors the retry host predicates
  (AvoidedBackends, with the fall-back to all hosts); the native random source is Envoy's `RandomGenerator`.
- PeakEwmaRtt: upstream records first-byte RTTs in its HTTP filter and folds them every 100 ms from a 1000-sample
  ring; our change gives a sample with Δt <= 0 the weight 0 (`calculateTimeBasedAlpha`; upstream gave it 1).
- ProvisionalCurrentRtt and PeakEwmaDeadline: `PendingRequests` host data (shared by the workers) and a
  `PendingRegistration` in the request's filter state, released when the downstream stream ends; `decide` filters
  every healthy host and takes the lowest cost; per-worker `queue_` and `retry_waiters_` with a 10 ms dispatcher
  timer; a request dropped past its deadline gets Envoy's local reply `503 no healthy upstream`.
- EwmaExecTime and EwmaAntiServiceTime: kept by the `testbed_anxious` upstream filter from each answer's ExecReport
  (only when `lb_retry_policy = anxious`), and recomputed for the summary by `convert.exec_ewmas`.
- ForwardLog and EwmaLatency: ForwardLogFromAccessLog, built by `convert.write_forwards` from the router's
  per-attempt upstream log; EwmaLatency is recomputed there with `ewma_beta`. FilterWaitQueue counters come from the
  balancer's queue trace (`convert.filter_counts`).
- ForwardTicket: the TicketSeq of an attempt is assigned by `testbed_anxious` when the attempt's headers go upstream.

- FilterMargin: the `peak_ewma_deadline` proto field `filter_margin` (unset or below 1 means 1), set from `--lb_filter_margin` by `bin/ingress.py`; `decide` compares `filter_margin_ * provisional * 1e6` with the time left (test `MarginScalesTheProvisionalRtt`).

- FilterProbe: the proto field `probe_tau` (unset: off), set from `--lb_probe_tau_ms`; the last-sent time is an atomic in the `PendingRequests` host data, stored in `registerPending` for every handed-out host; `decide` flips one `random_.random()` coin per refused host with nothing pending (53-bit uniform) and prefers the probed hosts; probe decisions are traced with kind `probe`.
- ProbeSample: `decide` stores the probe's monotonic time in an atomic of the probed host's `PendingRequests` (0: none). Upstream `processHostSamples` calls a virtual `fullWeightSample(host, t)` (default false) before the time-based alpha; the deadline balancer overrides it to consume the mark with a compare-and-swap when `t >= mark`, and the sample is then folded with alpha = 1 (the existing `updateEwmaWithSample`, which returns the sample).
- TokenBucketFilter: the proto message `TokenBucket` (field `token_bucket`; `slo` a Duration, the other parameters `DoubleValue`s, unset meaning the spec default) switches `decide` from the RTT test to the token test; `lb_filter = token_bucket` maps to the `peak_ewma_deadline` policy. Each host's bucket (`token_bucket.h`: one mutex per bucket, binary64 tokens and rate) lives in its `PendingRequests` and is initialized once, by the first balancer to see the host, at that balancer's start. A refill with a clock reading older than the bucket's last refill (another worker refilled later) adds nothing. `decide` checks `hasToken` per host and then `take`s on the chosen one, so a take lost to another worker returns none, as the spec's race. `token_rate_sample` is the upstream filter `envoy.filters.http.testbed_token_bucket`, one instance per attempt: request headers to response headers on the monotonic clock; an attempt destroyed without response headers reports nothing. Diagnostic trace `<prefix>.tokens.0.csv`: take, race and untaken at a pick, slow, slow_held, fast and mid at a sample.

### Not traced

| Design subsection | Reason |
|---|---|
| [EstimatedFinish](#estimatedfinish) | not realized on Envoy: `estimate_ns` is empty in a ForwardLog built from an access log ([ForwardLog](#forwardlog)) |
| [EwmaScore](#ewmascore) | not realized on Envoy: `lb_policy ewma is not supported by this implementation` |
| [PeakEwmaCost](#peakewmacost) | unchanged upstream code: `envoy/contrib/peak_ewma/load_balancing_policies/source/cost.cc` |
| [PickLog](#picklog) | optional output: Envoy writes `picks.csv` with its header only |

### Traceability

Generated from the `Design:` tags in the testbed's code under `implementation/envoy` by `implementation/spec_tools/tracegen.py` (see `AGENTS.md`). Each link points at the first code line after the tag.
Regenerate from the repository root with `python3 implementation/spec_tools/tracegen.py --lang Envoy --impl implementation/envoy --ext .cc --ext .h --comment // --ext .py:# --write`.

<!-- trace:begin -->
| Design subsection | Implementation (file:line) |
|---|---|
| [LoadBalancer](#loadbalancer) | [deadline_lb.cc:352](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/source/deadline_lb.cc#L352) |
| [ForwardTicket](#forwardticket) | [filter.cc:164](../../../implementation/envoy/envoy/contrib/peak_ewma/anxious/source/filter.cc#L164) |
| [EstimatedFinish](#estimatedfinish) | not traced |
| [EwmaLatency](#ewmalatency) | [convert.py:136](../../../implementation/envoy/wrappers/convert.py#L136) |
| [EwmaExecTime](#ewmaexectime) | [anxious_tracker.cc:18](../../../implementation/envoy/envoy/contrib/peak_ewma/anxious/source/anxious_tracker.cc#L18), [anxious_tracker.cc:59](../../../implementation/envoy/envoy/contrib/peak_ewma/anxious/source/anxious_tracker.cc#L59), [filter.cc:203](../../../implementation/envoy/envoy/contrib/peak_ewma/anxious/source/filter.cc#L203), [convert.py:292](../../../implementation/envoy/wrappers/convert.py#L292) |
| [EwmaAntiServiceTime](#ewmaantiservicetime) | [anxious_tracker.cc:59](../../../implementation/envoy/envoy/contrib/peak_ewma/anxious/source/anxious_tracker.cc#L59), [filter.cc:102](../../../implementation/envoy/envoy/contrib/peak_ewma/anxious/source/filter.cc#L102), [anxious_tracker_test.cc:151](../../../implementation/envoy/envoy/contrib/peak_ewma/anxious/test/anxious_tracker_test.cc#L151), [convert.py:292](../../../implementation/envoy/wrappers/convert.py#L292) |
| [EwmaScore](#ewmascore) | not traced |
| [PeakEwmaRtt](#peakewmartt) | [ingress.py:281](../../../implementation/envoy/bin/ingress.py#L281), [peak_ewma_lb.cc:386](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/source/peak_ewma_lb.cc#L386), [peak_ewma_lb_host_lifecycle_test.cc:367](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/test/peak_ewma_lb_host_lifecycle_test.cc#L367) |
| [PeakEwmaCost](#peakewmacost) | not traced |
| [LbPolicy](#lbpolicy) | [ingress.py:162](../../../implementation/envoy/bin/ingress.py#L162), [deadline_lb.cc:237](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/source/deadline_lb.cc#L237), [peak_ewma_lb.cc:307](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/source/peak_ewma_lb.cc#L307), [deadline_lb_test.cc:579](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/test/deadline_lb_test.cc#L579), [deadline_lb_test.cc:589](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/test/deadline_lb_test.cc#L589), [peak_ewma_lb_comprehensive_test.cc:120](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/test/peak_ewma_lb_comprehensive_test.cc#L120) |
| [DeadlineFilter](#deadlinefilter) | [deadline_lb.cc:217](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/source/deadline_lb.cc#L217) |
| [FilterWaitQueue](#filterwaitqueue) | [convert.py:315](../../../implementation/envoy/wrappers/convert.py#L315) |
| [ProvisionalCurrentRtt](#provisionalcurrentrtt) | [deadline_lb.cc:25](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/source/deadline_lb.cc#L25), [deadline_lb.cc:56](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/source/deadline_lb.cc#L56), [deadline_lb.cc:138](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/source/deadline_lb.cc#L138), [deadline_lb.cc:166](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/source/deadline_lb.cc#L166), [deadline_lb_test.cc:536](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/test/deadline_lb_test.cc#L536), [deadline_lb_test.cc:551](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/test/deadline_lb_test.cc#L551), [deadline_lb_test.cc:564](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/test/deadline_lb_test.cc#L564) |
| [PeakEwmaDeadline](#peakewmadeadline) | [deadline_lb.cc:217](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/source/deadline_lb.cc#L217), [deadline_lb.cc:352](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/source/deadline_lb.cc#L352), [deadline_lb.cc:397](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/source/deadline_lb.cc#L397), [deadline_lb.cc:410](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/source/deadline_lb.cc#L410), [deadline_lb.cc:427](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/source/deadline_lb.cc#L427), [deadline_lb_test.cc:133](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/test/deadline_lb_test.cc#L133), [deadline_lb_test.cc:337](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/test/deadline_lb_test.cc#L337), [deadline_lb_test.cc:348](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/test/deadline_lb_test.cc#L348), [deadline_lb_test.cc:364](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/test/deadline_lb_test.cc#L364), [deadline_lb_test.cc:372](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/test/deadline_lb_test.cc#L372), [deadline_lb_test.cc:380](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/test/deadline_lb_test.cc#L380), [deadline_lb_test.cc:390](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/test/deadline_lb_test.cc#L390), [deadline_lb_test.cc:433](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/test/deadline_lb_test.cc#L433), [deadline_lb_test.cc:446](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/test/deadline_lb_test.cc#L446), [deadline_lb_test.cc:462](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/test/deadline_lb_test.cc#L462), [deadline_lb_test.cc:477](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/test/deadline_lb_test.cc#L477), [deadline_lb_test.cc:502](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/test/deadline_lb_test.cc#L502), [deadline_lb_test.cc:517](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/test/deadline_lb_test.cc#L517), [convert.py:304](../../../implementation/envoy/wrappers/convert.py#L304), [convert.py:315](../../../implementation/envoy/wrappers/convert.py#L315), [test_convert.py:100](../../../implementation/envoy/wrappers/test_convert.py#L100) |
| [FilterProbe](#filterprobe) | [ingress.py:241](../../../implementation/envoy/bin/ingress.py#L241), [deadline_lb.cc:47](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/source/deadline_lb.cc#L47), [deadline_lb.cc:146](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/source/deadline_lb.cc#L146), [deadline_lb.cc:268](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/source/deadline_lb.cc#L268), [deadline_lb.cc:306](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/source/deadline_lb.cc#L306), [deadline_lb.cc:343](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/source/deadline_lb.cc#L343), [peak_ewma_lb.cc:450](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/source/peak_ewma_lb.cc#L450), [deadline_lb_test.cc:155](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/test/deadline_lb_test.cc#L155), [deadline_lb_test.cc:164](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/test/deadline_lb_test.cc#L164), [deadline_lb_test.cc:175](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/test/deadline_lb_test.cc#L175), [deadline_lb_test.cc:200](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/test/deadline_lb_test.cc#L200), [deadline_lb_test.cc:214](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/test/deadline_lb_test.cc#L214), [deadline_lb_test.cc:240](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/test/deadline_lb_test.cc#L240) |
| [TokenBucketFilter](#tokenbucketfilter) | [ingress.py:175](../../../implementation/envoy/bin/ingress.py#L175), [ingress.py:194](../../../implementation/envoy/bin/ingress.py#L194), [deadline_lb.cc:79](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/source/deadline_lb.cc#L79), [deadline_lb.cc:260](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/source/deadline_lb.cc#L260), [deadline_lb.cc:314](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/source/deadline_lb.cc#L314), [token_bucket.cc:14](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/source/token_bucket.cc#L14), [token_bucket.cc:33](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/source/token_bucket.cc#L33), [token_bucket.cc:65](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/source/token_bucket.cc#L65), [deadline_lb_test.cc:262](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/test/deadline_lb_test.cc#L262), [deadline_lb_test.cc:272](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/test/deadline_lb_test.cc#L272), [deadline_lb_test.cc:288](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/test/deadline_lb_test.cc#L288), [deadline_lb_test.cc:297](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/test/deadline_lb_test.cc#L297), [deadline_lb_test.cc:313](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/test/deadline_lb_test.cc#L313), [deadline_lb_test.cc:326](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/test/deadline_lb_test.cc#L326), [token_bucket_test.cc:32](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/test/token_bucket_test.cc#L32), [token_bucket_test.cc:47](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/test/token_bucket_test.cc#L47), [token_bucket_test.cc:58](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/test/token_bucket_test.cc#L58), [token_bucket_test.cc:88](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/test/token_bucket_test.cc#L88), [filter.cc:21](../../../implementation/envoy/envoy/contrib/peak_ewma/token_bucket/source/filter.cc#L21), [filter.cc:31](../../../implementation/envoy/envoy/contrib/peak_ewma/token_bucket/source/filter.cc#L31), [test_convert.py:214](../../../implementation/envoy/wrappers/test_convert.py#L214), [test_ingress.py:47](../../../implementation/envoy/wrappers/test_ingress.py#L47) |
| [PickLog](#picklog) | not traced |
| [ForwardLog](#forwardlog) | [ingress.py:248](../../../implementation/envoy/bin/ingress.py#L248), [convert.py:50](../../../implementation/envoy/wrappers/convert.py#L50), [convert.py:136](../../../implementation/envoy/wrappers/convert.py#L136), [test_convert.py:17](../../../implementation/envoy/wrappers/test_convert.py#L17), [test_convert.py:26](../../../implementation/envoy/wrappers/test_convert.py#L26) |
| [Worked examples](#worked-examples) | [deadline_lb_test.cc:536](../../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/test/deadline_lb_test.cc#L536) |
<!-- trace:end -->
