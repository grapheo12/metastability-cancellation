# Version — ingress/retry_policy

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-11-23T00:00:00Z | Initial spec: RetryPolicy `decide(state, event) -> Retry \| Wait` (pure, no side effects, only while budget remains); RetryEvent (`AttemptEnded(bad_gateway)`, `Tick`); RetryState with AttemptViews and BackendViews; RetryPolicyRegistry with `none`; Adding a RetryPolicy |
