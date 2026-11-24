# Load Balancer — API

## Parameters

From the Ingress parameters ([../api.md#program-parameters](../api.md#program-parameters)); they must match it.

| Parameter | Type / unit / constraint | Default | Meaning |
|---|---|---|---|
| `lb_policy` | `round_robin` \| `least_outstanding` \| `power_of_two` \| `ewma` | `least_outstanding` | The LbPolicy |
| `lb_filter` | `none` \| `deadline_mean` \| `deadline_mean_per_worker` \| `deadline_mean_queue_plus_share` \| `deadline_mmk` | `none` | The DeadlineFilter's DeadlineStrategy, or off ([design.md#deadlinefilter](design.md#deadlinefilter)) |
| `ewma_beta` | float, `0 <= ewma_beta <= 1` | `0.7` | Weight of the old value in each EwmaLatency update ([design.md#ewmalatency](design.md#ewmalatency)) |

## Constants

| Name | Value | Meaning |
|---|---|---|
| `EWMA_INITIAL_LATENCY_NS` | `1_000_000` (1 ms) | Every backend's EwmaLatency before its first sample. Not 0, so that the `ewma` policy still weighs `outstanding` before any response |
| `DEADLINE_FILTER_CONFIDENCE` | `0.99` | A backend passes the DeadlineFilter if the request finishes there in time with at least this probability ([design.md#deadlinefilter](design.md#deadlinefilter)) |
| `DEADLINE_FILTER_MISS` | `0.01` | `1 - DEADLINE_FILTER_CONFIDENCE`: the largest allowed late probability |
| `EXEC_NO_SAMPLE` | binary64 quiet NaN, bits `0x7FF8000000000000` | Stored in `ewma_exec_ns` and `ewma_anti_exec_ns` until their first sample ([design.md#ewmaexectime](design.md#ewmaexectime), [design.md#ewmaantiservicetime](design.md#ewmaantiservicetime)) |

## Types

### `LbPolicy`

`round_robin` \| `least_outstanding` \| `power_of_two` \| `ewma`, from the Ingress parameter `lb_policy`
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

### `pick(lb, remaining_ns: i64 | none, attempt: u32, record: boolean = true) -> ForwardTicket | Dropped`
Applies the DeadlineFilter when `lb_filter` is not `none` and `remaining_ns` is given, then chooses a backend by the
policy among the live (all if none is live) and eligible ones. If none is eligible it returns `Dropped` and changes
nothing; the caller decides what follows (a first attempt waits in the FilterWaitQueue, a refused LbRetry is skipped).
Otherwise it increments the chosen backend's `outstanding` and `forwarded` and stamps the ticket with the current
monotonic time; every such `pick` must be followed by exactly one `finish` with the same ticket. `record = false`
(a FilterWaitQueue re-check) writes no PickRecord. See [design.md#lbpolicy](design.md#lbpolicy),
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

### `stats_snapshot(lb) -> list of BackendStats values`
Reads every counter, `ewma_latency_ns`, `ewma_exec_ns` and `ewma_anti_exec_ns` (acquire), in `server_id` order, for the summary.
See [design.md#loadbalancer](design.md#loadbalancer).

### `pick_log(lb) -> list of PickRecord`
A copy of the PickLog in append order, for the Ingress's `<output_path>.picks.csv`. See
[design.md#picklog](design.md#picklog).

### `forward_log(lb) -> list of ForwardRecord`
A copy of the ForwardLog in append order, for the Ingress's `<output_path>.forwards.csv`. See
[design.md#forwardlog](design.md#forwardlog).

## Uses

- [../../sampling/api.md](../../sampling/api.md): `derive_seed`, `rng_new`, `uniform_int`, `STREAM_INGRESS_LB`
- [../heartbeat_monitor/api.md](../heartbeat_monitor/api.md): `is_live`, `HeartbeatMonitor`
