# Version — load_generator/client

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-10-30T00:00:00Z | Initial spec: Poisson schedule, GatedArrival, send_one, SLOCancellation, classification |
| 0.1.1 | 2026-10-30T00:00:00Z | API style: Types / Functions sections, design links for `Outcome` and `run_client`. No behavior change |
| 0.3.0 | 2026-11-10T00:00:00Z | SemiOpenLoopClient replaces ClosedLoopPoissonClient: sender and receiver activities, up to `n_waiting_requests` outstanding, GatedArrival on the cap, `start_request` and `settle` (first event wins), worked example, per-request state machine; connection error fails every outstanding request |
| 0.4.0 | 2026-11-12T00:00:00Z | RetryTimer: each RequestCopy has a `retry_timeout_ms` timer that sends the next copy (same RequestId, new stream, earlier copies ignored and not reset) up to `max_retries` times, then drops the request (`dropped`, no reset); SLOCancellation runs from the first send and resets every copy; RequestRecord gets `copies`; worked examples; state machine |
