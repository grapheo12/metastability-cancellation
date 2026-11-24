# Version — benchmark_runner/config

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-10-31T00:00:00Z | Initial spec: config file schema, BenchConfig validation, SweepExpansion, argument formatting |
| 0.2.0 | 2026-11-02T00:00:00Z | ProcessPlacement runner keys (`threads`, `cpu_set`) and SweepPoint `placement`. CancelRateFraction (`canceller.cancel_rate_fraction`, sweepable, derives `cancel_rate = global_rate × fraction`) |
| 0.2.1 | 2026-11-02T00:00:00Z | `incr_rand_max` added to the canceller program parameters (settable and sweepable) |
| 0.2.2 | 2026-11-02T00:00:00Z | Machine tables: `user`, `ssh_key`, `ssh_port` optional (from `~/.ssh/config`); `data_addr` default via `ssh_hostname`. `validate_machine` pseudocode and table |
| 0.3.0 | 2026-11-02T00:00:00Z | `[ingress]` replaces `[canceller]` (program params lb_policy, cancel_rate, duration_s, seed; runner keys listen_port, cancel_rate_fraction, placement). `[server] instances` (instance k on listen_port + k). SweepPoint: `ingress_args` (with runner-filled `backends`, `num_clients`), `server_instances`; `incr_rand_max` removed |
| 0.4.0 | 2026-11-03T00:00:00Z | IngressPlacement: runner key `[ingress] machine` (client or server); SweepPoint `ingress_machine` and `loadgen_target`; backends local when the Ingress is on the server machine; port-collision check |
| 0.5.0 | 2026-11-10T00:00:00Z | Server program parameter `n_worker_threads` (settable and sweepable); ProcessPlacement says how it differs from `threads`, with examples |
| 0.6.0 | 2026-11-10T00:00:00Z | Server: `queue_log_path` is set by the runner (forbidden in the config); `queue_sample_interval_ms` is a settable, sweepable program parameter |
| 0.7.0 | 2026-11-10T00:00:00Z | `initial_stall_ms` is a settable, sweepable program parameter of the server and of the Ingress |
| 0.8.0 | 2026-11-10T00:00:00Z | `n_waiting_requests` is a settable, sweepable load generator parameter |
| 0.9.0 | 2026-11-11T00:00:00Z | `ewma_beta` is a settable, sweepable program parameter of the Ingress |
| 0.10.0 | 2026-11-12T00:00:00Z | Server program parameters `workload`, `window_size`, `message_bytes`, `n_messages_per_request` are settable and sweepable |
| 0.11.0 | 2026-11-12T00:00:00Z | Runner key `[server] gc_trace` (boolean, default false, not sweepable) |
| 0.12.0 | 2026-11-12T00:00:00Z | Server program parameters: `n_gc_binary_tree_size` replaces `message_bytes` (an old config setting `message_bytes` is now an unknown key) |
| 0.13.0 | 2026-11-12T00:00:00Z | Load generator program parameters `retry_timeout_ms` and `max_retries` (settable and sweepable) |
| 0.14.0 | 2026-11-12T00:00:00Z | Load generator program parameter `initial_stall_ms`; `expand_sweep` copies the point's `server.initial_stall_ms` to the load generator unless `load_gen` sets it |
| 0.15.0 | 2026-11-13T00:00:00Z | Server program parameter `queue_discipline` (settable and sweepable) |
| 0.16.0 | 2026-11-18T00:00:00Z | LinkedSweep: `[run] link` maps a linked array parameter to a swept driver key; the linked key takes the driver's index at each point, is not a sweep dimension and is recorded in the new SweepPoint field `linked`; validation errors and worked examples |
| 0.17.0 | 2026-11-19T00:00:00Z | ServerMachines: `[machines.server]` may be an array of 1..=16 distinct tables; `instances` is per server machine; global instance ids (machine by machine), Backends across machines, new SweepPoint field `server_hosts`; `ingress.machine = "server"` needs one server machine; validation errors and worked examples |
| 0.18.0 | 2026-11-19T00:00:00Z | FreezeTarget: runner-owned `[server] freeze_instance` (in range, needs `freeze_after_ms`, cannot be swept); `freeze_after_ms` is a server program parameter; with a FreezeTarget it leaves `server_args` for `SweepPoint.freeze` |
| 0.19.0 | 2026-11-20T00:00:00Z | PROGRAM_PARAMS: server `heartbeat_interval_ms` (Heartbeat), Ingress `heartbeat_timeout_ms` (BackendLiveness); passed through like any program parameter |
| 0.20.0 | 2026-11-20T00:00:00Z | PROGRAM_PARAMS: load_gen `slo_cancel` and `drain_timeout_ms` (DrainDeadline); passed through like any program parameter (booleans as `true`/`false`) |
| 0.21.0 | 2026-11-20T00:00:00Z | PROGRAM_PARAMS: Ingress `lb_filter` (DeadlineFilter) |
| 0.22.0 | 2026-11-23T00:00:00Z | PROGRAM_PARAMS: server `reject_past_deadline` (PastDeadlineRejection) |
| 0.23.0 | 2026-11-23T00:00:00Z | PROGRAM_PARAMS: Ingress `lb_retry_policy`, `lb_max_retries` (LbRetries) |
| 0.24.0 | 2026-11-23T00:00:00Z | PROGRAM_PARAMS: Ingress `lb_retry_timeout_ms` (`timeout` RetryPolicy) |
| 0.25.0 | 2026-11-23T00:00:00Z | PROGRAM_PARAMS: Ingress `lb_retry_anxiety_ratio` (`anxious` RetryPolicy) |
| 0.26.0 | 2026-11-24T00:00:00Z | PROGRAM_PARAMS: Ingress `lb_retry_late_factor` (`anxious` ExpectedLate factor) |
