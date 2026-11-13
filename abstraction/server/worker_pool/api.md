# Worker Pool — API

## Parameters

| Parameter | Type / unit / constraint | Default | Meaning |
|---|---|---|---|
| `n_worker_threads` | integer, `1 ..= 2^32-1` | `1` | Number of workers. Same as [../../api.md#server-parameters](../../api.md#server-parameters) |
| `initial_stall_ms` | integer, ms, `>= 0` | `1000` | StallGate: no job is popped until this long after the first push. `0`: no stall. Same as [../../api.md#server-parameters](../../api.md#server-parameters) |
| `queue_discipline` | `fifo` \| `edf` | `fifo` | QueueDiscipline of the WorkQueue: push order, or EarliestDeadlineFirst ([design.md#earliestdeadlinefirst](design.md#earliestdeadlinefirst)). Same as [../../api.md#server-parameters](../../api.md#server-parameters) |
| `queue_sample_interval_ms` | integer, ms, `>= 1` | `10` | Time between QueueLengthLog samples. Same as [../../api.md#server-parameters](../../api.md#server-parameters) |

## Types

### `PingJob`

One admitted `POST /ping`, ready to run. Built by the Connection Handler when it admits a request
([../connection_handler/design.md](../connection_handler/design.md)), consumed by exactly one worker.

| Field | Type | Meaning |
|---|---|---|
| `task` | reference to a `StreamTask` ([../connection_handler/api.md#streamtask](../connection_handler/api.md#streamtask)) | The request's stream. The worker reads only its `cancelled` flag (the RST flag) |
| `request_id` | u64 | The RequestId parsed from the body |
| `client_id` | u32 | `request_id >> 32`, equal to the connection's bound client id |
| `service_us` | u64, µs | The ServiceTime drawn for this request when it was admitted |
| `deadline_unix_ns` | u64, Unix ns | The RequestDeadline from the body, or `NO_DEADLINE = 2^64 - 1` if it had none. Read only by the `edf` QueueDiscipline |
| `reply` | one-shot slot of `Response` or `Aborted` | Filled exactly once, by the worker that ran the job or by `queue_close`. The stream waits on it |

See [design.md#pingjob](design.md#pingjob).

### `WorkQueue`

Process-wide unbounded queue of PingJobs, popped in push order (`fifo`) or by earliest RequestDeadline (`edf`), safe
for any number of concurrent `queue_push` and `queue_pop` callers.
See [design.md#workqueue](design.md#workqueue).

### `WorkerPool`

The set of `n_worker_threads` running workers. See [design.md#workerpool](design.md#workerpool).

## Functions

### `queue_new(initial_stall_ms: u64, discipline: fifo | edf) -> WorkQueue`
Creates an empty, open queue with the given QueueDiscipline, whose StallGate opens `initial_stall_ms` after the first push. Called once per process by the Listener
([../design.md#listener](../design.md#listener)). See [design.md#workqueue](design.md#workqueue).

### `queue_push(queue, job: PingJob) -> ok | Closed`
Adds `job`: at the tail (`fifo`) or by its `(deadline_unix_ns, push_seq)` key (`edf`). Never blocks. The first successful push arms the StallGate
([design.md#stallgate](design.md#stallgate)). Returns `Closed`, and leaves `job.reply` untouched, if the queue has
been closed. Called by Connection Handlers. See [design.md#workqueue](design.md#workqueue).

### `queue_pop(queue) -> PingJob | None`
Removes and returns the next job in the discipline's order, waiting while the queue is empty or the StallGate is not yet open, and the
queue is open. Returns `None` once the queue is
closed. Called by workers only. See [design.md#workqueue](design.md#workqueue).

### `queue_close(queue) -> nothing`
Closes the queue, fills `reply` of every job still queued with `Aborted`, and wakes every waiting worker.
Idempotent. Called by the Listener at shutdown. See [design.md#workqueue](design.md#workqueue).

### `queue_len(queue) -> u64`
Number of jobs waiting in the queue (pushed, not yet popped). `0` once closed. Takes the queue lock briefly; never
waits for a job. See [design.md#queuelengthlog](design.md#queuelengthlog).

### `queue_sampler(queue, log: writable file, interval_ms: u64) -> nothing`
Runs on its own activity until `sampler_stop` is called. Writes the QueueLengthLog CSV header and then one row per
sample, in the format of [../api.md#queuelengthlog-output](../api.md#queuelengthlog-output), and closes `log` when it
returns. Started by the Listener only when `queue_log_path` is set. See [design.md#queuelengthlog](design.md#queuelengthlog).

### `sampler_stop(sampler) -> nothing`
Asks the sampler to stop and waits until it has returned and closed its file. Writes no extra row. Called by the
Listener at shutdown. See [design.md#queuelengthlog](design.md#queuelengthlog).

### `pool_start(n_worker_threads: u32, queue, config: ServerConfig, registry: CancelRegistry, counters: CancelCounters, buf: MessageBuffer | NONE) -> WorkerPool`
Starts the workers. `buf` is the process's MessageBuffer when `workload = message_buffer`, else `NONE`; workers
only pass it on to the Ping Handler. Called once per process by the Listener after `queue_new`. See
[design.md#workerpool](design.md#workerpool).

### `pool_join(pool) -> nothing`
Returns once every worker has returned. Workers return only after `queue_close`. See
[design.md#workerpool](design.md#workerpool).

## Lifecycle

- **Start:** `queue_new`, then `pool_start`, before the Listener accepts its first connection.
- **Run:** after the StallGate opens, workers run PingJobs in push order, up to `n_worker_threads` at a time.
- **Sampler:** if started, `queue_sampler` runs from just after `listening on` until `sampler_stop`, which the
  Listener calls before `queue_close`.
- **Stop:** `queue_close` drops the queued jobs (each gets `Aborted`). A burn that is running is not interrupted:
  its worker fills `reply` when the burn ends, then sees `None` and returns. `pool_join` waits for that.

## Uses

- [../ping_handler/api.md](../ping_handler/api.md): the function that runs one admitted `/ping` (CancelCheckPoints, burn, response)
- [../message_buffer/api.md](../message_buffer/api.md): `MessageBuffer` (passed through, never used directly)
- [../connection_handler/api.md](../connection_handler/api.md): `StreamTask`, `Response`
- [../cancel_tracker/api.md](../cancel_tracker/api.md): `CancelRegistry`, `CancelCounters`, passed through to the Ping Handler
