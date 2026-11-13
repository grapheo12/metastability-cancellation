# Version — load_generator

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-10-30T00:00:00Z | Initial spec: structure, time base, concurrency, phases |
| 0.1.1 | 2026-10-30T00:00:00Z | Style: description opens with subcomponents; `LoadGenConfig` under Types with design link. Children: client, metrics, run_controller 0.1.1. No behavior change |
| 0.2.0 | 2026-11-10T00:00:00Z | Parameter `n_waiting_requests` (default 100); description, structure table and concurrency for the SemiOpenLoopClient. Child bump: client 0.3.0 |
| 0.2.1 | 2026-11-10T00:00:00Z | Phases: DrainPhase wording. Child bump: run_controller 0.2.0 |
| 0.2.2 | 2026-11-12T00:00:00Z | Child bump: metrics 0.2.0 (`run_start_unix_ns`) |
| 0.2.3 | 2026-11-12T00:00:00Z | Time base: `run_start_unix_ns` reported. Child bump: run_controller 0.3.0 |
| 0.3.0 | 2026-11-12T00:00:00Z | Child bump: client 0.4.0 (RetryTimer, outcome `dropped`) |
| 0.3.1 | 2026-11-12T00:00:00Z | Parameters `retry_timeout_ms` (default 150) and `max_retries` (default 1), derived `retry_timeout_ns`; description introduces RequestCopy and RetryTimer; structure table and DrainPhase wording |
| 0.3.2 | 2026-11-12T00:00:00Z | Child bump: metrics 0.3.0 (`copies`, `dropped`, `copies_sent`) |
| 0.3.3 | 2026-11-12T00:00:00Z | Child bump: run_controller 0.4.0 (retry parameter validation) |
| 0.4.0 | 2026-11-12T00:00:00Z | Child bump: client 0.5.0 (StallWindow) |
| 0.4.1 | 2026-11-12T00:00:00Z | Child bump: metrics 0.4.0 (StallWindow excluded) |
| 0.4.2 | 2026-11-12T00:00:00Z | Child bump: run_controller 0.5.0 (`initial_stall_ms` validation) |
| 0.4.3 | 2026-11-12T00:00:00Z | Parameter `initial_stall_ms` (default 1000) and derived `initial_stall_ns`; description introduces the StallWindow |
| 0.5.0 | 2026-11-13T00:00:00Z | `request_bytes >= 76` (PingRequestBody). Child bump: client 0.6.0 (RequestDeadline) |
| 0.5.1 | 2026-11-13T00:00:00Z | Time base: `run_start_unix_ns` is also the RequestDeadline origin. Child bump: run_controller 0.6.0 |
