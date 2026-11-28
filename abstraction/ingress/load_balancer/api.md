# Load Balancer — API

## Parameters

From the Ingress parameters ([../api.md#program-parameters](../api.md#program-parameters)); they must match it.

| Parameter | Type / unit / constraint | Default | Meaning |
|---|---|---|---|
| `lb_policy` | `round_robin` \| `least_outstanding` \| `power_of_two` \| `ewma` \| `peak_ewma` (OptionalValue) | `least_outstanding` | The LbPolicy; `peak_ewma` uses PeakEwmaRtt and PeakEwmaCost ([design.md#peakewmartt](design.md#peakewmartt)) |
| `lb_filter` | `none` \| `deadline_mean` \| `deadline_mean_per_worker` \| `deadline_mean_queue_plus_share` \| `deadline_mmk` \| `deadline_provisional_rtt` \| `token_bucket` \| `bbr` (OptionalValue) | `none` | The DeadlineFilter's DeadlineStrategy, or off ([design.md#deadlinefilter](design.md#deadlinefilter)); `deadline_provisional_rtt` needs `lb_policy = peak_ewma` and is the PeakEwmaDeadline ([design.md#peakewmadeadline](design.md#peakewmadeadline)); `token_bucket` (OptionalValue, needs `lb_policy = peak_ewma`) is the TokenBucketFilter: a per-backend token bucket whose refill rate adapts by AIMD ([design.md#tokenbucketfilter](design.md#tokenbucketfilter)); `bbr` (OptionalValue, needs `lb_policy = peak_ewma`) is the BbrFilter: Linux BBR v1 per backend, in requests, with a Cwnd and a PacingRate ([design.md#bbrfilter](design.md#bbrfilter)) |
| `lb_filter_margin` | float, `>= 1`, finite | `1` | FilterMargin `γ` of the PeakEwmaDeadline: a backend is refused when `γ ×` its ProvisionalCurrentRtt exceeds the request's time left; `1` is the plain test. Used only by `lb_filter = deadline_provisional_rtt`, validated always ([design.md#peakewmadeadline](design.md#peakewmadeadline)) |
| `lb_probe_tau_ms` | float, ms, `>= 0`, finite | `0` | FilterProbe `τ`: a backend the PeakEwmaDeadline refuses still gets the request with probability `1 − e^(−Δt/τ)` if it has nothing pending, Δt the time since it last got one; `0` turns probing off. Used only by `lb_filter = deadline_provisional_rtt`, validated always ([design.md#filterprobe](design.md#filterprobe)) |
| `lb_tb_slo_ms` | float, ms, `> 0`, finite; required with `lb_filter = token_bucket` | `0` (unset) | TokenRate: the SLO an answered attempt's latency is judged against. Validated always (`0` or finite `> 0`), used only by `lb_filter = token_bucket` ([design.md#tokenbucketfilter](design.md#tokenbucketfilter)) |
| `lb_tb_init_rate` | float, tokens/s, `> 0`, finite | `50` | TokenRate `init_r`: every backend's refill rate at start ([design.md#tokenbucketfilter](design.md#tokenbucketfilter)) |
| `lb_tb_max_tokens` | float, tokens, `>= 1`, finite | `10` | TokenBucket capacity `max_tokens` ([design.md#tokenbucketfilter](design.md#tokenbucketfilter)) |
| `lb_tb_mult_dec` | float, `> 1`, finite | `2` | TokenRate `mult_dec_const`: a slow answer, or an attempt still pending after `lb_tb_slo_ms` (StuckAttempt), sets `r = r / mult_dec`, at most once per `lb_tb_slo_ms` ([design.md#tokenbucketfilter](design.md#tokenbucketfilter)) |
| `lb_tb_add_inc_limit` | float, `>= 1`, finite | `2` | TokenRate `add_inc_limit`: an answer faster than `lb_tb_slo_ms / add_inc_limit` raises `r`, at most once per `lb_tb_slo_ms` ([design.md#tokenbucketfilter](design.md#tokenbucketfilter)) |
| `lb_tb_add_inc_frac` | float, `>= 0`, finite | `0.1` | TokenRate `add_inc_const = lb_tb_add_inc_frac × init_r`, added per fast answer, at most once per `lb_tb_slo_ms` ([design.md#tokenbucketfilter](design.md#tokenbucketfilter)) |
| `lb_tb_min_rate_frac` | float, `0 < x <= 1`, finite | `0.1` | TokenRate floor `r_min = lb_tb_min_rate_frac × init_r` ([design.md#tokenbucketfilter](design.md#tokenbucketfilter)) |
| `lb_bbr_init_cwnd` | u32, attempts, `>= 4` | `10` | BbrFilter initial Cwnd of every backend (Linux `TCP_INIT_CWND`). Validated always, used only by `lb_filter = bbr` ([design.md#bbrfilter](design.md#bbrfilter)) |
| `ewma_beta` | float, `0 <= ewma_beta <= 1` | `0.7` | Weight of the old value in each EwmaLatency update ([design.md#ewmalatency](design.md#ewmalatency)) |

## Constants

| Name | Value | Meaning |
|---|---|---|
| `EWMA_INITIAL_LATENCY_NS` | `1_000_000` (1 ms) | Every backend's EwmaLatency before its first sample. Not 0, so that the `ewma` policy still weighs `outstanding` before any response |
| `DEADLINE_FILTER_CONFIDENCE` | `0.99` | A backend passes the DeadlineFilter if the request finishes there in time with at least this probability ([design.md#deadlinefilter](design.md#deadlinefilter)) |
| `DEADLINE_FILTER_MISS` | `0.01` | `1 - DEADLINE_FILTER_CONFIDENCE`: the largest allowed late probability |
| `PEAK_EWMA_DECAY_MS` | `10_000` | `tau` of the PeakEwmaRtt's time-based weight ([design.md#peakewmartt](design.md#peakewmartt)) |
| `PEAK_EWMA_AGGREGATION_MS` | `100` | New RTT samples are folded at most this often per Ingress worker |
| `PEAK_EWMA_MAX_SAMPLES` | `1_000` | RTT samples kept per backend between two folds |
| `PEAK_EWMA_DEFAULT_RTT_MS` | `10.0` | PeakEwmaCost latency of a backend that is unmeasured and idle ([design.md#peakewmacost](design.md#peakewmacost)) |
| `PEAK_EWMA_PENALTY` | `1_000_000.0` | PeakEwmaCost of a backend in use but never answered, plus its `outstanding` |
| `EXEC_NO_SAMPLE` | binary64 quiet NaN, bits `0x7FF8000000000000` | Stored in `ewma_exec_ns` and `ewma_anti_exec_ns` until their first sample ([design.md#ewmaexectime](design.md#ewmaexectime), [design.md#ewmaantiservicetime](design.md#ewmaantiservicetime)) |

## Types

### `LbPolicy`

`round_robin` \| `least_outstanding` \| `power_of_two` \| `ewma` \| `peak_ewma`, from the Ingress parameter `lb_policy`
([../api.md#program-parameters](../api.md#program-parameters)). See [design.md#lbpolicy](design.md#lbpolicy).

### `ForwardOutcome`

How a forward ended: `ok` (`202` relayed), `rejected` (`503` relayed), `bad_request` (`400` relayed),
`bad_gateway` (`502` sent), `reset` (the client reset its stream). Each maps to the BackendStats field of the same
name ([../api.md#backendstats](../api.md#backendstats)).

### `ForwardTicket`

| Field | Type | Meaning |
|---|---|---|
| `server_id` | u32 | The chosen backend |
| `start_ns` | u64, ns | Monotonic time of the `pick` |
| `attempt` | u32 | LbRetries of the ForwardTask before this attempt: `0` for its first ([../retry_policy/design.md](../retry_policy/design.md#retrypolicy)) |
| `seq` | u64 | TicketSeq: unique per run, increasing in pick order ([design.md#forwardticket](design.md#forwardticket)) |
| `queue_at_send` | u64 | QueueAtSend: the backend's `outstanding` just before this pick's increment ([design.md#forwardticket](design.md#forwardticket)) |
| `estimate_ns` | binary64, ns, or none | EstimatedFinish `(queue_at_send + 1) × EwmaExecTime / k` (k: WorkerCount, 1 while unknown) at the pick; none before the backend's first ExecutionTime sample ([design.md#estimatedfinish](design.md#estimatedfinish)) |

Returned by `pick`, given back unchanged to `finish`. See [design.md#forwardticket](design.md#forwardticket).

### `ForwardRecord`

| Field | Type | Meaning |
|---|---|---|
| `end_ns` | u64, ns | Monotonic time at which `finish` ran |
| `server_id` | u32 | The backend |
| `outcome` | ForwardOutcome | How the forward ended |
| `latency_ns` | u64, ns | `end_ns - start_ns` of the ForwardTicket, for every outcome |
| `ewma_latency_ns` | float (binary64), ns | The backend's EwmaLatency right after this `finish` |
| `exec_ns` | u64, ns, or none | The ExecutionTime the backend reported for this forward; none unless `ok` or `rejected` with a valid ExecReport |
| `ewma_exec_ns` | float (binary64), ns, or none | The backend's EwmaExecTime right after this `finish`; none before its first sample |
| `attempt` | u32 | The ticket's `attempt` |
| `estimate_ns` | binary64, ns, or none | The ticket's EstimatedFinish ([design.md#estimatedfinish](design.md#estimatedfinish)) |

One per `finish`, kept in the **ForwardLog**. See [design.md#forwardlog](design.md#forwardlog).

### `PickRecord`

| Field | Type | Meaning |
|---|---|---|
| `t_ns` | u64, ns | Monotonic time of the `pick`, read before `choose` |
| `server_id` | u32 | The backend described (every backend gets a record per pick) |
| `outstanding` | i64 | Its forwards in progress, before this pick's |
| `ewma_exec_ns` | float (binary64), ns, or none | Its EwmaExecTime; none before its first sample |
| `worker_count` | u32 | Its WorkerCount, `0` before its first answer |

`B` per `pick`, kept in the **PickLog**. See [design.md#picklog](design.md#picklog).

### `LoadBalancer`

The policy, `ewma_beta`, the BackendStats array, the round-robin counter, the policy's random stream and the
ForwardLog. Safe for
concurrent callers. See [design.md#loadbalancer](design.md#loadbalancer).

## Functions

### `lb_new(config: IngressConfig, monitor: HeartbeatMonitor | none) -> LoadBalancer`
`B = len(config.backends)` BackendStats, all counters zero, every `ewma_latency_ns = EWMA_INITIAL_LATENCY_NS` and
every `ewma_exec_ns` and `ewma_anti_exec_ns = EXEC_NO_SAMPLE`.
`monitor` gives the BackendLiveness; with `none` every backend is live.
The random stream is `rng_new(derive_seed(config.seed, STREAM_INGRESS_LB, 0))`.
See [design.md#loadbalancer](design.md#loadbalancer).

### `pick(lb, remaining_ns: i64 | none, attempt: u32, record: boolean = true, exclude: u32 | none = none, avoid: set of u32 = {}) -> ForwardTicket | Dropped`
Applies the DeadlineFilter when `lb_filter` is not `none` and `remaining_ns` is given, then chooses a backend by the
policy among the live (all if none is live) and eligible ones. If none is eligible it returns `Dropped` and changes
nothing; the caller decides what follows (a first attempt waits in the FilterWaitQueue, a refused LbRetry is skipped).
Otherwise it increments the chosen backend's `outstanding` and `forwarded` and stamps the ticket with the current
monotonic time; every such `pick` must be followed by exactly one `finish` with the same ticket. `record = false`
(a FilterWaitQueue re-check) writes no PickRecord. `exclude` (an LbRetry decided `RetryElsewhere`) removes that
backend from the LiveSet; `Dropped` if none is left ([design.md#lbpolicy](design.md#lbpolicy)). `avoid` (HedgedForwarding) leaves its
backends out unless that leaves none, and never causes `Dropped`. See [design.md#lbpolicy](design.md#lbpolicy),
[design.md#deadlinefilter](design.md#deadlinefilter) and [design.md#forwardticket](design.md#forwardticket).

### `admit(lb, deadline: u64 | none) -> Sent(ForwardTicket) | Wait(WaitEntry)`
For a first attempt: sends at once if the FilterWaitQueue is empty and a backend passes; otherwise appends a
WaitEntry (FIFO) whose `signal` fires when a later scan sends it (`sent(ticket)`) or drops it past its deadline
(`dropped`). The caller marks it `gone` if its client resets. See [design.md#filterwaitqueue](design.md#filterwaitqueue).

### `scan(lb) -> nothing`
Every `FILTER_QUEUE_TICK_MS = 10` while `lb_filter` is not `none`: drops waiting requests past their deadline and
sends those that now pass, oldest first. See [design.md#filterwaitqueue](design.md#filterwaitqueue).

### `finish(lb, ticket: ForwardTicket, outcome: ForwardOutcome, exec_ns: u64 | none, worker_count: u32 | none) -> sampled: boolean`
Decrements `outstanding` and increments the outcome's counter. If `outcome == ok`, also folds the forward's latency
into the backend's EwmaLatency, under every policy. If `outcome` is `ok` or `rejected` and `exec_ns` is given, folds
it into the backend's EwmaExecTime ([design.md#ewmaexectime](design.md#ewmaexectime)) and every other backend's
EwmaAntiServiceTime ([design.md#ewmaantiservicetime](design.md#ewmaantiservicetime)), and returns `sampled = true`. Then appends one ForwardRecord to the ForwardLog. See [design.md#loadbalancer](design.md#loadbalancer) and
[design.md#ewmalatency](design.md#ewmalatency). For `ok` and `rejected`, a given `worker_count` becomes the backend's WorkerCount, used by the
[DeadlineFilter](design.md#deadlinefilter).

### `peak_ewma_sample(lb, s: u32, first_byte_sent_ns: u64, first_byte_received_ns: u64) -> nothing`
With `lb_policy = peak_ewma`: records one PeakEwmaRtt sample (whole ms, truncated) for backend `s`; folded into the
average at a later pick. Called by the Frontend when a forward's response headers arrive, whatever the status. See
[design.md#peakewmartt](design.md#peakewmartt).

### `pending_add(lb, s: u32, request) -> nothing` and `pending_release(lb, request) -> nothing`
With `lb_filter = deadline_provisional_rtt`, `token_bucket` or `bbr`: a pick for `s` records a PendingStart; the end of the client request
(answered, dropped or reset) releases all of its own. See [design.md#provisionalcurrentrtt](design.md#provisionalcurrentrtt).

### `admit_peak(lb, worker, request, deadline: i64 | none)`, `retry_peak(lb, worker, request, deadline, exclude, avoid)`, `scan_peak(lb, worker)`
The PeakEwmaDeadline's admission, LbRetry pick and 10 ms scan, with one wait queue and one retry list per Ingress
worker; used in place of `admit` and `scan` when `lb_filter = deadline_provisional_rtt`, `token_bucket` or `bbr` (then with the
TokenBucketFilter's or the BbrFilter's choice). See [design.md#peakewmadeadline](design.md#peakewmadeadline).

### `token_rate_sample(lb, s: u32, latency_ns: u64) -> nothing`
With `lb_filter = token_bucket`: adapts backend `s`'s TokenRate by AIMD. Called by the Frontend when an attempt's
response headers arrive, whatever the status, with the time from sending that attempt; an attempt that never gets
response headers is not reported. See [design.md#tokenbucketfilter](design.md#tokenbucketfilter).

### `bbr_on_send(lb, s: u32, now_ns: u64) -> SendTag`, `bbr_on_answer(lb, s: u32, tag: SendTag, now_ns: u64) -> nothing`, `bbr_on_loss(lb, s: u32, tag: SendTag, now_ns: u64) -> nothing`
With `lb_filter = bbr`: the BbrFilter's view of each attempt at `s`. The Frontend calls `bbr_on_send` when the
attempt's request is sent and keeps the SendTag with the attempt; then exactly one of `bbr_on_answer` (its response
headers arrived, whatever the status) or `bbr_on_loss` (it ended without them). They update `s`'s BbrState: the
DeliveryRateSample, the BbrModel and the AttemptLoss recovery. See [design.md#bbrfilter](design.md#bbrfilter),
[design.md#deliveryratesample](design.md#deliveryratesample), [design.md#bbrmodel](design.md#bbrmodel) and
[design.md#attemptloss](design.md#attemptloss).

### `bbr_choose(lb, worker, L, remaining_ns: i64 | none) -> u32 | Dropped`
With `lb_filter = bbr`, the `choose` of the PeakEwmaDeadline's admission, LbRetry pick and scan: the lowest
PeakEwmaCost among the backends whose Cwnd has room and whose PacingRate allows a send now. See
[design.md#bbradmit](design.md#bbradmit) and [design.md#longtermbw](design.md#longtermbw).

### `stats_snapshot(lb) -> list of BackendStats values`
Reads every counter, `ewma_latency_ns`, `ewma_exec_ns` and `ewma_anti_exec_ns` (acquire), in `server_id` order, for the summary.
See [design.md#loadbalancer](design.md#loadbalancer).

### `pick_log(lb) -> list of PickRecord`
A copy of the PickLog in append order, for the Ingress's `<output_path>.picks.csv`; optional (may be empty). See
[design.md#picklog](design.md#picklog).

### `forward_log(lb) -> list of ForwardRecord`
A copy of the ForwardLog in append order, for the Ingress's `<output_path>.forwards.csv`. See
[design.md#forwardlog](design.md#forwardlog).

## Uses

- [../../sampling/api.md](../../sampling/api.md): `derive_seed`, `rng_new`, `uniform_int`, `STREAM_INGRESS_LB`
- [../heartbeat_monitor/api.md](../heartbeat_monitor/api.md): `is_live`, `HeartbeatMonitor`
