# Load Balancer — API

## Parameters

From the Ingress parameters ([../api.md#program-parameters](../api.md#program-parameters)); they must match it.

| Parameter | Type / unit / constraint | Default | Meaning |
|---|---|---|---|
| `lb_policy` | `round_robin` \| `least_outstanding` \| `power_of_two` \| `ewma` | `least_outstanding` | The LbPolicy |
| `lb_filter` | `none` \| `deadline` | `none` | The DeadlineFilter ([design.md#deadlinefilter](design.md#deadlinefilter)) |
| `ewma_beta` | float, `0 <= ewma_beta <= 1` | `0.7` | Weight of the old value in each EwmaLatency update ([design.md#ewmalatency](design.md#ewmalatency)) |

## Constants

| Name | Value | Meaning |
|---|---|---|
| `EWMA_INITIAL_LATENCY_NS` | `1_000_000` (1 ms) | Every backend's EwmaLatency before its first sample. Not 0, so that the `ewma` policy still weighs `outstanding` before any response |
| `DEADLINE_FILTER_CONFIDENCE` | `0.99` | A backend passes the DeadlineFilter if the request finishes there in time with at least this probability ([design.md#deadlinefilter](design.md#deadlinefilter)) |
| `DEADLINE_FILTER_MISS` | `0.01` | `1 - DEADLINE_FILTER_CONFIDENCE`: the largest allowed late probability |
| `EXEC_NO_SAMPLE` | binary64 quiet NaN, bits `0x7FF8000000000000` | Stored in `ewma_exec_ns` until the backend's first ExecutionTime sample ([design.md#ewmaexectime](design.md#ewmaexectime)) |

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

One per `finish`, kept in the **ForwardLog**. See [design.md#forwardlog](design.md#forwardlog).

### `LoadBalancer`

The policy, `ewma_beta`, the BackendStats array, the round-robin counter, the policy's random stream and the
ForwardLog. Safe for
concurrent callers. See [design.md#loadbalancer](design.md#loadbalancer).

## Functions

### `lb_new(config: IngressConfig, monitor: HeartbeatMonitor | none) -> LoadBalancer`
`B = len(config.backends)` BackendStats, all counters zero, every `ewma_latency_ns = EWMA_INITIAL_LATENCY_NS` and
every `ewma_exec_ns = EXEC_NO_SAMPLE`.
`monitor` gives the BackendLiveness; with `none` every backend is live.
The random stream is `rng_new(derive_seed(config.seed, STREAM_INGRESS_LB, 0))`.
See [design.md#loadbalancer](design.md#loadbalancer).

### `pick(lb, remaining_ns: i64 | none) -> ForwardTicket | Dropped`
Applies the DeadlineFilter when `lb_filter = deadline` and `remaining_ns` is given, then chooses a backend by the
policy among the live (all if none is live) and eligible ones. If none is eligible it returns `Dropped`, counts it in
`filter_dropped` and changes nothing else; no `finish` follows. Otherwise it increments the chosen backend's
`outstanding` and `forwarded` and stamps the ticket with the current monotonic time; every such `pick` must be
followed by exactly one `finish` with the same ticket. See [design.md#lbpolicy](design.md#lbpolicy),
[design.md#deadlinefilter](design.md#deadlinefilter) and [design.md#forwardticket](design.md#forwardticket).

### `finish(lb, ticket: ForwardTicket, outcome: ForwardOutcome, exec_ns: u64 | none, worker_count: u32 | none) -> nothing`
Decrements `outstanding` and increments the outcome's counter. If `outcome == ok`, also folds the forward's latency
into the backend's EwmaLatency, under every policy. If `outcome` is `ok` or `rejected` and `exec_ns` is given, folds
it into the backend's EwmaExecTime ([design.md#ewmaexectime](design.md#ewmaexectime)). Then appends one ForwardRecord to the ForwardLog. See [design.md#loadbalancer](design.md#loadbalancer) and
[design.md#ewmalatency](design.md#ewmalatency). For `ok` and `rejected`, a given `worker_count` becomes the backend's WorkerCount, used by the
[DeadlineFilter](design.md#deadlinefilter).

### `stats_snapshot(lb) -> list of BackendStats values`
Reads every counter, `ewma_latency_ns` and `ewma_exec_ns` (acquire), in `server_id` order, for the summary.
See [design.md#loadbalancer](design.md#loadbalancer).

### `forward_log(lb) -> list of ForwardRecord`
A copy of the ForwardLog in append order, for the Ingress's `<output_path>.forwards.csv`. See
[design.md#forwardlog](design.md#forwardlog).

## Uses

- [../../sampling/api.md](../../sampling/api.md): `derive_seed`, `rng_new`, `uniform_int`, `STREAM_INGRESS_LB`
- [../heartbeat_monitor/api.md](../heartbeat_monitor/api.md): `is_live`, `HeartbeatMonitor`
