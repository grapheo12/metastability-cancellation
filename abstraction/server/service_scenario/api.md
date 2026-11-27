# Service Scenario — API

## Parameters

Server program parameters ([../api.md#program-parameters](../api.md#program-parameters)) read by this component:

| Parameter | Type / unit / constraint | Default | Meaning |
|---|---|---|---|
| `service_scenario` | JSON string, or empty | empty | The ServiceScenario ([design.md#servicescenario](design.md#servicescenario)); empty means none. Only with `workload = sleep_constant` |
| `scenario_log_path` | path, or empty | empty | Where the ScenarioLog is written ([design.md#scenariolog](design.md#scenariolog)); empty means none |
| `seed` | u64 | `0` | The server's seed; a MarkovSchedule draws from `derive_seed(seed, STREAM_SERVER_SCENARIO, stream)` |

## Constants

| Name | Value | Meaning |
|---|---|---|
| `MARKOV_ROW_TOLERANCE` | `1e-9` | A MarkovSchedule row must sum to 1 within this ([design.md#servicescenario](design.md#servicescenario)) |
| `STREAM_SERVER_SCENARIO` | `7` | Sampling stream kind of a MarkovSchedule ([../../sampling/api.md#constants](../../sampling/api.md#constants)) |

## Types

### `ScenarioState`

| Field | Type | Meaning |
|---|---|---|
| `name` | string, `[A-Za-z0-9_-]+` | The state's name in the scenario and in the ScenarioLog |
| `service_us` | u64 `>= 1`, or none | The ConstantServiceTime of a request started in this state; none for a StalledState |
| `stall` | boolean | True for a StalledState ([design.md#stalledstate](design.md#stalledstate)) |

### `ServiceScenario`

| Field | Type | Meaning |
|---|---|---|
| `states` | map name -> ScenarioState | All states, at least one |
| `schedule` | list of `(at_ns: u64, state)`, or none | ScenarioSchedule ([design.md#scenarioschedule](design.md#scenarioschedule)) |
| `markov` | `{initial, step_ns: u64, p: map from -> map to -> float}`, or none | MarkovSchedule ([design.md#markovschedule](design.md#markovschedule)) |
| `stream` | u32 | Index of the MarkovSchedule's random stream (default `0`) |
| `armed_at_ns` | monotonic ns, or none | Start of the ScenarioClock ([design.md#scenarioclock](design.md#scenarioclock)) |

## Functions

### `scenario_new(json: string, seed: u64) -> ServiceScenario | ConfigError`
Parses and validates `service_scenario`; called once by the server's configuration validation. See
[design.md#servicescenario](design.md#servicescenario).

### `scenario_arm(sc, armed_at_ns: monotonic ns, armed_at_unix_ns: Unix ns)`
Starts the ScenarioClock; the WorkQueue calls it at its first push, the same instant that arms the StallGate. Only
the first call counts. See [design.md#scenarioclock](design.md#scenarioclock).

### `state_at(sc, now_ns: monotonic ns) -> ScenarioState`
The state the server is in at `now_ns` (the first state of the schedule, or the MarkovSchedule's `initial`, before the
clock starts). Safe from any thread. See [design.md#scenarioschedule](design.md#scenarioschedule) and
[design.md#markovschedule](design.md#markovschedule).

### `next_change_at(sc, now_ns: monotonic ns) -> monotonic ns | none`
The earliest time after `now_ns` at which the state can change: the next schedule entry, or the next step boundary of
a MarkovSchedule; none when no change can come (after the last schedule entry, or before the clock starts). Used to
time the waits of a StalledState. See [design.md#stalledstate](design.md#stalledstate).

### `scenario_logger(sc, log: file)`
The ScenarioLog writer, its own activity; started by the server when `scenario_log_path` is set, it writes the row of
the clock's start and of every change. See [design.md#scenariolog](design.md#scenariolog).

## Outputs

The ScenarioLog, a CSV file with header `t_ns,unix_ns,state` ([design.md#scenariolog](design.md#scenariolog)):
`t_ns` is ScenarioClock time (ns since the clock started), `unix_ns` the Unix time of the change, `state` the state's
name.

## Uses

- [../../sampling/api.md](../../sampling/api.md): `derive_seed`, `rng_new`, `next_unit`, `STREAM_SERVER_SCENARIO`.
- Called by [../worker_pool/api.md](../worker_pool/api.md) (arm, StalledState, service time at pop) and the server's
  [configuration validation](../design.md#configuration-validation).
