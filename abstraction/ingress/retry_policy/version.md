# Version — ingress/retry_policy

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-11-23T00:00:00Z | Initial spec: RetryPolicy `decide(state, event) -> Retry \| Wait` (pure, no side effects, only while budget remains); RetryEvent (`AttemptEnded(bad_gateway)`, `Tick`); RetryState with AttemptViews and BackendViews; RetryPolicyRegistry with `none`; Adding a RetryPolicy |
| 0.2.0 | 2026-11-23T00:00:00Z | `timeout` RetryPolicy (mimics the client RetryTimer: Retry on a Tick once the newest attempt has run `lb_retry_timeout_ms`; Wait on AttemptEnded); parameter `lb_retry_timeout_ms` (default 150); worked example |
