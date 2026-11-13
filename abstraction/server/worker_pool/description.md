# Worker Pool — Description

Leaf component. It runs the server's `/ping` work, separately from the connections the requests arrived on:

- **WorkQueue**: one process-wide, unbounded, multi-producer, multi-consumer queue of **PingJobs**, popped by its
  **QueueDiscipline**: in push order (`fifo`) or by earliest RequestDeadline (**EarliestDeadlineFirst**, `edf`). Every
  Connection Handler pushes onto it. A push never blocks and never fails while the server is running.
- **WorkerPool**: `n_worker_threads` workers. Each one repeatedly takes the next PingJob off the WorkQueue, runs it
  through the Ping Handler (CancelCheckPoints and the CPU burn), and hands the result back to the job's stream.
- **StallGate**: holds every worker back for `initial_stall_ms` after the first admitted `/ping`, so a backlog
  builds before any request runs.
- **QueueLengthLog**: an optional sampler that records the WorkQueue length (jobs waiting, not running) every
  `queue_sample_interval_ms` into a CSV, so a run's backlog can be plotted over time.

`/cancel` requests never enter the WorkQueue. Connection Handlers answer them directly, so a cancel never waits
behind the work it targets.

Why it exists: the server models a CPU-bound service with a fixed number of cores. All clients share one line
and a fixed number of workers, so abandoned work (`ignore` mode) delays every request queued behind it.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
