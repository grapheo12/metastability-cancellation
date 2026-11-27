# Service Scenario — Description

Leaf component. A server's optional **ServiceScenario**: a state machine that decides, over time, how long the
server's requests take. Each **ScenarioState** either fixes the service time to a constant (a ConstantServiceTime
while in it) or is a **StalledState**, in which no request makes progress and the server stops beating, as during a
WorkerFreeze, until the state is left. The transitions are part of the scenario:

- **ScenarioSchedule:** time-based. At every moment of the **ScenarioClock** the server is in exactly one state, the
  one of the last schedule entry at or before that moment.
- **MarkovSchedule:** random. A discrete-time Markov chain: every `step_ms` the next state is drawn from the current
  state's row of a transition matrix, from the server's seed and the scenario's own random stream, so the sequence of
  states is reproducible.

Responsibilities:
- Parse and validate a scenario given as JSON (`service_scenario`).
- Answer which state the server is in at a ScenarioClock time, and when the state can next change.
- Write the **ScenarioLog**: one row when the clock starts and one at every change of state.

It exists so experiments can script how servers behave (a slowdown, a stall that recovers, a server that flaps)
from the experiment config, instead of a single service time and a permanent WorkerFreeze.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
