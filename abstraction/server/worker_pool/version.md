# Version — worker_pool

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-11-10T00:00:00Z | Initial spec: PingJob, WorkQueue (unbounded MPMC FIFO, close drops queued jobs), WorkerPool of `n_worker_threads`, ordering, concurrency requirement |
| 0.2.0 | 2026-11-10T00:00:00Z | QueueLengthLog: `queue_len`, `queue_sampler` (drift-free deadlines, one row on a late wake, no row at stop), `sampler_stop`, worked example and edge cases; parameter `queue_sample_interval_ms` |
