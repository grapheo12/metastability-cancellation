# Version — server/ping_handler

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-10-30T00:00:00Z | Initial spec: handle_ping, sampling order, cancel-mode behavior |
| 0.2.0 | 2026-10-30T00:00:00Z | Client binding, CancelCheckPoints (RST + `/cancel`), 503 on `/cancel` match, `last_completed_id`, counters |
| 0.2.1 | 2026-10-30T00:00:00Z | API style: Functions section. No behavior change |
| 0.3.0 | 2026-11-02T00:00:00Z | FinishedWatermark: `finish()` publishes the id on every outcome (`202`, `503`, `Aborted`) of a parsed, binding-matched request, in both modes |
| 0.4.0 | 2026-11-10T00:00:00Z | Shared workers: `handle_ping` split into `admit_ping` (Connection Handler: parse, bind, sample, build PingJob) and `run_ping` (worker: CancelCheckPoints, burn, finish). Client state reached through the registry slot; `last_completed_id` is a max-update raised by `finish(true)` |
| 0.5.0 | 2026-11-10T00:00:00Z | `run_ping` calls `registry_start` when a worker starts a request and passes `counters` to `finish(completed)`; the `last_completed_id` section is removed; outcome table shows what a later `/cancel` counts |
| 0.6.0 | 2026-11-12T00:00:00Z | MessageWork for `workload = message_buffer`: create messages with checks after each, then push_messages; `run_ping` takes the MessageBuffer; ServiceTime drawn only for `cpu_burn`; worked example |
| 0.7.0 | 2026-11-12T00:00:00Z | MessageWork creates MessageTrees with `make_tree` from a per-request Rng derived from the seed and the RequestId (STREAM_SERVER_MESSAGES); worked example updated |
