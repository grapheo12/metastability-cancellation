# Version — worker_pool

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-11-10T00:00:00Z | Initial spec: PingJob, WorkQueue (unbounded MPMC FIFO, close drops queued jobs), WorkerPool of `n_worker_threads`, ordering, concurrency requirement |
| 0.2.0 | 2026-11-10T00:00:00Z | QueueLengthLog: `queue_len`, `queue_sampler` (drift-free deadlines, one row on a late wake, no row at stop), `sampler_stop`, worked example and edge cases; parameter `queue_sample_interval_ms` |
| 0.3.0 | 2026-11-10T00:00:00Z | StallGate: `queue_new(initial_stall_ms)`, the first push arms the gate, `queue_pop` waits until it opens; close wakes waiters; worked example and edge cases |
| 0.3.1 | 2026-11-10T00:00:00Z | Ordering: a client has many jobs at once, in any id order; CancelMatching no longer relies on per-client order. No behavior change |
| 0.4.0 | 2026-11-12T00:00:00Z | `pool_start` and `worker_loop` take the MessageBuffer (or NONE) and pass it to `run_ping` |
| 0.5.0 | 2026-11-13T00:00:00Z | QueueDiscipline `fifo` \| `edf`: `queue_new` takes the discipline; EarliestDeadlineFirst pops the smallest `(deadline_unix_ns, push_seq)` (retry copies tie on deadline and keep push order, no deadline = `2^64 - 1`); `PingJob.deadline_unix_ns`; Ordering and close order follow the discipline; worked example |
