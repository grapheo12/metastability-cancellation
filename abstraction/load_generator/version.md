# Version — load_generator

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-10-30T00:00:00Z | Initial spec: structure, time base, concurrency, phases |
| 0.1.1 | 2026-10-30T00:00:00Z | Style: description opens with subcomponents; `LoadGenConfig` under Types with design link. Children: client, metrics, run_controller 0.1.1. No behavior change |
| 0.2.0 | 2026-11-10T00:00:00Z | Parameter `n_waiting_requests` (default 100); description, structure table and concurrency for the SemiOpenLoopClient. Child bump: client 0.3.0 |
| 0.2.1 | 2026-11-10T00:00:00Z | Phases: DrainPhase wording. Child bump: run_controller 0.2.0 |
| 0.2.2 | 2026-11-12T00:00:00Z | Child bump: metrics 0.2.0 (`run_start_unix_ns`) |
