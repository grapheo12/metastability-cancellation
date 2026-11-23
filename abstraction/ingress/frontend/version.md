# Version — ingress/frontend

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-11-02T00:00:00Z | Initial spec: client connections, ForwardTask (pick, publish, forward, clear before relay, finish), reset propagation, StartOnce, state machine |
| 0.2.0 | 2026-11-10T00:00:00Z | InFlightMap publish/clear by entry (keyed by RequestId); many concurrent ForwardTasks per client |
| 0.2.1 | 2026-11-11T00:00:00Z | ForwardTask keeps the ForwardTicket from `pick` and gives it to `finish` (Load Balancer 0.2.0) |
| 0.2.2 | 2026-11-12T00:00:00Z | StartOnce also records `run_start_unix_ns` (wall clock) for the summary |
| 0.3.0 | 2026-11-20T00:00:00Z | ForwardTask: on `ok`/`rejected`, read `exec_ns` from the relayed body (`parse_exec_report`) and pass it to `finish`; body still relayed unchanged |
| 0.4.0 | 2026-11-20T00:00:00Z | DeadlineFilter: `handle_ping` passes `remaining_ns` (RequestDeadline minus the wall clock) to `pick`; on `Dropped` answers `503 {"id"}` without forwarding; state machine and table |
| 0.5.0 | 2026-11-20T00:00:00Z | ForwardTask: for relayed `202`/`503`, reads the WorkerCount header (`parse_worker_count`) and passes it to `finish`; not relayed |
| 0.6.0 | 2026-11-23T00:00:00Z | LB retries: ForwardTask runs ForwardAttempts (own pick with `attempt`, InFlightEntry replacement, clear+finish per attempt in its own activity) and an LbRetryLoop (newest answer counts; RetryPolicy on the newest 502 and every `LB_RETRY_TICK_MS = 10`; budget `lb_max_retries`; client reset cancels every running attempt); identical to before with `none` or budget 0; worked examples |
| 0.7.0 | 2026-11-23T00:00:00Z | Attempt 0 goes through `admit` (FilterWaitQueue): sent at once, or waits until a scan sends it or drops it past its deadline (`503`); a client reset while waiting marks it `gone`; a refused LbRetry counts `lb_retry_refused`; state machine and table |
