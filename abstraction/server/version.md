# Version — server

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-10-30T00:00:00Z | Initial spec: listener, config validation, cancellation semantics, shutdown |
| 0.2.0 | 2026-10-30T00:00:00Z | `/cancel` endpoint, new Cancel Tracker subcomponent, CancelCounters output, `/cancel` cancellation semantics, connection binding to client id. Children: connection_handler, ping_handler, cpu_burner 0.2.0; cancel_tracker 0.1.0 |
| 0.2.1 | 2026-10-30T00:00:00Z | API style: `ServerConfig` under Types. Children: cancel_tracker 0.1.1, connection_handler 0.2.1, cpu_burner 0.2.1, ping_handler 0.2.1. No behavior change |
| 0.2.2 | 2026-11-02T00:00:00Z | Child bump: cancel_tracker 0.2.0. Cancellation semantics: every `/cancel` `202` carries the FinishedWatermark |
| 0.2.3 | 2026-11-02T00:00:00Z | Child bump: ping_handler 0.3.0 |
| 0.3.0 | 2026-11-10T00:00:00Z | Shared WorkQueue and WorkerPool: new parameter `n_worker_threads`, new child worker_pool 0.1.0, structure, Listener, concurrency model, cancellation semantics and shutdown for the shared queue; `/cancel` answered inline, ServiceTime drawn at admission. "Why one shared WorkQueue" |
| 0.3.1 | 2026-11-10T00:00:00Z | Child bump: cancel_tracker 0.3.0 (ClientCancelState under a per-client lock, max-updates, overlap and ordering) |
| 0.3.2 | 2026-11-10T00:00:00Z | Child bump: ping_handler 0.4.0 (admit_ping / run_ping) |
| 0.3.3 | 2026-11-10T00:00:00Z | Child bump: connection_handler 0.3.0 (admission onto the WorkQueue, `/cancel` inline) |
| 0.4.0 | 2026-11-10T00:00:00Z | Parameters `queue_log_path` and `queue_sample_interval_ms`; QueueLengthLog output (CSV `t_ms,queue_len`); Listener starts the sampler, Shutdown stops it before closing the WorkQueue; lifecycle errors. Child bump: worker_pool 0.2.0 |
| 0.5.0 | 2026-11-10T00:00:00Z | Parameter `initial_stall_ms` (default 1000): validation, Listener passes it to `queue_new`, wire behavior, RST-during-stall row, shutdown note. Child bump: worker_pool 0.3.0 |
| 0.5.1 | 2026-11-10T00:00:00Z | WorkQueue rationale for many requests per client. Child bump: cancel_tracker 0.4.0 |
| 0.5.2 | 2026-11-10T00:00:00Z | Child bump: ping_handler 0.5.0 |
| 0.5.3 | 2026-11-10T00:00:00Z | Child bump: worker_pool 0.3.1 |
| 0.6.0 | 2026-11-12T00:00:00Z | ServerWorkload `workload` (`cpu_burn` \| `message_buffer`); parameters `window_size`, `message_bytes`, `n_messages_per_request`; `response_rate` required only for `cpu_burn`; Listener fills the MessageBuffer before bind; MessageBuffer lock rules. New child message_buffer 0.1.0 |
| 0.6.1 | 2026-11-12T00:00:00Z | Child bump: worker_pool 0.4.0 |
| 0.6.2 | 2026-11-12T00:00:00Z | Child bump: ping_handler 0.6.0 |
| 0.7.0 | 2026-11-12T00:00:00Z | ProcessStart: the first stdout line is `process_start_unix_ns <u64>` (wall clock, before validation), so process-relative GcTrace times can be placed on a shared clock |
| 0.7.1 | 2026-11-12T00:00:00Z | Parameter `n_gc_binary_tree_size` (`1 ..= 65536`, default 15) replaces `message_bytes`; the Listener passes the seed to `buffer_new`. Child bump: message_buffer 0.2.0 |
| 0.7.2 | 2026-11-12T00:00:00Z | Child bump: ping_handler 0.7.0 |
| 0.7.3 | 2026-11-12T00:00:00Z | Child bump: message_buffer 0.2.1 |
| 0.8.0 | 2026-11-13T00:00:00Z | Parameter `queue_discipline` (`fifo` \| `edf`, default `fifo`), validated and passed to `queue_new`; wire behavior names both start orders. Child bump: worker_pool 0.5.0 (EarliestDeadlineFirst) |
| 0.8.1 | 2026-11-13T00:00:00Z | Child bump: ping_handler 0.8.0 (RequestDeadline into the PingJob) |
