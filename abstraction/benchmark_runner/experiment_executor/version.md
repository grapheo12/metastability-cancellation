# Version — benchmark_runner/experiment_executor

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-10-31T00:00:00Z | Initial spec: run_point, ServerLifecycle, ClientRun, point outputs |
| 0.2.0 | 2026-11-02T00:00:00Z | ProcessPlacement: `placement_prefix` (env thread count, taskset CPU pinning) before each program, `placement.json` per point |
| 0.3.0 | 2026-11-02T00:00:00Z | Multiple server instances (pid file and log per instance), IngressLifecycle on the client machine (started after the servers, stopped before them), ClientRun runs only the load generator against the local Ingress. BackgroundProgram shared start/stop. Outputs: ingress.*, server_<k>.out, per-instance and summed CancelCounters |
