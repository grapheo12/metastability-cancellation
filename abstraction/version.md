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
| 0.9.36 | 2026-11-12T00:00:00Z | Child bump: benchmark_runner 0.2.23 (result_plotter 0.10.0) |
| 0.9.37 | 2026-11-12T00:00:00Z | Load generator outputs name `run_start_unix_ns`. Glossary: Availability, AvailabilityPlot |
| 0.9.38 | 2026-11-12T00:00:00Z | Child bump: sampling 0.3.0 (server message streams) |
| 0.9.39 | 2026-11-12T00:00:00Z | Child bump: server 0.7.1 (message_buffer 0.2.0) |
| 0.9.40 | 2026-11-12T00:00:00Z | Child bump: server 0.7.2 (ping_handler 0.7.0) |
| 0.9.41 | 2026-11-12T00:00:00Z | Server parameters: `n_gc_binary_tree_size` replaces `message_bytes`. Glossary: MessageTree; MessageBuffer holds MessageTrees |
| 0.9.42 | 2026-11-12T00:00:00Z | Child bump: benchmark_runner 0.2.24 (config 0.12.0) |
| 0.9.43 | 2026-11-12T00:00:00Z | Child bump: server 0.7.3 (message_buffer 0.2.1) |
| 0.9.44 | 2026-11-12T00:00:00Z | Child bump: benchmark_runner 0.2.25 (result_plotter 0.11.0) |
| 0.9.45 | 2026-11-12T00:00:00Z | Glossary: Availability is over 200 ms windows |
| 0.10.0 | 2026-11-12T00:00:00Z | Child bump: load_generator 0.3.0 (client 0.4.0, RetryTimer) |
| 0.10.1 | 2026-11-12T00:00:00Z | Load generator parameters `retry_timeout_ms` and `max_retries`; glossary RetryTimer and RequestCopy; request flow, time bounds and concurrent-stream bound with retries. Child bump: load_generator 0.3.1 |
| 0.10.2 | 2026-11-12T00:00:00Z | Child bump: load_generator 0.3.2 (metrics 0.3.0) |
| 0.10.3 | 2026-11-12T00:00:00Z | Child bump: load_generator 0.3.3 (run_controller 0.4.0) |
| 0.10.4 | 2026-11-12T00:00:00Z | Child bump: benchmark_runner 0.2.26 (config 0.13.0) |
| 0.10.5 | 2026-11-12T00:00:00Z | Child bump: benchmark_runner 0.2.27 (result_plotter 0.12.0) |
| 0.11.0 | 2026-11-12T00:00:00Z | Child bump: load_generator 0.4.0 (client 0.5.0, StallWindow) |
| 0.11.1 | 2026-11-12T00:00:00Z | Child bump: load_generator 0.4.1 (metrics 0.4.0) |
| 0.11.2 | 2026-11-12T00:00:00Z | Child bump: load_generator 0.4.2 (run_controller 0.5.0) |
| 0.11.3 | 2026-11-12T00:00:00Z | Load generator parameter `initial_stall_ms`; glossary StallWindow and StallWindowRequest; request flow line. Child bump: load_generator 0.4.3 |
| 0.11.4 | 2026-11-12T00:00:00Z | Child bump: benchmark_runner 0.2.28 (config 0.14.0) |
| 0.11.5 | 2026-11-12T00:00:00Z | Child bump: benchmark_runner 0.2.29 (result_plotter 0.13.0) |
| 0.12.0 | 2026-11-13T00:00:00Z | `/ping` request body is a PingRequestBody with the RequestDeadline; `request_bytes >= 76`; glossary PingRequestBody, RequestDeadline. Child bump: protocol 0.4.0 |
| 0.12.1 | 2026-11-13T00:00:00Z | Child bump: load_generator 0.5.0 (client 0.6.0, RequestDeadline) |
| 0.12.2 | 2026-11-13T00:00:00Z | Child bump: load_generator 0.5.1 (run_controller 0.6.0) |
| 0.12.3 | 2026-11-13T00:00:00Z | Child bump: server 0.8.0 (worker_pool 0.5.0, EarliestDeadlineFirst) |
| 0.12.4 | 2026-11-13T00:00:00Z | Child bump: server 0.8.1 (ping_handler 0.8.0) |
| 0.13.0 | 2026-11-13T00:00:00Z | Server parameter `queue_discipline` (`fifo` \| `edf`, default `fifo`); glossary QueueDiscipline and EarliestDeadlineFirst, WorkQueue wording; request flow and shared-queue rationale name the `edf` order |
| 0.13.1 | 2026-11-13T00:00:00Z | Child bump: benchmark_runner 0.2.30 (config 0.15.0) |
| 0.13.2 | 2026-11-17T00:00:00Z | Child bump: load_generator 0.6.0 (client 0.7.0: the last RequestCopy waits for the SLO) |
| 0.14.0 | 2026-11-17T00:00:00Z | RetryTimer wording in the parameter table, glossary and request flow: the last RequestCopy waits for its answer or the SLO; the request flow no longer lists `dropped` |
| 0.14.1 | 2026-11-18T00:00:00Z | Child bump: benchmark_runner 0.2.31 (config 0.16.0: LinkedSweep) |
| 0.14.2 | 2026-11-18T00:00:00Z | Glossary: LinkedSweep; SweepExpansion leaves out linked keys |
| 0.14.3 | 2026-11-18T00:00:00Z | Child bump: benchmark_runner 0.3.0 (Retry Timeout) |
| 0.15.0 | 2026-11-18T00:00:00Z | Benchmark runner command `bench retry-timeout`; glossary RetryTimeoutCalculator and RetryTimeout |
| 0.15.1 | 2026-11-18T00:00:00Z | Child bump: server 0.9.0 (Sleeper, `workload = sleep`) |
| 0.15.2 | 2026-11-18T00:00:00Z | Child bump: server 0.9.1 (ping_handler 0.9.0) |
| 0.16.0 | 2026-11-18T00:00:00Z | Server parameter `workload` gains `sleep` (`response_rate` required); glossary QuantumSleep, ServerWorkload |
| 0.16.1 | 2026-11-18T00:00:00Z | Child bump: benchmark_runner 0.4.0 (retry_timeout 0.2.0: EtaSlack, RetryFeedback, MaxStableEta) |
| 0.17.0 | 2026-11-18T00:00:00Z | Command `bench stable-eta` and `retry-timeout --eta`; glossary EtaSlack, RetryFeedback, StableLoad, TippingLoad, MaxStableEta |
| 0.17.1 | 2026-11-18T00:00:00Z | Child bump: benchmark_runner 0.4.1 (retry_timeout 0.3.0: Convergence, Lean proofs) |
| 0.18.0 | 2026-11-19T00:00:00Z | Child bump: benchmark_runner 0.5.0 (servers on several machines) |
| 0.18.1 | 2026-11-19T00:00:00Z | Child bump: benchmark_runner 0.5.1 (experiment_executor 0.8.0) |
| 0.19.0 | 2026-11-19T00:00:00Z | Server parameter `freeze_after_ms`; glossary WorkerFreeze. Child bump: server 0.10.0 (worker_pool 0.6.0) |
| 0.19.1 | 2026-11-19T00:00:00Z | Child bump: server 0.10.1 (ping_handler 0.10.0) |
| 0.20.0 | 2026-11-19T00:00:00Z | Glossary FreezeTarget. Child bump: benchmark_runner 0.6.0 (config 0.18.0) |
| 0.20.1 | 2026-11-19T00:00:00Z | Child bump: benchmark_runner 0.6.1 (experiment_executor 0.9.0) |
| 0.21.0 | 2026-11-20T00:00:00Z | Wire contract `/heartbeat`; glossary HeartbeatStream. Child bump: protocol 0.5.0 |
| 0.22.0 | 2026-11-20T00:00:00Z | Server parameter `heartbeat_interval_ms`; glossary Heartbeat. Child bump: server 0.11.0 (heartbeat 0.1.0) |
| 0.22.1 | 2026-11-20T00:00:00Z | Child bump: server 0.11.1 (connection_handler 0.4.0) |
| 0.23.0 | 2026-11-20T00:00:00Z | Ingress parameter `heartbeat_timeout_ms`; glossary BackendLiveness. Child bump: ingress 0.2.0 (heartbeat_monitor 0.1.0) |
| 0.23.1 | 2026-11-20T00:00:00Z | Child bump: ingress 0.2.1 (load_balancer 0.4.0) |
| 0.24.0 | 2026-11-20T00:00:00Z | Child bump: benchmark_runner 0.7.0 (config 0.19.0) |
| 0.24.1 | 2026-11-20T00:00:00Z | Child bump: benchmark_runner 0.7.1 (experiment_executor 0.10.0) |
| 0.24.2 | 2026-11-20T00:00:00Z | Child bump: benchmark_runner 0.7.2 (result_plotter 0.14.0) |
| 0.25.0 | 2026-11-20T00:00:00Z | `/ping` responses carry the ExecReport (ExecutionTime, StartSlack); `response_bytes >= 115`. Child bump: protocol 0.6.0 |
| 0.26.0 | 2026-11-20T00:00:00Z | Child bump: server 0.12.0 (ping_handler 0.11.0, ExecReport) |
| 0.27.0 | 2026-11-20T00:00:00Z | Child bump: load_generator 0.7.0 (metrics 0.5.0) |
| 0.27.1 | 2026-11-20T00:00:00Z | Child bump: load_generator 0.7.1 (client 0.8.0) |
| 0.28.0 | 2026-11-20T00:00:00Z | Glossary EwmaExecTime. Child bump: ingress 0.3.0 (load_balancer 0.5.0) |
| 0.28.1 | 2026-11-20T00:00:00Z | Child bump: ingress 0.3.1 (frontend 0.3.0) |
| 0.28.2 | 2026-11-20T00:00:00Z | Child bump: benchmark_runner 0.7.3 (result_plotter 0.15.0) |
| 0.29.0 | 2026-11-20T00:00:00Z | Glossary StartSlackHistogram. Child bump: benchmark_runner 0.8.0 (result_plotter 0.16.0) |
| 0.30.0 | 2026-11-20T00:00:00Z | Load-generator parameters `slo_cancel` and `drain_timeout_ms`; `slo_ms` defines Availability either way. Child bump: load_generator 0.8.0 (run_controller 0.7.0) |
| 0.30.1 | 2026-11-20T00:00:00Z | Child bump: load_generator 0.8.1 (client 0.9.0) |
| 0.30.2 | 2026-11-20T00:00:00Z | Child bump: load_generator 0.8.2 (metrics 0.6.0) |
| 0.30.3 | 2026-11-20T00:00:00Z | Child bump: benchmark_runner 0.8.1 (config 0.20.0) |
| 0.30.4 | 2026-11-20T00:00:00Z | Child bump: benchmark_runner 0.8.2 (experiment_executor 0.11.0) |
| 0.30.5 | 2026-11-20T00:00:00Z | Glossary FailureTime. Child bump: benchmark_runner 0.8.3 (result_plotter 0.17.0) |
| 0.31.0 | 2026-11-20T00:00:00Z | Ingress parameter `lb_filter`; glossary DeadlineFilter. Child bump: ingress 0.4.0 (load_balancer 0.6.0) |
| 0.31.1 | 2026-11-20T00:00:00Z | Child bump: ingress 0.4.1 (frontend 0.4.0) |
| 0.31.2 | 2026-11-20T00:00:00Z | Child bump: benchmark_runner 0.8.4 (config 0.21.0) |
| 0.32.0 | 2026-11-20T00:00:00Z | `/ping` answers carry the WorkerCount header. Child bump: protocol 0.7.0 |
| 0.32.1 | 2026-11-20T00:00:00Z | Child bump: server 0.12.1 (connection_handler 0.5.0) |
| 0.33.0 | 2026-11-20T00:00:00Z | DeadlineFilter divides by the WorkerCount (glossary). Child bump: ingress 0.5.0 (load_balancer 0.7.0) |
| 0.33.1 | 2026-11-20T00:00:00Z | Child bump: ingress 0.5.1 (frontend 0.5.0) |
| 0.34.0 | 2026-11-21T00:00:00Z | DeadlineFilter is an M/M/k on-time probability test (glossary). Child bump: ingress 0.5.2 (load_balancer 0.8.0) |
| 0.35.0 | 2026-11-21T00:00:00Z | Glossary PickLog. Child bump: ingress 0.6.0 (load_balancer 0.9.0) |
| 0.35.1 | 2026-11-21T00:00:00Z | Child bump: benchmark_runner 0.9.0 (experiment_executor 0.12.0) |
| 0.35.2 | 2026-11-21T00:00:00Z | Glossary ExpectedWaitPlot. Child bump: benchmark_runner 0.9.1 (result_plotter 0.18.0) |
| 0.36.0 | 2026-11-21T00:00:00Z | DeadlineStrategy switchable by `lb_filter` (glossary). Child bump: ingress 0.7.0 (load_balancer 0.10.0) |
| 0.36.1 | 2026-11-23T00:00:00Z | Child bump: server 0.12.2 (cancel_tracker 0.5.0) |
| 0.37.0 | 2026-11-23T00:00:00Z | Server parameter `reject_past_deadline`; glossary PastDeadlineRejection. Child bump: server 0.13.0 (ping_handler 0.12.0) |
| 0.37.1 | 2026-11-23T00:00:00Z | Child bump: benchmark_runner 0.9.2 (config 0.22.0) |
| 0.37.2 | 2026-11-23T00:00:00Z | Child bump: benchmark_runner 0.9.3 (experiment_executor 0.13.0) |
| 0.38.0 | 2026-11-23T00:00:00Z | `lb_filter` value `deadline_mean_queue_plus_share` (glossary). Child bump: ingress 0.8.0 (load_balancer 0.11.0) |
| 0.39.0 | 2026-11-23T00:00:00Z | Ingress parameters `lb_retry_policy`, `lb_max_retries`; glossary LbRetry, RetryPolicy. Child bump: ingress 0.9.0 (retry_policy 0.1.0) |
| 0.39.1 | 2026-11-23T00:00:00Z | Child bump: ingress 0.9.1 (inflight_map 0.4.0) |
| 0.39.2 | 2026-11-23T00:00:00Z | Child bump: ingress 0.9.2 (load_balancer 0.12.0) |
| 0.39.3 | 2026-11-23T00:00:00Z | Child bump: ingress 0.9.3 (frontend 0.6.0) |
| 0.39.4 | 2026-11-23T00:00:00Z | Child bump: benchmark_runner 0.9.4 (config 0.23.0) |
| 0.40.0 | 2026-11-23T00:00:00Z | `lb_retry_policy = timeout`, `lb_retry_timeout_ms`. Child bump: ingress 0.10.0 (retry_policy 0.2.0) |
| 0.40.1 | 2026-11-23T00:00:00Z | Child bump: benchmark_runner 0.9.5 (config 0.24.0) |
| 0.40.2 | 2026-11-23T00:00:00Z | DeadlineFilter waits in the FilterWaitQueue instead of dropping at once (glossary). Child bump: ingress 0.10.1 (load_balancer 0.13.0) |
| 0.40.3 | 2026-11-23T00:00:00Z | Child bump: ingress 0.10.2 (frontend 0.7.0) |
| 0.40.4 | 2026-11-23T00:00:00Z | Glossary: EwmaAntiServiceTime, TicketSeq, QueueAtSend. Child bump: ingress 0.10.3 (load_balancer 0.14.0) |
| 0.41.0 | 2026-11-23T00:00:00Z | `lb_retry_policy = anxious`, `lb_retry_anxiety_ratio`; glossary AnxiousRetry, AnxiousTracker, RetryObservation. Child bump: ingress 0.11.0 (retry_policy 0.3.0) |
| 0.41.1 | 2026-11-23T00:00:00Z | Child bump: ingress 0.11.1 (frontend 0.8.0) |
| 0.41.2 | 2026-11-23T00:00:00Z | Child bump: benchmark_runner 0.9.6 (config 0.25.0) |
| 0.41.3 | 2026-11-24T00:00:00Z | Glossary ExcludedBackend. Child bump: ingress 0.11.2 (load_balancer 0.15.0) |
| 0.41.4 | 2026-11-24T00:00:00Z | Glossary RetryPolicy, AnxiousRetry, AnxiousTracker, RetryObservation. Child bump: ingress 0.11.3 (retry_policy 0.4.0) |
| 0.41.5 | 2026-11-24T00:00:00Z | Child bump: ingress 0.12.0 (frontend 0.9.0) |
| 0.42.0 | 2026-11-24T00:00:00Z | Glossary Availability: 1 s window. Child bump: benchmark_runner 0.10.0 (result_plotter 0.19.0) |
| 0.42.1 | 2026-11-24T00:00:00Z | Glossary AnxiousRetry (ExpectedLate). Child bump: ingress 0.12.1 (retry_policy 0.5.0) |
| 0.42.2 | 2026-11-24T00:00:00Z | Glossary AnxiousRetry, AnxiousTracker, EdfAhead. Child bump: ingress 0.12.2 (retry_policy 0.6.0) |
| 0.42.3 | 2026-11-24T00:00:00Z | Child bump: ingress 0.12.3 (frontend 0.10.0) |
| 0.43.0 | 2026-11-24T00:00:00Z | Parameter `lb_retry_late_factor`; glossary AnxiousRetry. Child bump: ingress 0.13.0 (retry_policy 0.7.0) |
| 0.43.1 | 2026-11-24T00:00:00Z | Child bump: benchmark_runner 0.10.1 (config 0.26.0) |
| 0.44.0 | 2026-11-24T00:00:00Z | Glossary AvailabilityPlot (log unavailability). Child bump: benchmark_runner 0.11.0 (result_plotter 0.20.0) |
| 0.45.0 | 2026-11-24T00:00:00Z | Glossary AvailabilityPlot (linear again). Child bump: benchmark_runner 0.12.0 (result_plotter 0.21.0) |
| 0.46.0 | 2026-11-24T00:00:00Z | Glossary Availability (send-time windows). Child bump: benchmark_runner 0.13.0 (result_plotter 0.22.0) |
| 0.46.1 | 2026-11-24T00:00:00Z | Glossary EstimatedFinish, EstimateError. Child bump: ingress 0.13.1 (load_balancer 0.16.0) |
| 0.47.0 | 2026-11-24T00:00:00Z | Glossary EstimateErrorHistogram. Child bump: benchmark_runner 0.14.0 (result_plotter 0.23.0) |
| 0.47.1 | 2026-11-24T00:00:00Z | Glossary EstimatedFinish (/ WorkerCount). Child bump: ingress 0.13.2 (load_balancer 0.17.0) |
| 0.47.2 | 2026-11-24T00:00:00Z | Child bump: benchmark_runner 0.14.1 (result_plotter 0.23.1) |
| 0.48.0 | 2026-11-24T00:00:00Z | Glossary EstimateErrorCdf (was EstimateErrorHistogram). Child bump: benchmark_runner 0.15.0 (result_plotter 0.24.0) |
| 0.48.1 | 2026-11-24T00:00:00Z | Child bump: ingress 0.13.3 (load_balancer 0.17.1) |
| 0.49.0 | 2026-11-25T00:00:00Z | Workload `sleep_constant`; glossary ConstantServiceTime, ServerWorkload. Child bump: server 0.14.0 (ping_handler 0.13.0) |
| 0.49.1 | 2026-11-25T00:00:00Z | Child bump: server 0.14.1 (sleeper 0.1.1) |
| 0.50.0 | 2026-11-27T00:00:00Z | Server parameters `service_scenario` and `scenario_log_path`; glossary ServiceScenario, ScenarioState, ScenarioSchedule, MarkovSchedule, ScenarioClock, StalledState, ScenarioLog. Child bump: server 0.15.0 (service_scenario 0.1.0, new) |
| 0.50.1 | 2026-11-27T00:00:00Z | Child bump: sampling 0.4.0 (stream kind `STREAM_SERVER_SCENARIO = 7`) |
