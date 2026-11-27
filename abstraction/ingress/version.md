# Version — ingress

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-11-02T00:00:00Z | New component: parameters, BackendStats, outputs, lifecycle, shutdown, time base. Children: inflight_map 0.1.0 |
| 0.1.1 | 2026-11-02T00:00:00Z | Child added: load_balancer 0.1.0 |
| 0.1.2 | 2026-11-02T00:00:00Z | Child added: upstream_pool 0.1.0 |
| 0.1.3 | 2026-11-02T00:00:00Z | Child added: load_canceller 0.3.0 (moved from the root) |
| 0.1.4 | 2026-11-02T00:00:00Z | Child added: frontend 0.1.0 |
| 0.1.5 | 2026-11-02T00:00:00Z | Parameters restated from the root `api.md` (Ingress parameters) |
| 0.1.6 | 2026-11-04T00:00:00Z | InFlightMap key-space wording; child bump: inflight_map 0.2.0 (library-backed lock-free map contract) |
| 0.1.7 | 2026-11-10T00:00:00Z | The server binds connections but no longer serves them serially (shared WorkQueue): design rationale and Uses wording. Child bump: upstream_pool 0.1.1. No behavior change |
| 0.1.8 | 2026-11-10T00:00:00Z | Parameter `initial_stall_ms` (default 1000) restated and validated. Child bump: load_canceller 0.4.0 |
| 0.1.9 | 2026-11-10T00:00:00Z | Many forwards per client at once; InFlightMap keyed by RequestId. Child bump: inflight_map 0.3.0 |
| 0.1.10 | 2026-11-10T00:00:00Z | Child bump: frontend 0.2.0 |
| 0.1.11 | 2026-11-10T00:00:00Z | Child bump: load_canceller 0.5.0 |
| 0.1.12 | 2026-11-10T00:00:00Z | Child bump: upstream_pool 0.1.2 |
| 0.1.13 | 2026-11-11T00:00:00Z | LbPolicy `ewma` and parameter `ewma_beta`; BackendStats and summary `backends` gain `ewma_latency_ns`. Child bump: load_balancer 0.2.0 |
| 0.1.14 | 2026-11-11T00:00:00Z | Child bump: frontend 0.2.1 |
| 0.1.15 | 2026-11-12T00:00:00Z | New output `<output_path>.forwards.csv` (the ForwardLog); shared state and lock note. Child bump: load_balancer 0.3.0 |
| 0.1.16 | 2026-11-12T00:00:00Z | `run_start_unix_ns` (wall clock of `run_start`) in the summary, for aligning with other processes' times |
| 0.1.17 | 2026-11-12T00:00:00Z | Child bump: frontend 0.2.2 |
| 0.2.0 | 2026-11-20T00:00:00Z | New subcomponent Heartbeat Monitor 0.1.0; parameter `heartbeat_timeout_ms` (validation `>= 0`, default 0 = off); started before `listening on`, stopped at shutdown; output `<output_path>.liveness.csv`; Structure, Shared state, Concurrency model |
| 0.2.1 | 2026-11-20T00:00:00Z | Child bump: load_balancer 0.4.0 (LiveSet) |
| 0.3.0 | 2026-11-20T00:00:00Z | BackendStats `ewma_exec_ns`; `forwards.csv` columns `exec_ns`, `ewma_exec_ns`; summary `ewma_exec_ns` (`null` before a sample). Child bump: load_balancer 0.5.0 |
| 0.3.1 | 2026-11-20T00:00:00Z | Uses `parse_exec_report`. Child bump: frontend 0.3.0 |
| 0.4.0 | 2026-11-20T00:00:00Z | Parameter `lb_filter` (`none` default, `deadline`), validation; summary `filter_dropped`. Child bump: load_balancer 0.6.0 |
| 0.4.1 | 2026-11-20T00:00:00Z | Child bump: frontend 0.4.0 (deadline-aware pick, filter drops) |
| 0.5.0 | 2026-11-20T00:00:00Z | BackendStats and summary `worker_count`. Child bump: load_balancer 0.7.0 |
| 0.5.1 | 2026-11-20T00:00:00Z | Child bump: frontend 0.5.0 (WorkerCount to finish) |
| 0.5.2 | 2026-11-21T00:00:00Z | `lb_filter` text: on-time probability >= 0.99. Child bump: load_balancer 0.8.0 |
| 0.6.0 | 2026-11-21T00:00:00Z | New output `<output_path>.picks.csv` (the PickLog), shared state and shutdown. Child bump: load_balancer 0.9.0 |
| 0.7.0 | 2026-11-21T00:00:00Z | `lb_filter` values `deadline_mean`, `deadline_mean_per_worker`, `deadline_mmk` (validation; `deadline` removed). Child bump: load_balancer 0.10.0 |
| 0.8.0 | 2026-11-23T00:00:00Z | `lb_filter` value `deadline_mean_queue_plus_share`. Child bump: load_balancer 0.11.0 |
| 0.9.0 | 2026-11-23T00:00:00Z | New subcomponent Retry Policy 0.1.0; parameters `lb_retry_policy` (default `none`) and `lb_max_retries` (default 0), validation; summary `lb_retries` |
| 0.9.1 | 2026-11-23T00:00:00Z | Child bump: inflight_map 0.4.0 (`attempt`) |
| 0.9.2 | 2026-11-23T00:00:00Z | `forwards.csv` column `attempt` (last). Child bump: load_balancer 0.12.0 |
| 0.9.3 | 2026-11-23T00:00:00Z | Child bump: frontend 0.6.0 (ForwardAttempt, LbRetryLoop) |
| 0.10.0 | 2026-11-23T00:00:00Z | `lb_retry_policy` value `timeout`; parameter `lb_retry_timeout_ms`, validation. Child bump: retry_policy 0.2.0 |
| 0.10.1 | 2026-11-23T00:00:00Z | Summary `filter_queued`, `filter_sent_from_queue`, `filter_queue_max`, `lb_retry_refused`; `filter_dropped` now means dropped past the deadline while waiting; `lb_filter` text. Child bump: load_balancer 0.13.0 |
| 0.10.2 | 2026-11-23T00:00:00Z | Child bump: frontend 0.7.0 (wait in the FilterWaitQueue) |
| 0.10.3 | 2026-11-23T00:00:00Z | BackendStats and summary `ewma_anti_exec_ns`. Child bump: load_balancer 0.14.0 |
| 0.11.0 | 2026-11-23T00:00:00Z | `lb_retry_policy` value `anxious`; parameter `lb_retry_anxiety_ratio`, validation. Child bump: retry_policy 0.3.0 |
| 0.11.1 | 2026-11-23T00:00:00Z | Shutdown calls `retry_policy.stop`. Child bump: frontend 0.8.0 (RetryObservations) |
| 0.11.2 | 2026-11-24T00:00:00Z | Child bump: load_balancer 0.15.0 (ExcludedBackend) |
| 0.11.3 | 2026-11-24T00:00:00Z | Child bump: retry_policy 0.4.0 (RetryElsewhere, RetryRefused, attach; anxious live-only, evaluate on change) |
| 0.12.0 | 2026-11-24T00:00:00Z | Shared state: `retry_policy`, attached to the Heartbeat Monitor's `is_live` at startup. Child bump: frontend 0.9.0 (RetryElsewhere, RetryRefused) |
| 0.12.1 | 2026-11-24T00:00:00Z | Child bump: retry_policy 0.5.0 (anxious ExpectedLate) |
| 0.12.2 | 2026-11-24T00:00:00Z | Child bump: retry_policy 0.6.0 (anxious EdfAhead) |
| 0.12.3 | 2026-11-24T00:00:00Z | Child bump: frontend 0.10.0 (AttemptSent deadline) |
| 0.13.0 | 2026-11-24T00:00:00Z | Parameter `lb_retry_late_factor` (default 10), validation. Child bump: retry_policy 0.7.0 |
| 0.13.1 | 2026-11-24T00:00:00Z | `forwards.csv` column `estimate_ns`. Child bump: load_balancer 0.16.0 |
| 0.13.2 | 2026-11-24T00:00:00Z | `forwards.csv` `estimate_ns` text. Child bump: load_balancer 0.17.0 |
| 0.13.3 | 2026-11-24T00:00:00Z | Child bump: load_balancer 0.17.1 (wording) |
| 0.14.0 | 2026-11-27T00:00:00Z | Validation of `peak_ewma` / `deadline_provisional_rtt` (each needs the other or `none`) and of OptionalValues; Ingress workers in the concurrency model; parameter rows. Child bump: load_balancer 0.18.0 |
| 0.15.0 | 2026-11-27T00:00:00Z | Parameter `forward_mode` (`relay` \| `hedge`, OptionalValue) and its validation. Child bump: frontend 0.11.0 (HedgedForwarding); load_balancer link to it |
| 0.15.1 | 2026-11-27T00:00:00Z | Child bump: load_balancer 0.18.1 (wording) |
| 0.16.0 | 2026-11-27T00:00:00Z | Child bump: retry_policy 0.8.0 (policies under HedgedForwarding) |
| 0.16.1 | 2026-11-27T00:00:00Z | forwards.csv columns for a ForwardLog built from an access log. Child bump: load_balancer 0.18.2 |
| 0.17.0 | 2026-11-27T00:00:00Z | Parameter `lb_filter_margin` and its validation. Child bump: load_balancer 0.19.0 |
