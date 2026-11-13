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
