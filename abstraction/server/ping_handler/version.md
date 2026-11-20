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
| 0.8.0 | 2026-11-13T00:00:00Z | `admit_ping` reads the RequestDeadline (`protocol.parse_deadline`; missing or malformed = `NO_DEADLINE = 2^64 - 1`, never a rejection) into `PingJob.deadline_unix_ns` |
| 0.9.0 | 2026-11-18T00:00:00Z | `workload = sleep`: `admit_ping` samples the ServiceTime as for `cpu_burn`; `run_ping` runs `sleeper.sleep` (cooperative: RST flag and CancelMatching at each wake-up; ignore: no checks); CancelCheckPoints table and Sampling order updated |
| 0.10.0 | 2026-11-19T00:00:00Z | WorkerFreeze: `run_ping` takes the WorkQueue and calls `worker_pool.freeze_wait` at check point 1 and at every check of check point 2 (cooperative: inside `id_check`; ignore: a `freeze_check` that never cancels, only when `freeze_after_ms` is set) |
| 0.11.0 | 2026-11-20T00:00:00Z | ExecReport: `run_ping` stamps `t0` (monotonic) and StartSlack (wall clock vs `deadline_unix_ns`, saturating i64; none for `NO_DEADLINE`) right after the pop, and every `202`/`503` body carries `exec_ns` measured when it is built; worked examples |
