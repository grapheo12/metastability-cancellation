# Version — server/ping_handler

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-10-30T00:00:00Z | Initial spec: handle_ping, sampling order, cancel-mode behavior |
| 0.2.0 | 2026-10-30T00:00:00Z | Client binding, CancelCheckPoints (RST + `/cancel`), 503 on `/cancel` match, `last_completed_id`, counters |
| 0.2.1 | 2026-10-30T00:00:00Z | API style: Functions section. No behavior change |
| 0.3.0 | 2026-11-02T00:00:00Z | FinishedWatermark: `finish()` publishes the id on every outcome (`202`, `503`, `Aborted`) of a parsed, binding-matched request, in both modes |
| 0.4.0 | 2026-11-10T00:00:00Z | Shared workers: `handle_ping` split into `admit_ping` (Connection Handler: parse, bind, sample, build PingJob) and `run_ping` (worker: CancelCheckPoints, burn, finish). Client state reached through the registry slot; `last_completed_id` is a max-update raised by `finish(true)` |
