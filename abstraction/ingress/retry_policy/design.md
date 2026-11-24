# Retry Policy — Design

## RetryPolicy

A RetryPolicy is a pure decision function. The Frontend's LbRetryLoop
([../frontend/design.md#forwardtask](../frontend/design.md#forwardtask)) calls it; the policy never sends, waits or
counts anything itself.

```
function decide(policy, state, event):               # -> Retry | RetryElsewhere | Wait
    return policy.decide_fn(policy.params, state, event)
```

- **Only while budget remains.** The Frontend calls `decide` only if `state.retries_so_far < state.max_retries`, so a
  policy never has to check the budget, and `lb_max_retries = 0` disables LB retries whatever the policy says.
- **No side effects.** `decide` reads its arguments, the policy's own immutable parameters and, for a policy that
  observes ([RetryObservation](#retryobservation)), a value its own tracker published. It must not block (it runs on
  the ForwardTask's activity, which also relays answers) and must not change any shared state; two ForwardTasks may
  call the same policy at the same time.
- **Retry means "one more attempt now".** The new attempt goes wherever the Load Balancer's normal `pick` sends it
  (LiveSet, DeadlineFilter, LbPolicy); a policy cannot choose the backend. **RetryElsewhere** is the same, except
  that the pick excludes the newest attempt's backend (ExcludedBackend,
  [../load_balancer/design.md#lbpolicy](../load_balancer/design.md#lbpolicy)). If that `pick` is `Dropped`, no
  attempt starts, the request keeps waiting on its current newest attempt, and an observing policy is told
  (`RetryRefused`).
- **Determinism.** `decide` may depend on time (`now_ns`, `remaining_ns`) and on the BackendView, so policies that use
  them are no more reproducible than the run's timing.

## RetryEvent

| Event | When the Frontend sends it |
|---|---|
| `AttemptEnded(outcome)` | The **newest** attempt ended without a usable answer: `outcome` is `bad_gateway` (a `502` or an upstream failure). A usable answer (`ok`, `rejected`, `bad_request`) ends the ForwardTask instead, and an older attempt's end is never an event |
| `Tick` | Every `LB_RETRY_TICK_MS` while the newest attempt is still running ([../frontend/design.md#forwardtask](../frontend/design.md#forwardtask)) |

On `AttemptEnded`, `Wait` means "give up": the Frontend relays the `502`. On `Tick`, `Wait` means "not yet".

## RetryObservation

A policy may also keep its own state about every attempt in the Ingress, not only the ForwardTask that consults it.
Such a policy implements two optional operations; a policy without them (`none`, `timeout`) is never sent anything.

| Observation | Sent by (Frontend) | When | Payload |
|---|---|---|---|
| `AttemptSent(seq, server_id, queue_at_send)` | `start_attempt` | right after its `pick` succeeded, before the forward starts; for attempt 0 and every LbRetry | the ForwardTicket's TicketSeq, backend and QueueAtSend ([../load_balancer/design.md#forwardticket](../load_balancer/design.md#forwardticket)) |
| `AttemptFinished(seq, server_id)` | `attempt_activity` | right after `finish`, for every outcome (`reset` and `bad_gateway` included) | the ticket's TicketSeq and backend |
| `RetryRefused(seq)` | LbRetryLoop | a `Retry` or `RetryElsewhere` whose `pick` was `Dropped` | the newest attempt's TicketSeq |
| `EwmaChanged(exec[0..B-1], anti[0..B-1])` | `attempt_activity` | right after `AttemptFinished`, only when `finish` returned `sampled` | every backend's EwmaExecTime and EwmaAntiServiceTime (`exec_value`, `anti_value`; none before a sample), read in `server_id` order |

```
procedure observe(policy, observation):     # optional; a no-op for a policy without it
procedure stop(policy):                     # optional; called once at Ingress Shutdown, after the listener closed
procedure attach(policy, is_live):          # optional; called once at Ingress start, before any request:
                                            # is_live(s) is the Heartbeat Monitor's BackendLiveness (always true without one)
```

- **Order.** Observations from one attempt arrive in the order above (sent, then finished, then EWMAs), because one
  activity sends them in that order. Observations from different attempts interleave in any order.
- **Must not block on the policy's logic.** `observe` only hands the observation over (for example on a bounded
  channel); the work happens on the policy's own activity. After `stop`, `observe` discards what it is given and
  returns at once, since attempts may still be finishing during Shutdown.

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
        attempts       = [ AttemptView(a.server_id, a.start_ns, a.running, a.outcome, a.ticket.seq)
                           for a in task.attempts ],
        backends       = [ BackendView(lb.stats[s].outstanding.load(), lb.stats[s].worker_count.load(),
                                       exec_value(lb, s), anti_value(lb, s), heartbeat_monitor.is_live(monitor, s))
                           for s in 0 ..= B-1 ])
```

The values are read without locks and may be slightly out of date, as for every Load Balancer heuristic. The state is
a copy: changing it changes nothing in the Ingress.

## RetryPolicyRegistry

```
RETRY_POLICIES = { "none": none_policy, "timeout": timeout_policy, "anxious": anxious_policy }   # name -> constructor(config)

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

### anxious

**AnxiousRetry**: retry an attempt when moving it is expected to finish at least a fraction `alpha` sooner than
leaving it where it is, **and** where it is it is expected to miss its RequestDeadline (CurrentExpFinish greater than
the time left). The policy keeps its own **AnxiousTracker**, an activity that follows every attempt in the
Ingress through [RetryObservations](#retryobservation) and, every `ANXIOUS_TICK_MS`, marks the anxious ones.

| Name | Value | Meaning |
|---|---|---|
| `ANXIOUS_TICK_MS` | `10` | Period of the tracker's evaluation |
| `ANXIOUS_BUFFER` | `4096` | Capacity of the observation channel |
| `alpha` | `lb_retry_anxiety_ratio` (default `0.3`) | The anxiety ratio |

```
function anxious_policy(config):
    p = { alpha: config.lb_retry_anxiety_ratio, B: len(config.backends), is_live: none,     # set by attach
          inbox: bounded FIFO channel of ANXIOUS_BUFFER observations, stopped: atomic boolean = false,
          anxious: atomic reference to an immutable map u64 -> binary64 = {} } # TicketSeq -> CurrentExpFinish (ns)
    spawn anxious_tracker(p)                                                   # its own coroutine or thread
    return RetryPolicy(decide_fn = anxious_decide, observe_fn = anxious_observe, stop_fn = anxious_stop,
                       attach_fn = (p, is_live) -> p.is_live = is_live, params = p)

procedure anxious_observe(p, obs):
    if p.stopped.load(): return
    send obs on p.inbox, or return as soon as p.stopped is set            # blocks only while the channel is full

procedure anxious_stop(p):
    p.stopped.store(true)                                                  # the tracker ends, observe discards

function anxious_decide(p, state, event):
    newest = state.attempts[last]
    if event is not Tick or not newest.running: return Wait               # AttemptEnded: the 502 is relayed
    c = p.anxious.load().get(newest.ticket_seq)                            # CurrentExpFinish, or none
    if c is none or state.remaining_ns is none: return Wait               # no deadline: never late, never retried
    return RetryElsewhere if c > float(state.remaining_ns) else Wait      # ExpectedLate: strict >

procedure anxious_tracker(p):
    entries = map TicketSeq -> { server_id: u32, queue: u64 }              # attempts in flight
    count   = array[B] of u64 = 0                                          # entries per backend
    exec    = array[B] of (binary64 or none) = none                        # EwmaExecTime, as last observed
    anti    = array[B] of (binary64 or none) = none                        # EwmaAntiServiceTime, as last observed
    live    = array[B] of boolean = true                                   # BackendLiveness, as last read
    dirty   = false                                                        # a stat changed since the last evaluation
    every ANXIOUS_TICK_MS, and on each observation, until p.stopped:
        on AttemptSent(seq, s, q):
            entries[seq] = { s, q }; count[s] += 1; dirty = true
        on AttemptFinished(seq, s):
            if seq in entries: remove entries[seq]; count[s] -= 1
            for each e in entries with e.server_id == s:
                e.queue = e.queue - 1 if e.queue > 0 else 0                # one fewer request ahead of it
            dirty = true
        on EwmaChanged(x, a):
            exec = x; anti = a; dirty = true
        on RetryRefused(seq):                                              # not a stat change: no evaluation
            p.anxious.store(p.anxious.load() without key seq)              # asked no more until the next evaluation
        on the tick:
            now_live = [ p.is_live(s) if p.is_live is not none else true for s in 0 ..= B-1 ]
            if now_live != live: live = now_live; dirty = true
            if dirty:                                                      # AnxiousEvaluation
                p.anxious.store(anxious_set(p, entries, count, exec, anti, live))
                dirty = false

function anxious_set(p, entries, count, exec, anti, live):                 # binary64, each step rounded, no fused ops
    out = {}                                                               # TicketSeq -> CurrentExpFinish
    for each (seq, e) in entries:
        b = e.server_id
        L = [ c for c in 0 ..= B-1 if c != b and live[c] ]                 # where a RetryElsewhere can go
        if L is empty: continue
        if exec[b] is none or anti[b] is none or exec[b] == 0: continue
        current = float(e.queue + 1) * exec[b]                             # CurrentExpFinish
        others  = sum(count[c] for c in L)                                 # in flight on the live other backends
        retry   = (float(others) / float(len(L)) + 1) * anti[b]            # RetryExpFinish
        if (current - retry) / current >= p.alpha: out[seq] = current
    return out
```

- **Which queue.** `queue` starts at the attempt's QueueAtSend, the `n` its `pick` saw, and loses one for every attempt
  on the same backend that finishes after it was sent (whatever its outcome), never below 0. `count[b]` is the number
  of attempts the tracker knows to be in flight on `b`, older attempts of an LbRetry included: they still occupy the
  backend.
- **Which retry estimate.** `others / len(L)` is the average number in flight on one of the **live** backends other
  than `b`, plus one for the retry itself, at the service time a request sees away from `b` (EwmaAntiServiceTime).
  A dead backend is no target: its stuck attempts and stale numbers would make moving look cheap. The decision is
  `RetryElsewhere`, so the retry never lands on `b` again; among the others it goes wherever `pick` sends it, and it
  may be refused (DeadlineFilter, or no live other backend at pick time).
- **ExpectedLate is checked in `decide`, not in the tracker.** The tracker publishes CurrentExpFinish with each
  anxious TicketSeq; `decide` compares it with `remaining_ns`, which the Frontend computes fresh at every
  `LB_RETRY_TICK_MS` tick. So an attempt that becomes late while nothing else changes is still retried, without an
  AnxiousEvaluation. CurrentExpFinish itself is as of the last evaluation. An attempt that is expected to finish in
  time where it is is never retried, however much faster moving looks. A request without a RequestDeadline is never
  retried. `remaining_ns <= 0` (already late) retries whenever `c > 0`. A tie (`c == remaining_ns`) waits.
- **AnxiousEvaluation only after a change.** The set is recomputed at a tick only if an `AttemptSent`,
  `AttemptFinished` or `EwmaChanged` arrived, or the liveness read at the tick differs from the last one, since the
  previous evaluation; otherwise the tick does nothing. A `RetryRefused(seq)` removes `seq` from the published map
  without an evaluation, so a ForwardTask whose retry was refused is not asked again until the stats change.
- **Not the newest.** The set holds every anxious attempt, but `decide` only retries a ForwardTask whose **newest**
  attempt is in it and is ExpectedLate, while budget remains. Once an LbRetry starts, its own `AttemptSent` adds it; the older attempt
  stays in `entries` until it finishes.
- **Ties and races.**
  - A ratio exactly equal to `alpha` retries (`>=`).
  - Observations are applied in the order the tracker receives them. If an attempt's `AttemptSent` is received
    after the `AttemptFinished` of an attempt its `pick` already counted, that finish does not lower its `queue`. The
    estimate is a heuristic and the race is accepted.
  - `decide` reads the set published at the last tick, so it is up to `ANXIOUS_TICK_MS` old, and a TicketSeq whose
    attempt has since finished is harmless (`decide` checks `newest.running`).
- **Edge cases.** No live backend other than `b` (including `B = 1`), a backend with no EwmaExecTime or EwmaAntiServiceTime yet, or an EwmaExecTime of 0: never
  anxious. `alpha = 0` retries as soon as moving is expected to be no slower. `alpha > 1` never retries (the ratio
  is at most 1).
- **A request first in line on a stopped backend** (`queue = 0`, no finishes) is anxious only if
  `exec[b] × (1 - alpha) >= retry`: the policy estimates from queue position, not from elapsed time.

Worked example, `alpha = 0.3`, times in ms:

| Case | `B` | entry (`b`, `queue`) | `exec[b]` | `anti[b]` | `count` | current | retry | ratio | Anxious |
|---|---|---|---|---|---|---|---|---|---|
| Long queue | 2 | (0, 4) | 80 | 80 | [6, 2] | `5 × 80 = 400` | `(2/1 + 1) × 80 = 240` | `160/400 = 0.4` | yes |
| Exactly alpha | 2 | (0, 4) | 80 | 70 | [5, 3] | `5 × 80 = 400` | `(3/1 + 1) × 70 = 280` | `120/400 = 0.3` | yes (`>=`) |
| Short queue | 2 | (0, 1) | 80 | 80 | [2, 2] | `2 × 80 = 160` | `(2/1 + 1) × 80 = 240` | `-80/160 = -0.5` | no |
| Three backends | 3 | (0, 4) | 100 | 100 | [5, 3, 1] | `5 × 100 = 500` | `(4/2 + 1) × 100 = 300` | `200/500 = 0.4` | yes |
| No sample | 2 | (1, 9) | none | 80 | [1, 10] | — | — | — | no |
| One backend | 1 | (0, 9) | 80 | none | [10] | — | — | — | no |
| Other backend dead | 2 | (0, 4) | 80 | 80 | [6, 2], backend 1 not live | — | — | — | no (`L` empty) |
| Three, one dead | 3 | (0, 4) | 100 | 100 | [5, 3, 1], backend 2 not live | `5 × 100 = 500` | `(3/1 + 1) × 100 = 400` | `100/500 = 0.2` | no |

In every row above, the other backends are live unless the row says otherwise. "Anxious" means the TicketSeq is in the
published map with `current` as its CurrentExpFinish; `decide` then also needs ExpectedLate:

| `c` (CurrentExpFinish, ms) | `remaining_ns` (ms) | Decision on a Tick (newest running) | Why |
|---|---|---|---|
| 400 | 150 | `RetryElsewhere` | `400 > 150` |
| 400 | 400 | `Wait` | tie: strict `>` |
| 400 | 4000 | `Wait` | expected in time |
| 400 | -20 | `RetryElsewhere` | already past the deadline |
| 400 | none | `Wait` | no RequestDeadline |
| not in the map | 10 | `Wait` | not anxious |

When the set is recomputed (ticks at 10, 20, 30, … ms):

| Between ticks | At the tick | Evaluation | Published set |
|---|---|---|---|
| `AttemptSent(7, 0, 4)` | liveness unchanged | yes | recomputed: `{7}` if anxious |
| nothing | liveness unchanged | no | unchanged |
| `RetryRefused(7)` | liveness unchanged | no | `7` removed at once, not added back |
| nothing | backend 1 turns not live | yes | recomputed (7 may return) |
| `AttemptFinished(3, 1)` | — | yes | recomputed |

Queue bookkeeping, backend 0 holding TicketSeqs 9 (`queue 1`), 10 (`queue 2`), 11 (`queue 3`):

| Observation | 9 | 10 | 11 | `count[0]` |
|---|---|---|---|---|
| start | 1 | 2 | 3 | 3 |
| `AttemptFinished(9, 0)` | removed | 1 | 2 | 2 |
| `AttemptFinished(10, 0)` | — | removed | 1 | 1 |
| `AttemptFinished(42, 0)` (not tracked) | — | — | 0 | 1 |
| `AttemptFinished(43, 0)` (not tracked) | — | — | 0 (floor) | 1 |

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

## Design specific to Go

Implementation: `implementation/go` (module `testbed`, Go 1.21). Build and test with `CGO_ENABLED=0 go build ./...` /
`CGO_ENABLED=0 go test ./...`.

### Go choices

- File `internal/ingress/retrypolicy.go` (package `ingress`, next to the Frontend that consults it).
- `RetryPolicy` is an interface with one method, `Decide(RetryState, RetryEvent) RetryDecision`; `RetryPolicyFunc`
  adapts a plain function, so a policy is usually one function literal.
- The RetryPolicyRegistry is the package map `retryPolicies` (name -> `func(Config) RetryPolicy`), holding only
  `none`. `RegisterRetryPolicy(name, ctor)` adds one; call it before configuring an Ingress (an `init` function in a
  new file is the intended place, so adding a policy touches no existing file except the allowed-values text).
  `NewRetryPolicy(name, cfg)` is `retry_policy_new`; Ingress validation checks the name against the same map.
- Times in RetryState and AttemptView are `time.Time` values (they carry Go's monotonic reading) instead of ns
  integers; `RemainingNs`/`HasDeadline` and `EwmaExecNs`/`HasEwmaExec` stand for "or none".
- `(*LoadBalancer).backendViews()` builds the BackendView list from `Outstanding`, `WorkerCount`, `execValue` and the
  monitor's `IsLive` (nil-safe: always live without a monitor).
- Tests: `retrypolicy_test.go` (`TestRetryPolicyRegistryNone`, `TestRegisterRetryPolicy`, `TestValidateLbRetry`).
- `timeout` lives in its own file, `internal/ingress/retrypolicy_timeout.go`, registered from `init` with `RegisterRetryPolicy` as the recipe describes; it compares `s.Now.Sub(newest.Start) >= time.Duration(cfg.LbRetryTimeoutNs)`. `TestTimeoutPolicyWorkedExample` checks the decisions; `TestLbRetryTimeoutPolicy` (in `lbretry_test.go`) runs it end to end with a 50 ms delay.
- RetryObservation is a struct `{Kind, Seq, ServerID, QueueAtSend, Exec, Anti, HasExec, HasAnti}` with `Kind` one of `ObsAttemptSent`, `ObsAttemptFinished`, `ObsEwmaChanged`. `observe` and `stop` are the optional interfaces `RetryObserver` (`Observe`) and `RetryStopper` (`Stop`); the Frontend calls them through `observePolicy`/`stopPolicy`, type assertions that are no-ops for `none` and `timeout`. `AttemptView.TicketSeq` and `BackendView.EwmaAntiNs`/`HasEwmaAnti` (from `antiValue`) are the new view fields.
- `anxious` lives in `internal/ingress/retrypolicy_anxious.go`, registered from `init`. `anxiousPolicy` holds `alpha`, `B`, the inbox (a buffered `chan RetryObservation` of `AnxiousBuffer`), a `stopped` channel closed once by `Stop` (`sync.Once`), a `done` channel the tracker closes when it returns (so `Stop` waits for it), and the published set as `atomic.Pointer[map[uint64]struct{}]`, replaced whole at every tick and never mutated after publication. The constructor starts the tracker goroutine (`run`), a `select` over `stopped`, the inbox and a `time.Ticker` of `AnxiousTick`. `Observe` returns at once if `stopped` is closed, else `select`s between sending and `stopped`.
- `anxiousTracker` keeps `entries` per backend (`[]map[uint64]*anxiousEntry`), so `count[b]` is `len(entries[b])` and an `AttemptFinished` walks only its own backend. `anxiousSet` computes `retry` once per backend and `current` per entry, wrapping each step in `float64(...)` so Go cannot fuse a multiply and an add (as in `ewmaNext`). Map iteration order does not matter: the result is a set.
- Tests: `retrypolicy_anxious_test.go`: `TestAnxiousWorkedExample` (the table, plus `alpha = 0` and `alpha > 1`), `TestAnxiousQueueBookkeeping` (the bookkeeping table), `TestAnxiousPolicyDecide` (observations through the goroutine, Tick/AttemptEnded/not running, `Observe` after `Stop` never blocks), `TestAnxiousRegistryAndConfig` (default 0.3, validation).
- v0.4.0: `RetryElsewhere` is a third `RetryDecision` value; `ObsRetryRefused` a fourth observation kind; `attach` is the optional interface `RetryAttacher` (`Attach(func(uint32) bool)`), called through `attachPolicy`. In `anxious`, `Attach` stores the function in an `atomic.Pointer` (the tracker goroutine reads it at every tick; nil means all live). The tracker keeps `live []bool` and `dirty bool`: `apply` sets `dirty` for `AttemptSent`, `AttemptFinished` and `EwmaChanged`; `readLiveness` sets it when a backend's liveness differs; a tick evaluates only when `dirty`, then clears it. `RetryRefused` is handled in `run` by `unpublish(seq)`, which stores a copy of the published map without `seq` (published maps are never mutated) and leaves `dirty` alone. `anxiousSet` computes `others` and `len(L)` per backend over the live backends other than it. `TestAnxiousWorkedExample` covers the dead-backend rows; `TestAnxiousEvaluateOnlyOnChange` covers the dirty flag, the refusal staying out across ticks, the return after a new observation, and a liveness change through `Attach`.

### Traceability

Generated from the `// Design:` tags in the code by `implementation/spec_tools/tracegen.py` (see `AGENTS.md`). Each link points at the first code line after the tag.
Regenerate from the repository root with `python3 implementation/spec_tools/tracegen.py --lang Go --ext .go --comment // --write`.

<!-- trace:begin -->
| Design subsection | Implementation (file:line) |
|---|---|
| [RetryPolicy](#retrypolicy) | [retrypolicy.go:88](../../../implementation/go/internal/ingress/retrypolicy.go#L88) |
| [RetryEvent](#retryevent) | [retrypolicy.go:26](../../../implementation/go/internal/ingress/retrypolicy.go#L26) |
| [RetryObservation](#retryobservation) | [retrypolicy.go:111](../../../implementation/go/internal/ingress/retrypolicy.go#L111), [retrypolicy.go:125](../../../implementation/go/internal/ingress/retrypolicy.go#L125), [retrypolicy.go:133](../../../implementation/go/internal/ingress/retrypolicy.go#L133), [retrypolicy.go:147](../../../implementation/go/internal/ingress/retrypolicy.go#L147) |
| [RetryState](#retrystate) | [frontend.go:285](../../../implementation/go/internal/ingress/frontend.go#L285), [retrypolicy.go:71](../../../implementation/go/internal/ingress/retrypolicy.go#L71), [retrypolicy.go:219](../../../implementation/go/internal/ingress/retrypolicy.go#L219) |
| [RetryPolicyRegistry](#retrypolicyregistry) | [lbretry_test.go:274](../../../implementation/go/internal/ingress/lbretry_test.go#L274), [retrypolicy.go:180](../../../implementation/go/internal/ingress/retrypolicy.go#L180), [retrypolicy.go:182](../../../implementation/go/internal/ingress/retrypolicy.go#L182), [retrypolicy.go:208](../../../implementation/go/internal/ingress/retrypolicy.go#L208), [retrypolicy_anxious.go:25](../../../implementation/go/internal/ingress/retrypolicy_anxious.go#L25), [retrypolicy_anxious.go:35](../../../implementation/go/internal/ingress/retrypolicy_anxious.go#L35), [retrypolicy_anxious.go:49](../../../implementation/go/internal/ingress/retrypolicy_anxious.go#L49), [retrypolicy_anxious.go:66](../../../implementation/go/internal/ingress/retrypolicy_anxious.go#L66), [retrypolicy_anxious.go:81](../../../implementation/go/internal/ingress/retrypolicy_anxious.go#L81), [retrypolicy_anxious.go:96](../../../implementation/go/internal/ingress/retrypolicy_anxious.go#L96), [retrypolicy_anxious.go:106](../../../implementation/go/internal/ingress/retrypolicy_anxious.go#L106), [retrypolicy_anxious.go:137](../../../implementation/go/internal/ingress/retrypolicy_anxious.go#L137), [retrypolicy_anxious.go:160](../../../implementation/go/internal/ingress/retrypolicy_anxious.go#L160), [retrypolicy_anxious.go:184](../../../implementation/go/internal/ingress/retrypolicy_anxious.go#L184), [retrypolicy_anxious.go:216](../../../implementation/go/internal/ingress/retrypolicy_anxious.go#L216), [retrypolicy_anxious.go:232](../../../implementation/go/internal/ingress/retrypolicy_anxious.go#L232), [retrypolicy_anxious_test.go:36](../../../implementation/go/internal/ingress/retrypolicy_anxious_test.go#L36), [retrypolicy_anxious_test.go:81](../../../implementation/go/internal/ingress/retrypolicy_anxious_test.go#L81), [retrypolicy_anxious_test.go:118](../../../implementation/go/internal/ingress/retrypolicy_anxious_test.go#L118), [retrypolicy_anxious_test.go:166](../../../implementation/go/internal/ingress/retrypolicy_anxious_test.go#L166), [retrypolicy_anxious_test.go:198](../../../implementation/go/internal/ingress/retrypolicy_anxious_test.go#L198), [retrypolicy_test.go:13](../../../implementation/go/internal/ingress/retrypolicy_test.go#L13), [retrypolicy_test.go:65](../../../implementation/go/internal/ingress/retrypolicy_test.go#L65), [retrypolicy_timeout.go:14](../../../implementation/go/internal/ingress/retrypolicy_timeout.go#L14) |
| [Adding a RetryPolicy](#adding-a-retrypolicy) | [retrypolicy.go:191](../../../implementation/go/internal/ingress/retrypolicy.go#L191), [retrypolicy_test.go:31](../../../implementation/go/internal/ingress/retrypolicy_test.go#L31) |
<!-- trace:end -->
