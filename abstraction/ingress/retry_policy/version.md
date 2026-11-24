# Version — ingress/retry_policy

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-11-23T00:00:00Z | Initial spec: RetryPolicy `decide(state, event) -> Retry \| Wait` (pure, no side effects, only while budget remains); RetryEvent (`AttemptEnded(bad_gateway)`, `Tick`); RetryState with AttemptViews and BackendViews; RetryPolicyRegistry with `none`; Adding a RetryPolicy |
| 0.2.0 | 2026-11-23T00:00:00Z | `timeout` RetryPolicy (mimics the client RetryTimer: Retry on a Tick once the newest attempt has run `lb_retry_timeout_ms`; Wait on AttemptEnded); parameter `lb_retry_timeout_ms` (default 150); worked example |
| 0.3.0 | 2026-11-23T00:00:00Z | RetryObservation (optional `observe`/`stop`: `AttemptSent`, `AttemptFinished`, `EwmaChanged`); AttemptView `ticket_seq`, BackendView `ewma_anti_exec_ns`; `anxious` RetryPolicy (AnxiousRetry, AnxiousTracker; parameter `lb_retry_anxiety_ratio`, default 0.3; constants `ANXIOUS_TICK_MS`, `ANXIOUS_BUFFER`; worked examples) |
| 0.4.0 | 2026-11-24T00:00:00Z | RetryDecision `RetryElsewhere` (pick excludes the newest attempt's backend); RetryObservation `RetryRefused`; optional `attach(is_live)`; `anxious`: RetryExpFinish over the live backends other than the attempt's only (none: never anxious), decides `RetryElsewhere`, AnxiousEvaluation only after a stat or liveness change, a refused TicketSeq leaves the published set until the next evaluation; worked examples |
