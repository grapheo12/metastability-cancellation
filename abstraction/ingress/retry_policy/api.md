# Retry Policy — API

## Parameters

From the Ingress parameters ([../api.md#program-parameters](../api.md#program-parameters)); they must match it.

| Parameter | Type / unit / constraint | Default | Meaning |
|---|---|---|---|
| `lb_retry_policy` | a name in the RetryPolicyRegistry: `none` \| `timeout` | `none` | The RetryPolicy the Frontend consults ([design.md#retrypolicyregistry](design.md#retrypolicyregistry)) |
| `lb_max_retries` | integer, `>= 0` | `0` | Most LbRetries per ForwardTask (one client stream). `0`: no LB retries whatever the policy |
| `lb_retry_timeout_ms` | float, ms, `> 0` | `150` | The `timeout` policy's delay: retry once the newest attempt has run this long ([design.md#retrypolicyregistry](design.md#retrypolicyregistry)); validated always, used only by `timeout` |

## Types

### `RetryEvent`

`AttemptEnded(outcome: ForwardOutcome)` \| `Tick`. Why the policy is consulted. See
[design.md#retryevent](design.md#retryevent).

### `RetryDecision`

`Retry` \| `Wait`.

### `AttemptView`

| Field | Type | Meaning |
|---|---|---|
| `server_id` | u32 | The backend the attempt went to |
| `start_ns` | u64, ns, monotonic | When its `pick` stamped its ForwardTicket |
| `running` | boolean | `true` until its forward ended |
| `outcome` | ForwardOutcome or none | How it ended; none while running |

### `BackendView`

| Field | Type | Meaning |
|---|---|---|
| `outstanding` | i64 | The backend's forwards in progress ([../load_balancer/design.md#loadbalancer](../load_balancer/design.md#loadbalancer)) |
| `worker_count` | u64 | Its WorkerCount, 0 before its first answer |
| `ewma_exec_ns` | binary64 or none | Its EwmaExecTime |
| `live` | boolean | Its BackendLiveness (always `true` without a Heartbeat Monitor) |

### `RetryState`

| Field | Type | Meaning |
|---|---|---|
| `request_id` | u64 | The RequestId |
| `retries_so_far` | u32 | LbRetries already started for this ForwardTask (`len(attempts) - 1`) |
| `max_retries` | u32 | `lb_max_retries` |
| `now_ns` | u64, ns, monotonic | When the policy is consulted |
| `first_start_ns` | u64, ns, monotonic | `start_ns` of attempt 0 |
| `remaining_ns` | i64, ns, or none | Time left before the RequestDeadline, on the Ingress's wall clock; none without one |
| `attempts` | list of AttemptView | In attempt order; the last one is the newest |
| `backends` | list of BackendView | In `server_id` order, read when the policy is consulted |

See [design.md#retrystate](design.md#retrystate).

### `RetryPolicy`

An opaque value made by `retry_policy_new`; consulted only through `decide`.

## Functions

### `retry_policy_new(name: string, config: IngressConfig) -> RetryPolicy | ConfigError`
Looks `name` up in the RetryPolicyRegistry. `ConfigError` for an unknown name. Called once at Ingress start. See
[design.md#retrypolicyregistry](design.md#retrypolicyregistry).

### `decide(policy: RetryPolicy, state: RetryState, event: RetryEvent) -> RetryDecision`
Whether to start another attempt now. Must not block, must not change any shared state, and is only called while
`retries_so_far < max_retries`. See [design.md#retrypolicy](design.md#retrypolicy).

## Uses

- [../load_balancer/api.md](../load_balancer/api.md): `ForwardOutcome`, BackendStats values for the BackendView
- [../heartbeat_monitor/api.md](../heartbeat_monitor/api.md): `is_live` for the BackendView
