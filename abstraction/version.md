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
| 0.5.4 | 2026-11-02T00:00:00Z | Child bump: benchmark_runner 0.2.3 (result_plotter 0.5.0) |
| 0.5.5 | 2026-11-02T00:00:00Z | Glossary: LbImbalance |
| 0.5.6 | 2026-11-03T00:00:00Z | Child bump: benchmark_runner 0.2.4 (config 0.4.0) |
| 0.5.7 | 2026-11-03T00:00:00Z | Child bump: benchmark_runner 0.2.5 (experiment_executor 0.4.0) |
| 0.5.8 | 2026-11-03T00:00:00Z | System summary and glossary: IngressPlacement |
| 0.5.9 | 2026-11-04T00:00:00Z | Ingress `num_clients` bounds the InFlightMap key space; child bump: ingress 0.1.6 (inflight_map 0.2.0) |
| 0.6.0 | 2026-11-10T00:00:00Z | Server parameter `n_worker_threads`. System summary, glossary (WorkQueue, WorkerPool, PingJob replace SerialStreamWorker; CancelInbox, CancelMatching wording), component diagram, request and cancel flows, "Why the server has one shared WorkQueue". Child bump: server 0.3.0 (new worker_pool 0.1.0) |
| 0.6.1 | 2026-11-10T00:00:00Z | Glossary: ClientCancelState. Child bump: server 0.3.1 (cancel_tracker 0.3.0) |
| 0.6.2 | 2026-11-10T00:00:00Z | Child bump: server 0.3.2 (ping_handler 0.4.0) |
| 0.6.3 | 2026-11-10T00:00:00Z | Child bump: server 0.3.3 (connection_handler 0.3.0) |
| 0.6.4 | 2026-11-10T00:00:00Z | Child bump: benchmark_runner 0.2.6 (config 0.5.0) |
| 0.6.5 | 2026-11-10T00:00:00Z | Child bump: ingress 0.1.7 (upstream_pool 0.1.1) |
| 0.7.0 | 2026-11-10T00:00:00Z | Server parameters `queue_log_path`, `queue_sample_interval_ms`; server outputs: QueueLengthLog; non-goals and glossary (QueueLengthLog). Child bump: server 0.4.0 (worker_pool 0.2.0) |
| 0.7.1 | 2026-11-10T00:00:00Z | Child bump: benchmark_runner 0.2.7 (config 0.6.0) |
| 0.7.2 | 2026-11-10T00:00:00Z | Child bump: benchmark_runner 0.2.8 (experiment_executor 0.5.0) |
| 0.7.3 | 2026-11-10T00:00:00Z | Glossary: QueueLengthPlot. Child bump: benchmark_runner 0.2.9 (result_plotter 0.6.0) |
| 0.8.0 | 2026-11-10T00:00:00Z | Server parameter `initial_stall_ms`; glossary StallGate. Child bump: server 0.5.0 (worker_pool 0.3.0) |
| 0.8.1 | 2026-11-10T00:00:00Z | Ingress parameter `initial_stall_ms`. Child bump: ingress 0.1.8 (load_canceller 0.4.0) |
| 0.8.2 | 2026-11-10T00:00:00Z | Child bump: benchmark_runner 0.2.10 (config 0.7.0) |
| 0.9.0 | 2026-11-10T00:00:00Z | Load generator parameter `n_waiting_requests`; system summary, glossary (SemiOpenLoopClient replaces ClosedLoopPoissonClient; GatedArrival), request flow and WorkQueue rationale for many outstanding requests per client. Child bump: load_generator 0.2.0 (client 0.3.0) |
| 0.9.1 | 2026-11-10T00:00:00Z | Wire contract: peers accept at least `MAX_WAITING_REQUESTS` (1000) concurrent streams per connection. Child bump: load_generator 0.2.1 (run_controller 0.2.0) |
| 0.9.2 | 2026-11-10T00:00:00Z | Glossary: ClientCancelState, CancelMatching; WorkQueue rationale no longer relies on request order. Child bump: server 0.5.1 (cancel_tracker 0.4.0) |
| 0.9.3 | 2026-11-10T00:00:00Z | Child bump: server 0.5.2 (ping_handler 0.5.0) |
| 0.9.4 | 2026-11-10T00:00:00Z | Child bump: server 0.5.3 (worker_pool 0.3.1) |
| 0.9.5 | 2026-11-10T00:00:00Z | Glossary: InFlightMap keyed by RequestId. Child bump: ingress 0.1.9 (inflight_map 0.3.0) |
| 0.9.6 | 2026-11-10T00:00:00Z | Child bump: ingress 0.1.10 (frontend 0.2.0) |
| 0.9.7 | 2026-11-10T00:00:00Z | Glossary: InFlightVictimChoice per request. Child bump: ingress 0.1.11 (load_canceller 0.5.0) |
| 0.9.8 | 2026-11-10T00:00:00Z | Child bump: ingress 0.1.12 (upstream_pool 0.1.2) |
| 0.9.9 | 2026-11-10T00:00:00Z | Child bump: benchmark_runner 0.2.11 (config 0.8.0) |
| 0.9.10 | 2026-11-11T00:00:00Z | Child bump: ingress 0.1.13 (load_balancer 0.2.0) |
| 0.9.11 | 2026-11-11T00:00:00Z | Ingress parameters: `lb_policy` value `ewma`, new `ewma_beta`. Glossary: LbPolicy, BackendStats, new EwmaLatency, EwmaScore, ForwardTicket |
| 0.9.12 | 2026-11-11T00:00:00Z | Child bump: ingress 0.1.14 (frontend 0.2.1) |
| 0.9.13 | 2026-11-11T00:00:00Z | Child bump: benchmark_runner 0.2.12 (config 0.9.0) |
| 0.9.14 | 2026-11-12T00:00:00Z | Child bump: server 0.6.0 (new message_buffer 0.1.0) |
| 0.9.15 | 2026-11-12T00:00:00Z | Child bump: server 0.6.1 (worker_pool 0.4.0) |
| 0.9.16 | 2026-11-12T00:00:00Z | Child bump: server 0.6.2 (ping_handler 0.6.0) |
| 0.9.17 | 2026-11-12T00:00:00Z | Child bump: ingress 0.1.15 (load_balancer 0.3.0) |
| 0.9.18 | 2026-11-12T00:00:00Z | Server parameters: `workload`, `window_size`, `message_bytes`, `n_messages_per_request`; `response_rate` required only for `cpu_burn`. Ingress `output_path` names `forwards.csv`. Glossary: ServerWorkload, MessageBuffer, MessageCursor, MessageWork, ForwardRecord, ForwardLog |
| 0.9.19 | 2026-11-12T00:00:00Z | Child bump: benchmark_runner 0.2.13 (config 0.10.0) |
| 0.9.20 | 2026-11-12T00:00:00Z | Child bump: benchmark_runner 0.2.14 (experiment_executor 0.6.0) |
| 0.9.21 | 2026-11-12T00:00:00Z | Child bump: benchmark_runner 0.2.15 (result_plotter 0.7.0) |
| 0.9.22 | 2026-11-12T00:00:00Z | Child bump: benchmark_runner 0.2.16 (result_plotter 0.8.0) |
| 0.9.23 | 2026-11-12T00:00:00Z | Child bump: benchmark_runner 0.2.17 (experiment_executor 0.6.1) |
| 0.9.24 | 2026-11-12T00:00:00Z | Glossary: FigurePage |
| 0.9.25 | 2026-11-12T00:00:00Z | Child bump: server 0.7.0 (ProcessStart line) |
| 0.9.26 | 2026-11-12T00:00:00Z | Child bump: ingress 0.1.16 (`run_start_unix_ns`) |
| 0.9.27 | 2026-11-12T00:00:00Z | Child bump: ingress 0.1.17 (frontend 0.2.2) |
| 0.9.28 | 2026-11-12T00:00:00Z | Child bump: benchmark_runner 0.2.18 (remote_setup 0.4.0) |
| 0.9.29 | 2026-11-12T00:00:00Z | Child bump: benchmark_runner 0.2.19 (config 0.11.0) |
| 0.9.30 | 2026-11-12T00:00:00Z | Child bump: benchmark_runner 0.2.20 (remote_setup 0.4.1) |
| 0.9.31 | 2026-11-12T00:00:00Z | Child bump: benchmark_runner 0.2.21 (experiment_executor 0.7.0) |
| 0.9.32 | 2026-11-12T00:00:00Z | Child bump: benchmark_runner 0.2.22 (result_plotter 0.9.0) |
| 0.9.33 | 2026-11-12T00:00:00Z | Server and Ingress outputs name `process_start_unix_ns` and `run_start_unix_ns`. Glossary: GcTrace, GcTraceHook, GcEvent, GcOverlay |
| 0.9.34 | 2026-11-12T00:00:00Z | Child bump: load_generator 0.2.2 (metrics 0.2.0) |
| 0.9.35 | 2026-11-12T00:00:00Z | Child bump: load_generator 0.2.3 (run_controller 0.3.0) |
