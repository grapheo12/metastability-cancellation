# Version — benchmark_runner

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-10-31T00:00:00Z | Initial spec: commands, output directory, run flow. Children: config, remote_transport, remote_setup, experiment_executor, result_plotter 0.1.0 |
| 0.1.1 | 2026-10-31T00:00:00Z | Child bump: remote_transport 0.1.1 |
| 0.1.2 | 2026-11-02T00:00:00Z | Child bump: remote_setup 0.2.0 |
| 0.1.3 | 2026-11-02T00:00:00Z | Child bump: config 0.2.0. Run flow checks `threads` against the BuildManifest `thread_env` and warns when `cpu_set` cannot be applied |
| 0.1.4 | 2026-11-02T00:00:00Z | Child bump: experiment_executor 0.2.0. Output directory lists `placement.json` |
| 0.1.5 | 2026-11-02T00:00:00Z | Child bump: result_plotter 0.2.0. Output directory lists `throughput_latency.png` |
| 0.1.6 | 2026-11-02T00:00:00Z | Child bump: config 0.2.1 |
| 0.1.7 | 2026-11-02T00:00:00Z | Child bump: remote_transport 0.2.0 |
| 0.1.8 | 2026-11-02T00:00:00Z | Child bump: config 0.2.2 |
| 0.1.9 | 2026-11-02T00:00:00Z | Child bump: result_plotter 0.3.0. Output directory lists `server_counters_normalized.png` |
| 0.1.10 | 2026-11-02T00:00:00Z | Child bump: result_plotter 0.4.0. Output directory lists `throughput_latency_normalized.png` |
| 0.2.0 | 2026-11-02T00:00:00Z | Child bump: config 0.3.0 (Ingress replaces the canceller in configs) |
| 0.2.1 | 2026-11-02T00:00:00Z | Child bump: remote_setup 0.3.0 |
| 0.2.2 | 2026-11-02T00:00:00Z | Child bump: experiment_executor 0.3.0. Output directory, description, run-flow placement checks and Uses describe the Ingress and server instances |
| 0.2.3 | 2026-11-02T00:00:00Z | Child bump: result_plotter 0.5.0 |
| 0.2.4 | 2026-11-03T00:00:00Z | Child bump: config 0.4.0 (IngressPlacement) |
| 0.2.5 | 2026-11-03T00:00:00Z | Child bump: experiment_executor 0.4.0 |
| 0.2.6 | 2026-11-10T00:00:00Z | Child bump: config 0.5.0 (`n_worker_threads`) |
| 0.2.7 | 2026-11-10T00:00:00Z | Child bump: config 0.6.0 (QueueLengthLog parameters) |
| 0.2.8 | 2026-11-10T00:00:00Z | Output tree: `server_<k>.queue.csv`. Child bump: experiment_executor 0.5.0 |
| 0.2.9 | 2026-11-10T00:00:00Z | Output tree: `queue_length.png`. Child bump: result_plotter 0.6.0 |
| 0.2.10 | 2026-11-10T00:00:00Z | Child bump: config 0.7.0 (`initial_stall_ms`) |
| 0.2.11 | 2026-11-10T00:00:00Z | Child bump: config 0.8.0 (`n_waiting_requests`) |
| 0.2.12 | 2026-11-11T00:00:00Z | Child bump: config 0.9.0 (`ewma_beta`) |
| 0.2.13 | 2026-11-12T00:00:00Z | Child bump: config 0.10.0 (server workload parameters) |
| 0.2.14 | 2026-11-12T00:00:00Z | Output directory lists `ingress.forwards.csv`. Child bump: experiment_executor 0.6.0 |
| 0.2.15 | 2026-11-12T00:00:00Z | Output directory lists the forward timeline plots. Child bump: result_plotter 0.7.0 |
| 0.2.16 | 2026-11-12T00:00:00Z | Output directory lists `.html` figures and their shared script file. Child bump: result_plotter 0.8.0 |
| 0.2.17 | 2026-11-12T00:00:00Z | Child bump: experiment_executor 0.6.1 |
| 0.2.18 | 2026-11-12T00:00:00Z | Child bump: remote_setup 0.4.0 (GcTraceHook) |
| 0.2.19 | 2026-11-12T00:00:00Z | Child bump: config 0.11.0 (`gc_trace`) |
| 0.2.20 | 2026-11-12T00:00:00Z | Child bump: remote_setup 0.4.1 (MachineReady carries the GcTraceHook) |
| 0.2.21 | 2026-11-12T00:00:00Z | Run flow refuses `gc_trace` without a GcTraceHook (exit 2); output directory lists `server_<k>.gc.csv`. Child bump: experiment_executor 0.7.0 |
| 0.2.22 | 2026-11-12T00:00:00Z | Child bump: result_plotter 0.9.0 (GcOverlay) |
| 0.2.23 | 2026-11-12T00:00:00Z | Output directory lists `availability.html`. Child bump: result_plotter 0.10.0 |
| 0.2.24 | 2026-11-12T00:00:00Z | Child bump: config 0.12.0 |
| 0.2.25 | 2026-11-12T00:00:00Z | Child bump: result_plotter 0.11.0 |
| 0.2.26 | 2026-11-12T00:00:00Z | Child bump: config 0.13.0 (`retry_timeout_ms`, `max_retries`) |
| 0.2.27 | 2026-11-12T00:00:00Z | Child bump: result_plotter 0.12.0 (`lg_dropped`, `lg_copies_sent`) |
| 0.2.28 | 2026-11-12T00:00:00Z | Child bump: config 0.14.0 (`initial_stall_ms` copied to the load generator) |
