# Service Scenario — Design

## ServiceScenario

The scenario is the server parameter `service_scenario`, a JSON object:

```
{"states":   {"<name>": {"service_us": <integer >= 1>} | {"stall": true}, ...},       # at least one state
 "schedule": [[<at_ms>, "<name>"], ...],                                               # exactly one of
 "markov":   {"initial": "<name>", "step_ms": <integer >= 1>,                          #   schedule, markov
              "p": {"<from>": {"<to>": <number >= 0>, ...}, ...}},
 "stream":   <integer 0 ..= 2^32-1>}                                                   # optional, default 0
```

```
MARKOV_ROW_TOLERANCE = 1e-9
MAX_MS = floor((2^64 - 1) / 1_000_000)                  # the largest ms that still fits in u64 ns

function scenario_new(json, seed):                     # -> ServiceScenario | ConfigError
    v = parse json as a JSON object                     else error "service_scenario: not a JSON object"
    every key of v is one of states, schedule, markov, stream   else error "service_scenario: unknown key <key>"
    states = v.states: a non-empty object               else error "service_scenario: states must be a non-empty object"
    for (name, s) in states:
        name matches [A-Za-z0-9_-]+                     else error "service_scenario: bad state name <name>"
        s is exactly {"service_us": n} with n an integer in 1 ..= 2^64-1,
          or exactly {"stall": true}                    else error "service_scenario: state <name> needs service_us >= 1 or stall: true"
    exactly one of v.schedule and v.markov is present   else error "service_scenario: give exactly one of schedule and markov"
    stream = v.stream if present else 0; an integer in 0 ..= 2^32-1   else error "service_scenario: bad stream"
    sc = ServiceScenario(states, stream, armed_at_ns = none, lock = new mutex, changes = [])
    if v.schedule is present:
        a non-empty list of [at_ms, name] pairs, at_ms an integer in 0 ..= MAX_MS, name in states,
          the first at_ms is 0, at_ms strictly increasing   else error "service_scenario: bad schedule"
        sc.schedule = [(at_ms * 1_000_000, states[name]) for each pair]      # at_ns, u64
        sc.changes  = the schedule entries whose state differs from the previous entry's (the first always)
    else:
        m = v.markov: exactly the keys initial, step_ms, p   else error "service_scenario: bad markov"
        m.initial in states; m.step_ms an integer in 1 ..= MAX_MS             else error "service_scenario: bad markov"
        m.p has exactly one row per state; every row maps names in states to numbers >= 0,
          and |sum of the row - 1| <= MARKOV_ROW_TOLERANCE                    else error "service_scenario: bad markov row <from>"
        sc.markov = { step_ns: m.step_ms * 1_000_000,
                      rows: for each from: [(to, p) for to in the row's names in bytewise order] }
        sc.rng = sampling.rng_new(sampling.derive_seed(seed, STREAM_SERVER_SCENARIO, stream))
        sc.k = 0; sc.cur = states[m.initial]; sc.changes = [(0, sc.cur)]
    return sc
```

- **One source of truth for the first state.** The first state is the schedule's first entry (at 0), or `initial`
  of a MarkovSchedule. A ScenarioSchedule has no `initial`.
- **Names, not indices.** States are referred to by name everywhere, including the ScenarioLog. A row's targets are
  taken in bytewise name order, so the order of keys in the JSON never matters.
- **Only with `sleep_constant`.** The server's configuration validation rejects a scenario with any other workload
  ([../design.md#configuration-validation](../design.md#configuration-validation)).

## ScenarioClock

```
function scenario_arm(sc, armed_at_ns, armed_at_unix_ns):     # called by the WorkQueue at its first push
    with sc.lock:
        if sc.armed_at_ns is none:
            sc.armed_at_ns = armed_at_ns; sc.armed_at_unix_ns = armed_at_unix_ns
            notify the ScenarioLog writer

function clock(sc, now_ns):                                   # caller holds sc.lock -> ScenarioClock ns, u64
    if sc.armed_at_ns is none or now_ns <= sc.armed_at_ns: return 0
    return now_ns - sc.armed_at_ns
```

The clock starts at the first admitted `/ping`, the instant that arms the StallGate and the WorkerFreeze
([../worker_pool/design.md#stallgate](../worker_pool/design.md#stallgate)), so `at_ms` and `freeze_after_ms` count
from the same moment. Before it starts, the server is in its first state and nothing changes.

## ScenarioSchedule

```
function schedule_state(sc, t):        # t: ScenarioClock ns
    i = the largest index with sc.schedule[i].at_ns <= t    # binary search; sc.schedule[0].at_ns = 0
    return sc.schedule[i].state

function schedule_next(sc, t):         # -> ScenarioClock ns, or none
    return the at_ns of the first entry with at_ns > t, or none if there is none
```

After the last entry the server stays in its state for good; a StalledState there is a permanent stall.

## MarkovSchedule

The chain moves at step boundaries `k × step_ns` (`k = 1, 2, …`); in `[k × step_ns, (k+1) × step_ns)` the server is
in state `s_k`, with `s_0 = initial`.

```
function markov_advance(sc, k):        # caller holds sc.lock; makes the chain reach step k (k > sc.k)
    while sc.k < k:
        u = sampling.next_unit(sc.rng)                 # exactly one draw per step, in step order
        c = 0; next = none
        for (to, prob) in sc.markov.rows[sc.cur.name]:  # targets in bytewise name order
            c = c + prob                                # binary64, left to right
            if u < c: next = to; break
        if next is none:                                # u >= the row's total, only by rounding
            next = the last target of the row with prob > 0
        sc.k = sc.k + 1
        if states[next] != sc.cur: sc.changes.append((sc.k * sc.markov.step_ns, states[next]))
        sc.cur = states[next]

function markov_state(sc, t):
    k = t div sc.markov.step_ns                         # u64 floor division
    if k > sc.k: markov_advance(sc, k)
    return the state of the last entry of sc.changes with time <= t   # binary search; also answers a t older
                                                                       # than the chain's newest step

function markov_next(sc, t):
    return (t div sc.markov.step_ns + 1) * sc.markov.step_ns           # the next step boundary
```

## state_at and next_change_at

```
function state_at(sc, now_ns):         # any thread
    with sc.lock:
        t = clock(sc, now_ns)
        return schedule_state(sc, t) if sc.schedule is not none else markov_state(sc, t)

function next_change_at(sc, now_ns):   # -> monotonic ns, or none
    with sc.lock:
        if sc.armed_at_ns is none: return none          # the clock has not started
        t = clock(sc, now_ns)
        n = schedule_next(sc, t) if sc.schedule is not none else markov_next(sc, t)
        return none if n is none else sc.armed_at_ns + n
```

`next_change_at` is when the state *can* change: a schedule entry that repeats the current state, or a Markov step
that stays, changes nothing, and a waiter woken then simply waits again.

## StalledState

A ScenarioState with `stall: true`. While the server is in one, it behaves as during a WorkerFreeze
([../worker_pool/design.md#workerfreeze](../worker_pool/design.md#workerfreeze)), but only until the state is left:

| Who | While stalled | When the stall ends |
|---|---|---|
| Idle workers (`queue_pop`) | hand out nothing; wait until `next_change_at` (or a push, a close) and check again | pop in the discipline's order |
| Running requests | held at their next check (`freeze_wait`), as for a WorkerFreeze | carry on; a QuantumSleep's deadline is fixed when it starts, so a request whose service time ran out during the hold is answered at once |
| HeartbeatSender | writes nothing (`freeze_wait` before each line) | writes the next line at once, `seq` continuing |
| Connections, `/cancel`, RST flags, QueueLengthLog | carry on; the WorkQueue keeps growing | — |

- **Service time.** A request's service time is fixed when a worker pops it, from the state at that moment
  ([../worker_pool/design.md#workqueue](../worker_pool/design.md#workqueue)). Popping never happens in a StalledState,
  so every popped request has a ConstantServiceTime.
- **ExecutionTime.** A request held by a stall reports the hold in its `exec_ns` (it runs from the pop to the
  answer, [../ping_handler/design.md#execreport](../ping_handler/design.md#execreport)).
- **With a WorkerFreeze.** The two combine: the server is frozen if either says so, and the WorkerFreeze never ends.

## ScenarioLog

```
procedure scenario_logger(sc, log):                 # own activity; only with scenario_log_path set
    write "t_ns,unix_ns,state\n"
    wait until sc.armed_at_ns is set or the server shuts down
    written = 0
    loop until the server shuts down:
        with sc.lock:
            t = clock(sc, monotonic_now_ns())
            if sc.markov is not none and t div sc.markov.step_ns > sc.k: markov_advance(sc, t div sc.markov.step_ns)
            rows = [(tc, s) for (tc, s) in sc.changes[written:] if tc <= t]; written = written + len(rows)
            at = sc.armed_at_unix_ns
        for (tc, s) in rows: write decimal(tc) + "," + decimal(at + tc) + "," + s.name + "\n"; flush
        n = next_change_at(sc, monotonic_now_ns())
        if n is none: wait until the server shuts down
        else: sleep until n, ending early if the server shuts down
```

- **Rows** are changes of state only: the first state at `t_ns = 0`, then one row per change, at the exact ScenarioClock
  time of the change (a schedule entry, or a step boundary), not the time the writer woke.
- **`unix_ns`** is the wall-clock arm time plus `t_ns`; the arm is the server's third wall-clock read, after
  ProcessStart and the ExecReport's StartSlack ([../design.md#processstart](../design.md#processstart)).
- The writer advances a MarkovSchedule itself at each boundary, so the log is written on time even when no worker
  asks; that changes no draw (one draw per step, whoever advances).

## Ordering

The sequence of states of a MarkovSchedule depends only on `seed`, `stream`, `step_ms` and the rows: one draw per step,
in step order, from its own stream (kind `STREAM_SERVER_SCENARIO`, index `stream`), whoever asks and whenever. Two
instances of the same scenario follow different sequences only if their `stream`s differ (the BenchmarkRunner gives
each instance its instance id as `stream`). A ScenarioSchedule draws nothing.

## Concurrency

One lock per scenario. `state_at` is called by workers at every pop and stall check, `next_change_at` by waiters,
`scenario_arm` by the first push, and the ScenarioLog writer advances and reads the change list. Nothing is done under
the lock but arithmetic and draws; the writer writes its rows outside it. The waits of a StalledState are the
WorkQueue's own condition-variable waits, bounded by `next_change_at` ([../worker_pool/design.md#workqueue](../worker_pool/design.md#workqueue)).

## Failure and edge cases

| Situation | Behavior |
|---|---|
| No `/ping` yet | First state, ScenarioClock 0, `next_change_at` none: a first state that stalls holds every worker until after the first push and then until its end |
| After the last schedule entry | That state for good; a stall there is permanent |
| A Markov row with `p = 1` to itself | An absorbing state |
| `now_ns` before the arm time (another thread's earlier read) | Clock 0 (no wrap-around) |
| Very small `step_ms` | One draw per step, including steps nobody asked about (the writer, or the next query, catches up) |
| Shutdown during a stall | `queue_close` wakes every waiter, as for a WorkerFreeze; the writer stops |
| `scenario_log_path` set without a scenario | Nothing is written (the file is not created) |

## Worked examples

**ScenarioSchedule.** `states = {fast: {service_us: 400000}, down: {stall: true}, slow: {service_us: 800000}}`,
`schedule = [[0, fast], [60000, down], [80000, slow]]`; `t` is ScenarioClock time:

| `t` | `state_at` | `next_change_at` (as ScenarioClock time) | Check |
|---|---|---|---|
| not armed | fast | none | first entry; clock not started |
| 0 | fast | 60 s | `0 <= 0` |
| 59 999 999 999 ns | fast | 60 s | `< 60 000 000 000` |
| 60 s | down | 80 s | `60 000 000 000 <= t` |
| 79.9 s | down | 80 s | |
| 80 s | slow | none | last entry |
| 500 s | slow | none | |

ScenarioLog: `0,<arm>,fast`, `60000000000,<arm+60 s>,down`, `80000000000,<arm+80 s>,slow`.

**MarkovSchedule.** `seed = 42`, `stream = 0`, so the stream seed is `derive_seed(42, 7, 0) = 7197217839792819605`;
`states = {down: {stall: true}, fast: {service_us: 400000}}`, `initial = fast`, `step_ms = 1000`,
`p = {fast: {down: 0.6, fast: 0.4}, down: {down: 0.7, fast: 0.3}}`; targets in name order: `down`, then `fast`:

| Step `k` (time) | `u = next_unit` | From | Cumulative check | `s_k` |
|---|---|---|---|---|
| 1 (1 s) | 0.64914772414042154 | fast | `u >= 0.6`, `u < 1.0` | fast |
| 2 (2 s) | 0.58157259907852199 | fast | `u < 0.6` | down |
| 3 (3 s) | 0.78373825975853983 | down | `u >= 0.7`, `u < 1.0` | fast |
| 4 (4 s) | 0.45530670850642208 | fast | `u < 0.6` | down |
| 5 (5 s) | 0.68629871035240841 | down | `u < 0.7` | down |
| 6 (6 s) | 0.62169536156500282 | down | `u < 0.7` | down |

`state_at` at 2.5 s is `down`, at 3.2 s `fast`; `next_change_at` at 2.5 s is 3 s. ScenarioLog: `0 fast`, `2 s down`,
`3 s fast`, `4 s down` (steps 5 and 6 stay, no rows).

**Validation** (each returns the error shown):

| `service_scenario` | Error |
|---|---|
| `{"states": {}, "schedule": [[0, "a"]]}` | `states must be a non-empty object` |
| `{"states": {"a": {"service_us": 0}}, "schedule": [[0, "a"]]}` | `state a needs service_us >= 1 or stall: true` |
| `{"states": {"a": {"stall": true}}}` | `give exactly one of schedule and markov` |
| `{"states": {"a": {"stall": true}}, "schedule": [[5, "a"]]}` | `bad schedule` (does not start at 0) |
| `{"states": {"a": {"service_us": 1}}, "schedule": [[0, "a"], [0, "a"]]}` | `bad schedule` (not strictly increasing) |
| markov with row `a: {a: 0.5, b: 0.4}` | `bad markov row a` (sums to 0.9) |
| markov with no row for state `b` | `bad markov row b` |
| `{"states": …, "schedule": …, "extra": 1}` | `unknown key extra` |

## Design specific to Go

Implementation: `implementation/go` (module `testbed`, Go 1.21, `golang.org/x/net v0.17.0`).
Build and test with `CGO_ENABLED=0 go build ./...` / `CGO_ENABLED=0 go test ./...`. On recent macOS the dynamic
loader rejects cgo-linked Go 1.21 binaries (missing `LC_UUID`), so cgo is disabled.

### Go choices

- File `internal/server/scenario.go`. `scenario_new` is `NewScenario(json, seed) (*Scenario, error)`; `ScenarioState` is a struct (`Name`, `ServiceUs`, `Stall`), and states are compared by pointer. `state_at` is `(*Scenario).StateAt(now)`, `next_change_at` is `(*Scenario).NextChangeAt(now) (time.Time, bool)` (`false` is none), `scenario_arm` is `(*Scenario).Arm(armedAt)`.
- JSON is parsed with `encoding/json` and `UseNumber`, so integers are read with `strconv.ParseUint` (digits only: `1.5` and `1e3` are not integers) and probabilities with `strconv.ParseFloat`. A second value after the object is refused. Keys of one object are checked in bytewise order (`sort.Strings`), so the first error reported never depends on the JSON's key order. Duplicate keys keep the last value (`encoding/json`).
- Monotonic ns are `time.Time` values with their monotonic reading. `Arm` takes one `time.Now()`: its monotonic reading is the ScenarioClock's origin, its wall reading (`UnixNano`) the ScenarioLog's arm time, so the arm is one clock read, not two. `clock` is `now.Sub(armedAt)` (monotonic), `0` for a reading not after the arm.
- `next_change_at` values beyond `time.Duration` (about 292 years after the arm, possible only with a huge `at_ms` or `step_ms`) are returned as none: no run can tell them from a change that never comes. `markov_next` uses `bits.Mul64` to detect that overflow.
- One `sync.Mutex` per scenario. The MarkovSchedule's `rng` is a `sampling.Rng` from `DeriveSeed(seed, StreamServerScenario, stream)`, used only under the lock. The arm notifies the writer by closing the channel `armedCh`.
- The ScenarioLog writer is `ScenarioLogger`, a goroutine started by `StartScenarioLogger(sc, out)` and ended by `Stop()` (closes a stop channel and waits for a done channel), like the QueueLengthLog sampler. It waits for each `next_change_at` with a `time.Timer` in a `select` on the stop channel, and writes through a `bufio.Writer` flushed after each batch of rows. A write error prints one line to stderr and ends the writer (the file is closed); the server keeps serving, as for the QueueLengthLog.
- Tests: `scenario_test.go` checks the ScenarioSchedule worked example (states and `next_change_at` at every row, the change list), the MarkovSchedule worked example (the derived seed, the six `next_unit` values, the states, a query older than the chain, the change list, the same states when jumping ahead, a separate stream), every validation row plus a few more, the edge cases (a reading before the arm, a second arm, an absorbing state), and the ScenarioLog of a schedule (a repeated entry writes no row) and of a Markov chain advanced by the writer alone (10 ms steps, exact `t_ns` and `unix_ns`).

### Traceability

Generated from the `// Design:` tags in the code by `implementation/spec_tools/tracegen.py` (see `AGENTS.md`). Each link points at the first code line after the tag.
Regenerate from the repository root with `python3 implementation/spec_tools/tracegen.py --lang Go --ext .go --comment // --write`.

<!-- trace:begin -->
| Design subsection | Implementation (file:line) |
|---|---|
| [ServiceScenario](#servicescenario) | [scenario.go:60](../../../implementation/go/internal/server/scenario.go#L60), [scenario.go:108](../../../implementation/go/internal/server/scenario.go#L108) |
| [ScenarioClock](#scenarioclock) | [scenario.go:255](../../../implementation/go/internal/server/scenario.go#L255), [scenario.go:271](../../../implementation/go/internal/server/scenario.go#L271) |
| [ScenarioSchedule](#scenarioschedule) | [scenario.go:169](../../../implementation/go/internal/server/scenario.go#L169), [scenario.go:287](../../../implementation/go/internal/server/scenario.go#L287) |
| [MarkovSchedule](#markovschedule) | [scenario.go:200](../../../implementation/go/internal/server/scenario.go#L200), [scenario.go:299](../../../implementation/go/internal/server/scenario.go#L299), [scenario.go:333](../../../implementation/go/internal/server/scenario.go#L333) |
| [state_at and next_change_at](#state_at-and-next_change_at) | [scenario.go:344](../../../implementation/go/internal/server/scenario.go#L344), [scenario.go:362](../../../implementation/go/internal/server/scenario.go#L362) |
| [StalledState](#stalledstate) | [scenario.go:36](../../../implementation/go/internal/server/scenario.go#L36) |
| [ScenarioLog](#scenariolog) | [scenario.go:386](../../../implementation/go/internal/server/scenario.go#L386), [scenario.go:408](../../../implementation/go/internal/server/scenario.go#L408), [scenario.go:417](../../../implementation/go/internal/server/scenario.go#L417), [scenario.go:430](../../../implementation/go/internal/server/scenario.go#L430), [scenario_test.go:231](../../../implementation/go/internal/server/scenario_test.go#L231) |
| [Ordering](#ordering) | [scenario.go:299](../../../implementation/go/internal/server/scenario.go#L299), [scenario_test.go:83](../../../implementation/go/internal/server/scenario_test.go#L83) |
| [Concurrency](#concurrency) | [scenario.go:60](../../../implementation/go/internal/server/scenario.go#L60) |
| [Failure and edge cases](#failure-and-edge-cases) | [scenario_test.go:190](../../../implementation/go/internal/server/scenario_test.go#L190) |
| [Worked examples](#worked-examples) | [scenario_test.go:31](../../../implementation/go/internal/server/scenario_test.go#L31), [scenario_test.go:83](../../../implementation/go/internal/server/scenario_test.go#L83), [scenario_test.go:146](../../../implementation/go/internal/server/scenario_test.go#L146) |
<!-- trace:end -->
