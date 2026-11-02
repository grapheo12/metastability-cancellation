# Version — abstraction (root)

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-10-30T00:00:00Z | Initial spec: description, API, top-level design |
| 0.2.0 | 2026-10-30T00:00:00Z | Server-directed cancellation: `/cancel` API, Load Canceller program, CancelCounters, RequestId layout flipped to client id in high bits |
| 0.2.1 | 2026-10-30T00:00:00Z | Child bump: protocol 0.2.0 |
| 0.2.2 | 2026-10-30T00:00:00Z | Child bump: sampling 0.2.0 |
| 0.2.3 | 2026-10-30T00:00:00Z | Child bump: server 0.2.0 (new cancel_tracker 0.1.0) |
| 0.2.4 | 2026-10-30T00:00:00Z | Child bump: load_canceller 0.1.0 (new) |
| 0.2.5 | 2026-10-30T00:00:00Z | API style: Exit status section, Components table, design links for every output. No behavior change |
| 0.2.6 | 2026-10-30T00:00:00Z | Child bump: protocol 0.2.1 |
| 0.2.7 | 2026-10-30T00:00:00Z | Child bump: sampling 0.2.1 |
| 0.2.8 | 2026-10-30T00:00:00Z | Child bump: server 0.2.1 (cancel_tracker 0.1.1, connection_handler 0.2.1, cpu_burner 0.2.1, ping_handler 0.2.1) |
| 0.2.9 | 2026-10-30T00:00:00Z | Child bump: load_generator 0.1.1 (client, metrics, run_controller 0.1.1) |
| 0.2.10 | 2026-10-30T00:00:00Z | Child bump: load_canceller 0.1.1 |
| 0.3.0 | 2026-10-31T00:00:00Z | New component: Benchmark Runner (description, glossary, `bench` program, design diagram) |
| 0.3.1 | 2026-10-31T00:00:00Z | Child bump: benchmark_runner 0.1.0 (new: config, remote_transport, remote_setup, experiment_executor, result_plotter) |
| 0.3.2 | 2026-10-31T00:00:00Z | Child bump: benchmark_runner 0.1.1 (remote_transport 0.1.1) |
| 0.3.3 | 2026-11-02T00:00:00Z | Child bump: benchmark_runner 0.1.2 (remote_setup 0.2.0) |
| 0.3.4 | 2026-11-02T00:00:00Z | Child bump: benchmark_runner 0.1.3 (config 0.2.0) |
| 0.3.5 | 2026-11-02T00:00:00Z | Child bump: benchmark_runner 0.1.4 (experiment_executor 0.2.0) |
| 0.3.6 | 2026-11-02T00:00:00Z | Child bump: benchmark_runner 0.1.5 (result_plotter 0.2.0) |
| 0.3.7 | 2026-11-02T00:00:00Z | Glossary: ProcessPlacement, CancelRateFraction, ThroughputLatencyPlot |
| 0.3.8 | 2026-11-02T00:00:00Z | `/cancel` wire contract: `202` body is a CancelReplyBody. Child bump: protocol 0.3.0 |
| 0.3.9 | 2026-11-02T00:00:00Z | Child bump: server 0.2.2 (cancel_tracker 0.2.0) |
| 0.3.10 | 2026-11-02T00:00:00Z | Child bump: server 0.2.3 (ping_handler 0.3.0) |
| 0.4.0 | 2026-11-02T00:00:00Z | Canceller parameter `incr_rand_max`. System summary: the canceller aims past each client's reported FinishedWatermark. Child bump: load_canceller 0.2.0 |
| 0.4.1 | 2026-11-02T00:00:00Z | Child bump: benchmark_runner 0.1.6 (config 0.2.1) |
| 0.4.2 | 2026-11-02T00:00:00Z | Glossary: FinishedWatermark, CancelReplyBody |
| 0.4.3 | 2026-11-02T00:00:00Z | Child bump: benchmark_runner 0.1.7 (remote_transport 0.2.0) |
| 0.4.4 | 2026-11-02T00:00:00Z | Child bump: benchmark_runner 0.1.8 (config 0.2.2) |
| 0.4.5 | 2026-11-02T00:00:00Z | Child bump: benchmark_runner 0.1.9 (result_plotter 0.3.0) |
| 0.4.6 | 2026-11-02T00:00:00Z | Child bump: benchmark_runner 0.1.10 (result_plotter 0.4.0) |
| 0.4.7 | 2026-11-02T00:00:00Z | Glossary: CancelOfferedFraction, ThroughputLatencyNormalizedPlot |
| 0.4.8 | 2026-11-02T00:00:00Z | Child bump: protocol 0.3.1 |
| 0.4.9 | 2026-11-02T00:00:00Z | Child bump: sampling 0.2.2 |
| 0.4.10 | 2026-11-02T00:00:00Z | New child (not yet linked from the root documents): ingress 0.1.0 (inflight_map 0.1.0) |
| 0.4.11 | 2026-11-02T00:00:00Z | Child bump: ingress 0.1.1 (load_balancer 0.1.0) |
| 0.4.12 | 2026-11-02T00:00:00Z | Child bump: ingress 0.1.2 (upstream_pool 0.1.0) |
| 0.4.13 | 2026-11-02T00:00:00Z | Child moved: load_canceller is now ingress/load_canceller (ingress 0.1.3). Link paths updated only; root content follows |
| 0.4.14 | 2026-11-02T00:00:00Z | Child bump: ingress 0.1.4 (frontend 0.1.0) |
| 0.5.0 | 2026-11-02T00:00:00Z | Programs are server, loadgen and ingress: Ingress parameters and outputs replace the canceller program; system summary, glossary (Ingress, LoadBalancer, LbPolicy, BackendStats, UpstreamPool, InFlightMap, InFlightEntry, ForwardTask, InFlightVictimChoice; CancelTargetChoice removed), component diagram, request and cancel flows, time bounds. Child bump: ingress 0.1.5 |
| 0.5.1 | 2026-11-02T00:00:00Z | Child bump: benchmark_runner 0.2.0 (config 0.3.0) |
| 0.5.2 | 2026-11-02T00:00:00Z | Child bump: benchmark_runner 0.2.1 (remote_setup 0.3.0) |
| 0.5.3 | 2026-11-02T00:00:00Z | Child bump: benchmark_runner 0.2.2 (experiment_executor 0.3.0) |
