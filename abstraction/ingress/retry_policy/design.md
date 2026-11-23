# Retry Policy — Design

## RetryPolicy

A RetryPolicy is a pure decision function. The Frontend's LbRetryLoop
([../frontend/design.md#forwardtask](../frontend/design.md#forwardtask)) calls it; the policy never sends, waits or
counts anything itself.

```
function decide(policy, state, event):               # -> Retry | Wait
    return policy.decide_fn(policy.params, state, event)
```

- **Only while budget remains.** The Frontend calls `decide` only if `state.retries_so_far < state.max_retries`, so a
  policy never has to check the budget, and `lb_max_retries = 0` disables LB retries whatever the policy says.
- **No side effects.** `decide` reads its arguments and the policy's own immutable parameters only. It must not block
  (it runs on the ForwardTask's activity, which also relays answers) and must not touch shared state; two ForwardTasks
  may call the same policy at the same time.
- **Retry means "one more attempt now".** The new attempt goes wherever the Load Balancer's normal `pick` sends it
  (LiveSet, DeadlineFilter, LbPolicy); a policy cannot choose the backend. If that `pick` is `Dropped`, no attempt
  starts and the request keeps waiting on its current newest attempt.
- **Determinism.** `decide` may depend on time (`now_ns`, `remaining_ns`) and on the BackendView, so policies that use
  them are no more reproducible than the run's timing.

## RetryEvent

| Event | When the Frontend sends it |
|---|---|
| `AttemptEnded(outcome)` | The **newest** attempt ended without a usable answer: `outcome` is `bad_gateway` (a `502` or an upstream failure). A usable answer (`ok`, `rejected`, `bad_request`) ends the ForwardTask instead, and an older attempt's end is never an event |
| `Tick` | Every `LB_RETRY_TICK_MS` while the newest attempt is still running ([../frontend/design.md#forwardtask](../frontend/design.md#forwardtask)) |

On `AttemptEnded`, `Wait` means "give up": the Frontend relays the `502`. On `Tick`, `Wait` means "not yet".

## RetryState

Built by the Frontend right before each `decide`:

```
function retry_state(task, lb, monitor, config, now_ns, wall_now_ns):
    return RetryState(
        request_id     = task.request_id,
        retries_so_far = len(task.attempts) - 1,
        max_retries    = config.lb_max_retries,
        now_ns         = now_ns,
        first_start_ns = task.attempts[0].start_ns,
        remaining_ns   = task.deadline - wall_now_ns if task.deadline is not none else none,   # exact, saturated to i64
        attempts       = [ AttemptView(a.server_id, a.start_ns, a.running, a.outcome) for a in task.attempts ],
        backends       = [ BackendView(lb.stats[s].outstanding.load(), lb.stats[s].worker_count.load(),
                                       exec_value(lb, s), heartbeat_monitor.is_live(monitor, s)) for s in 0 ..= B-1 ])
```

The values are read without locks and may be slightly out of date, as for every Load Balancer heuristic. The state is
a copy: changing it changes nothing in the Ingress.

## RetryPolicyRegistry

```
RETRY_POLICIES = { "none": none_policy, "timeout": timeout_policy }   # name -> constructor(config) -> RetryPolicy

function retry_policy_new(name, config):
    if name not in RETRY_POLICIES: return ConfigError("lb_retry_policy must be one of " + names)
    return RETRY_POLICIES[name](config)
```

The Ingress builds its policy once, at start, from `lb_retry_policy`; Configuration validation rejects an unknown
name ([../design.md#configuration-validation](../design.md#configuration-validation)).

### `none`

```
function none_policy(config):  return RetryPolicy(decide_fn = (params, state, event) -> Wait, params = none)
```

Never retries. With `none` (the default) the Frontend does not even run its tick timer, so a ForwardTask is exactly
one attempt, as before LB retries existed.

Worked example, `none`, `lb_max_retries = 3`:

| Event | `retries_so_far` | Decision | Frontend then |
|---|---|---|---|
| `Tick` | 0 | `Wait` | (no tick timer runs with `none`) |
| `AttemptEnded(bad_gateway)` | 0 | `Wait` | relays `502` |

### timeout

Mimics the load-generator client's RetryTimer
([../../load_generator/client/design.md#retrytimer](../../load_generator/client/design.md#retrytimer)): if the
newest attempt has not answered `lb_retry_timeout_ms` after its `pick`, start the next one.

```
function timeout_policy(config):
    d = round(config.lb_retry_timeout_ms * 1_000_000)          # ns, from the Ingress parameter (default 150)
    decide_fn = (params, state, event) ->
        newest = state.attempts[last]
        Retry if event is Tick and newest.running and state.now_ns - newest.start_ns >= params.d
        else Wait
    return RetryPolicy(decide_fn, params = { d })
```

- **Like the client.** The newest attempt's answer counts and older attempts keep running (the Frontend's rule); a
  retry goes through the Load Balancer's `pick`, as a client RequestCopy does through the Ingress.
- **Errors are not retried.** On `AttemptEnded` (a `502`) it answers `Wait`, so the `502` is relayed: the client's
  RetryTimer does not retry an error either.
- **Granularity.** It is consulted on `LB_RETRY_TICK_MS = 10` ticks, so a retry starts between `d` and
  `d + LB_RETRY_TICK_MS` after the newest attempt's `pick`; the client's timer fires at exactly `RETRY`.
- **Clock origin.** `d` counts from the Load Balancer's `pick` of the attempt, not from the client's send, so it
  excludes the client-to-Ingress hop.

Worked example, `lb_retry_timeout_ms = 2000`, `lb_max_retries = 1` (times in ms from attempt 0's `pick`):

| Case | Events | Decisions | Result |
|---|---|---|---|
| Answered in time | ticks 10 ..= 790; attempt 0 answers at 800 | `Wait` at every tick | attempt 0's answer, no LbRetry |
| Slow | ticks 10 ..= 1990 `Wait`; tick at 2000 | `Retry` at 2000 (`2000 - 0 >= 2000`) | attempt 1 starts; budget used up, no more decisions; attempt 1's answer is relayed |
| Upstream failure | attempt 0 `502` at 30 | `AttemptEnded`: `Wait` | `502` relayed |
| Budget 0 | any | never consulted | one attempt |

## Adding a RetryPolicy

A new policy touches only this component, the Ingress parameter list, and the implementation's registry:

1. Add a `### <name>` subsection under [RetryPolicyRegistry](#retrypolicyregistry) with its exact `decide_fn` in
   pseudocode, every constant it uses, and a worked example (events, state, decisions).
2. If it needs parameters, add them as Ingress parameters (`lb_retry_<name>_<param>`) to
   [api.md](api.md#parameters) and [../api.md](../api.md#program-parameters), with validation in
   [../design.md](../design.md#configuration-validation); the constructor reads them from `config`.
3. Add `<name>` to the allowed values of `lb_retry_policy` here, in [../api.md](../api.md#program-parameters), in the
   root parameter table and in validation.
4. In each implementation, write one decision function and register it under `<name>`; nothing in the Frontend
   changes.

Example of a policy definition (not shipped):

```
### hedge_after(d)
function hedge_after_policy(config):
    d = config.lb_retry_hedge_after_ms * 1_000_000
    return RetryPolicy(decide_fn = (p, state, event) ->
        Retry if event is Tick and state.attempts[last].running and state.now_ns - state.attempts[last].start_ns >= d
        else Retry if event is AttemptEnded
        else Wait, params = d)
```
