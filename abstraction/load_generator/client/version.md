# Version — load_generator/client

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-10-30T00:00:00Z | Initial spec: Poisson schedule, GatedArrival, send_one, SLOCancellation, classification |
| 0.1.1 | 2026-10-30T00:00:00Z | API style: Types / Functions sections, design links for `Outcome` and `run_client`. No behavior change |
| 0.3.0 | 2026-11-10T00:00:00Z | SemiOpenLoopClient replaces ClosedLoopPoissonClient: sender and receiver activities, up to `n_waiting_requests` outstanding, GatedArrival on the cap, `start_request` and `settle` (first event wins), worked example, per-request state machine; connection error fails every outstanding request |
| 0.4.0 | 2026-11-12T00:00:00Z | RetryTimer: each RequestCopy has a `retry_timeout_ms` timer that sends the next copy (same RequestId, new stream, earlier copies ignored and not reset) up to `max_retries` times, then drops the request (`dropped`, no reset); SLOCancellation runs from the first send and resets every copy; RequestRecord gets `copies`; worked examples; state machine |
| 0.5.0 | 2026-11-12T00:00:00Z | StallWindow: a request first sent before `initial_stall_ns` (a StallWindowRequest) sends only copy 0 and never starts a RetryTimer, keeps SLOCancellation, and is flagged `in_stall_window` in its RequestRecord; worked examples |
| 0.6.0 | 2026-11-13T00:00:00Z | RequestDeadline: `start_request` computes `deadline_unix_ns = run_start_unix_ns + t_send + SLO` once and builds one PingRequestBody that every RequestCopy reuses, so retries carry the original deadline; `run_client` takes `run_start_unix_ns`; worked example |
| 0.7.0 | 2026-11-17T00:00:00Z | RetryTimer: the last RequestCopy (copy `max_retries`) has no RetryTimer and waits for its answer or SLOCancellation, so no request ends `dropped` (outcome kept, reserved); `max_retries = 0` means one copy with no RetryTimer; worked examples and state machine updated |
| 0.8.0 | 2026-11-20T00:00:00Z | `settle` reads the ExecReport (`parse_exec_report`) of the deciding `ok`/`rejected` response into the RequestRecord |
| 0.9.0 | 2026-11-20T00:00:00Z | `slo_cancel = false`: no SLO timer; new DrainDeadline (per-request timer at `D + DRAIN`, same settle/reset path, outcome `cancelled`); late answers stay `ok`; worked examples; state machine and description |
| 0.10.0 | 2026-11-27T00:00:00Z | RequestDeadlineHeader on every copy (`x-request-deadline`, from `req.deadline_unix_ns`); RequestDeadline stated as first-send wall clock + SLO (reading the wall clock at the send is equivalent); ArrivalRng: `arrival_rng = sampling` (derive_seed streams) or `native` (OptionalValue: implementation generator seeded `seed + client index`) |
