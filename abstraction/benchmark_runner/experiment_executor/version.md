# Version — benchmark_runner/experiment_executor

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-10-31T00:00:00Z | Initial spec: run_point, ServerLifecycle, ClientRun, point outputs |
| 0.2.0 | 2026-11-02T00:00:00Z | ProcessPlacement: `placement_prefix` (env thread count, taskset CPU pinning) before each program, `placement.json` per point |
| 0.3.0 | 2026-11-02T00:00:00Z | Multiple server instances (pid file and log per instance), IngressLifecycle on the client machine (started after the servers, stopped before them), ClientRun runs only the load generator against the local Ingress. BackgroundProgram shared start/stop. Outputs: ingress.*, server_<k>.out, per-instance and summed CancelCounters |
| 0.4.0 | 2026-11-03T00:00:00Z | IngressPlacement: the Ingress starts, stops and is collected on `ingress_host` (server or client machine); the load generator targets `point.loadgen_target`; leftover Ingress cleanup on both machines |
| 0.5.0 | 2026-11-10T00:00:00Z | QueueLengthLog: every server instance gets `--queue_log_path <RD>/server_<k>.queue.csv`; Point outputs fetch it, warn once if missing, never fail the point |
| 0.6.0 | 2026-11-12T00:00:00Z | Point outputs fetch `ingress.forwards.csv` (ForwardLog), warn once if missing, never fail the point |
| 0.6.1 | 2026-11-12T00:00:00Z | The missing QueueLengthLog warning names the `queue_length` figure without a file extension |
