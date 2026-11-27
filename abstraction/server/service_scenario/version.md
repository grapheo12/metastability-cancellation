# Version — server/service_scenario

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-11-27T00:00:00Z | Initial spec: ServiceScenario (JSON `service_scenario`, validation), ScenarioState (ConstantServiceTime or StalledState), ScenarioClock (armed with the StallGate), ScenarioSchedule (time-based), MarkovSchedule (discrete-time, `next_unit` from `derive_seed(seed, STREAM_SERVER_SCENARIO, stream)`, targets in name order, one draw per step), `next_change_at`, ScenarioLog; worked examples |
