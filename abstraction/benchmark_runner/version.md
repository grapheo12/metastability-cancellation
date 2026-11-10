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
