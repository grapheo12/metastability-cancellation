# Worker Pool — Design

## PingJob

```
PingJob: { task, request_id: u64, client_id: u32, service_us: u64, deadline_unix_ns: u64, reply: OneShot }

OneShot: a slot written exactly once and read by one waiter
    put(value)      # first and only write; never blocks
    take() -> value # blocks until put has happened
```

A PingJob is created only for a `POST /ping` whose body parses and whose client id matches the connection's
binding. Its ServiceTime is already drawn, so a worker never touches a connection's Rng. `deadline_unix_ns` is the
request's RequestDeadline, or `NO_DEADLINE = 2^64 - 1` if the body had none; only the `edf` QueueDiscipline reads
it ([EarliestDeadlineFirst](#earliestdeadlinefirst)). Every PingJob that was
pushed successfully gets exactly one `reply.put`: from the worker that ran it ([WorkerPool](#workerpool)), or from
`queue_close` if it was still queued ([WorkQueue](#workqueue)). A job whose push returned `Closed` gets none, and
its pusher answers the stream itself.

## WorkQueue

An unbounded multi-producer, multi-consumer queue whose pop order is set by its **QueueDiscipline**: `fifo` (push
order) or `edf` ([EarliestDeadlineFirst](#earliestdeadlinefirst)). The reference design is one lock and one
condition variable. An implementation may use any library queue with the same four properties: pop order as the
discipline says, `push` never blocks, `pop` blocks while empty and open, and `close` wakes every blocked `pop`.

```
WorkQueue: { lock, not_empty: condition variable, discipline: fifo | edf,
             items: FIFO of PingJob                                         # fifo
                    or min-priority queue of (deadline_unix_ns, push_seq, PingJob),   # edf, smallest key first
             next_seq: u64 = 0,                                              # edf: push_seq of the next push
             closed: boolean = false,
             stall_ns: u64, open_at_ns: u64 or none = none }      # StallGate, see below

function queue_new(initial_stall_ms, discipline):
    return WorkQueue(discipline = discipline, stall_ns = initial_stall_ms * 1_000_000)

function add_item(q, job):                       # caller holds q.lock
    if q.discipline == fifo: q.items.push_back(job)
    else:
        q.items.insert((job.deadline_unix_ns, q.next_seq, job))
        q.next_seq = q.next_seq + 1              # u64; 2^64 pushes are out of reach

function take_item(q):                           # caller holds q.lock; q.items is not empty
    if q.discipline == fifo: return q.items.pop_front()
    return q.items.pop_min().job                 # smallest (deadline_unix_ns, push_seq)

function queue_push(q, job):
    with q.lock:
        if q.closed: return Closed
        if q.open_at_ns is none:                 # StallGate: the first push arms it
            q.open_at_ns = monotonic_now_ns() + q.stall_ns
            if q.stall_ns > 0: schedule q.not_empty.notify_all() at q.open_at_ns
        add_item(q, job)
        q.not_empty.notify_one()
    return ok

function queue_pop(q):
    with q.lock:
        loop:
            if q.closed: return None             # closed wins over queued items
            if q.items is not empty and monotonic_now_ns() >= q.open_at_ns:
                return take_item(q)
            q.not_empty.wait()                   # woken by a push, the gate opening, or close

procedure queue_close(q):
    with q.lock:
        if q.closed: return
        q.closed = true
        dropped = take_item(q) repeatedly until q.items is empty   # in pop order
        q.not_empty.notify_all()
    for job in dropped: job.reply.put(Aborted)    # outside the lock
```

- **Unbounded.** Nothing limits the queue length. Each connection's HTTP/2 stream limit bounds what one
  connection can have queued, and nothing bounds the total across connections. That is intended: the server
  does no admission control ([../../description.md#non-goals](../../description.md#non-goals)).
- **Closed wins.** After `queue_close`, `queue_pop` returns `None` even if a job was pushed just before the close
  and not yet popped. `queue_close` has already answered that job with `Aborted`.
- **StallGate.** Workers pop nothing until the gate opens ([StallGate](#stallgate)). Pushes, `queue_len` and
  `queue_close` are not affected by it.
- **No reordering after the push.** A job's place is fixed when it is pushed (its key, for `edf`). It is never
  pushed back or skipped. If its stream was reset while it was queued, it is still popped in turn, and the Ping Handler's CancelCheckPoint 1 sees the RST flag
  ([../ping_handler/design.md#cancelcheckpoints](../ping_handler/design.md#cancelcheckpoints)).

## StallGate

The **StallGate** holds every worker back for `initial_stall_ms` after the server admits its **first** `/ping`, so
a backlog builds in the WorkQueue before any request runs. It is part of the WorkQueue (above), not a separate
lock.

- **Armed by the first successful `queue_push`.** That is the first admitted `/ping` of the process, on any
  connection. A `/ping` that gets `400` is never pushed and does not arm it. `/cancel` never touches the queue and
  does not arm it.
- **Opens once**, at `open_at_ns = arm time + initial_stall_ms`, and never closes again. With `initial_stall_ms =
  0`, it is open as soon as it is armed, which is the behavior without a stall.
- **Only popping waits.** During the stall, connections keep admitting and pushing, `/cancel` is answered inline and
  pushes onto CancelInboxes, RST flags are set, and the QueueLengthLog samples the growing queue.
- **Cancels during the stall** are handled when their jobs reach CancelCheckPoint 1 after the gate opens: a reset
  job is skipped with no burn, and a matching `/cancel` gives `503` and `cancelled_before_processing += 1`
  ([../ping_handler/design.md#cancelcheckpoints](../ping_handler/design.md#cancelcheckpoints)).
- **Shutdown during the stall:** `queue_close` wakes every waiting worker. They return `None` at once and the queued
  jobs are answered `Aborted`, as without a stall.
- **Timing:** `schedule ... at` is any timer that wakes the waiting workers no earlier than `open_at_ns`. A worker
  re-checks the time after every wake-up, so an early or spurious wake-up only waits again.

Worked example, `initial_stall_ms = 1000`, times in ms since the server started:

| Event | `open_at` | Effect | Check |
|---|---|---|---|
| `/cancel` at 100 | none | answered `202`, nothing popped | the gate is not armed by `/cancel` |
| first `/ping` pushed at 250 | 1250 | queue 1, no pop | 250 + 1000 |
| 9 more pushed until 1200 | 1250 | queue 10, no pop | the gate is still closed |
| 1250 | 1250 | workers pop in the discipline's order | 1250 >= 1250 |
| `initial_stall_ms = 0`, first push at 250 | 250 | popped at once | 250 + 0 |
| `queue_close` at 600, stall 1000 | 1250 | queued jobs `Aborted`, workers return | close wins |

## WorkerPool

```
function pool_start(n_worker_threads, queue, config, registry, counters, buf):
    workers = []
    for w in 0 .. n_worker_threads - 1:           # n_worker_threads >= 1, checked by config validation
        workers.append(spawn worker_loop(queue, config, registry, counters, buf))
    return WorkerPool(workers)

procedure worker_loop(queue, config, registry, counters, buf):    # buf: MessageBuffer or NONE
    loop:
        job = queue_pop(queue)
        if job is None: return
        result = ping_handler.run_ping(job, config, registry, counters, buf)   # Response or Aborted
        job.reply.put(result)

procedure pool_join(pool):
    for w in pool.workers: wait until w has returned
```

Workers have no identity that matters: any worker may run any client's job, and no state is kept per worker.
All workers share the one MessageBuffer ([../message_buffer/design.md](../message_buffer/design.md#messagebuffer)),
which does its own locking.
The client's cancellation state lives in the CancelRegistry
([../cancel_tracker/design.md#cancelregistry](../cancel_tracker/design.md#cancelregistry)). The response is
written by the stream, not by the worker ([../connection_handler/design.md](../connection_handler/design.md)).

## EarliestDeadlineFirst

With `queue_discipline = edf`, the WorkQueue is a min-priority queue: a worker pops the queued job with the
**earliest RequestDeadline**. This is **EarliestDeadlineFirst** (EDF).

- **Key.** `(deadline_unix_ns, push_seq)`, compared as a pair of unsigned 64-bit integers. `push_seq` counts
  successful pushes from 0, so equal deadlines pop in push order. Retry copies of one request carry the same
  deadline ([../../load_generator/client/design.md#requestdeadline](../../load_generator/client/design.md#requestdeadline))
  and therefore pop in the order they arrived.
- **No deadline.** A job whose body had no valid deadline gets `NO_DEADLINE = 2^64 - 1`. It pops after every job
  with a deadline, and such jobs pop among themselves in push order. With no deadlines at all, `edf` pops exactly
  like `fifo`.
- **Ordering only.** The server never compares a deadline with its own clock: a job whose deadline has passed is
  not dropped or demoted, only ordered. Deadlines are wall-clock values of the client's machine; the server uses
  them only to compare jobs with each other.
- **Starvation is accepted.** A job with a late deadline waits as long as jobs with earlier deadlines keep
  arriving. The client's SLOCancellation and the CancelCheckPoints bound what that costs.
- **Cost.** Push and pop take `O(log n)` for `n` queued jobs, under the same one lock.

Worked example, `edf`, one worker, the StallGate closed while jobs are pushed in the order shown (deadlines in ms
of Unix time, `-` for none):

| Push order (`push_seq`) | Job | Deadline | Key | Pop order |
|---|---|---|---|---|
| 0 | A | 300 | (300, 0) | 4 |
| 1 | B | 100 | (100, 1) | 1 |
| 2 | C | 200 | (200, 2) | 3 |
| 3 | D | - | (2^64 - 1, 3) | 5 |
| 4 | B' (retry copy of B) | 100 | (100, 4) | 2 |

Check: sorting the keys gives (100,1), (100,4), (200,2), (300,0), (2^64-1,3), so the pop order is
**B, B', C, A, D**. B and B' tie on the deadline and `push_seq` puts the original first; D has no deadline and
goes last. With `fifo` the same pushes pop A, B, C, D, B'.

## Ordering

- Jobs **start** in pop order, whatever `n_worker_threads` is. With `fifo` that is push order: the `k`-th job
  popped is the `k`-th job pushed. With `edf` it is the order of `(deadline_unix_ns, push_seq)` among the jobs
  queued at each pop ([EarliestDeadlineFirst](#earliestdeadlinefirst)); a job pushed later with an earlier deadline
  starts before older queued jobs.
- With `n_worker_threads = 1`, jobs also **finish** in pop order, one at a time.
- With `n_worker_threads > 1`, up to `n_worker_threads` jobs run at once, and a job that started later can finish
  earlier.
- One connection pushes its jobs in the order it admits them, which is the order their requests were fully
  received. A client keeps up to `n_waiting_requests` requests outstanding on its connection, and their streams
  may complete in any order, so a client's jobs need not start in `request_id` order, and several of them can be
  queued and running at once. Nothing in the server relies on a per-client order: CancelMatching matches each
  `/cancel` to its request by id
  ([../cancel_tracker/design.md#out-of-order-requests](../cancel_tracker/design.md#out-of-order-requests)).

Worked example (`fifo`): jobs `A`, `B`, `C` pushed at `t = 0` in that order, with `service_us` 300, 100 and 100,
no cancellation:

| `n_worker_threads` | Runs (worker: job [start, end) µs) | Finish order | Check |
|---|---|---|---|
| 1 | w0: A [0, 300), B [300, 400), C [400, 500) | A, B, C | 300 + 100 + 100 = 500 |
| 2 | w0: A [0, 300); w1: B [0, 100), C [100, 200) | B, C, A | w1 is free at 100 and pops C, the next in order |
| 3 | w0: A [0, 300); w1: B [0, 100); w2: C [0, 100) | B and C (tie), then A | all start at 0 |
| 4 | as for 3, plus one worker blocked in `queue_pop` | B and C, then A | more workers than jobs: the extra one waits |

## QueueLengthLog

The **QueueLengthLog** records the WorkQueue length over time. It counts jobs **waiting** in the queue, not jobs a
worker is running, so it measures backlog: `0` means every admitted request is being served or done.

```
function queue_len(q):
    with q.lock:
        return length of q.items                  # 0 after queue_close (items were taken)

procedure queue_sampler(q, log, interval_ms):     # own activity; stop is an atomic boolean plus a wake-up
    start_ns = monotonic_now_ns()
    write(log, "t_ms,queue_len\n")
    k = 0                                          # index of the next deadline
    loop:
        deadline_ms = k * interval_ms              # u64; relative to start
        wait until elapsed_ms() >= deadline_ms or stop is set
        if stop is set: break
        t_ms = elapsed_ms()                        # floor((monotonic_now_ns() - start_ns) / 1_000_000)
        n = queue_len(q)
        if write(log, decimal(t_ms) + "," + decimal(n) + "\n") fails:
            print to stderr "queue log write failed: <reason>"; break          # keep serving, stop sampling
        k = t_ms / interval_ms + 1                 # integer division: the next deadline strictly after t_ms
    flush and close log                            # errors here are reported the same way and ignored

procedure sampler_stop(sampler):
    set stop; wake the sampler; wait until it has returned
```

- **Drift-free.** Deadlines are multiples of `interval_ms` from the start, not "last sample + interval", so small
  delays do not accumulate.
- **No catch-up burst.** If the sampler wakes late past several deadlines, it writes **one** row at the real time,
  and the next deadline is the first multiple of `interval_ms` after it. Rows therefore never share a `t_ms`, and
  `t_ms` strictly increases.
- **No row at stop.** A stop between two deadlines writes nothing more; the last row is the last deadline reached.
- **Cheap.** `queue_len` holds the queue lock only to read a length, like a push or a pop. The sampler never touches
  a job.
- **Buffered.** Rows may be buffered. They are on disk after `sampler_stop` returns, which the Listener waits for
  before printing the CancelCounters line ([../design.md#shutdown](../design.md#shutdown)).

Worked example, `interval_ms = 10`:

| Event (ms since start) | Deadline waited for | Row written | Next `k` | Check |
|---|---|---|---|---|
| start | — | header | 0 | — |
| wake at 0 | 0 | `0,<n>` | 1 | 0 / 10 + 1 = 1 |
| wake at 10 | 10 | `10,<n>` | 2 | 10 / 10 + 1 = 2 |
| wake at 20 | 20 | `20,<n>` | 3 | 20 / 10 + 1 = 3 |
| wake at 47 (late: deadlines 30 and 40 missed) | 30 | `47,<n>` | 5 | 47 / 10 + 1 = 5, next deadline 50 |
| wake at 50 | 50 | `50,<n>` | 6 | 50 / 10 + 1 = 6 |
| stop at 55 | 60 | none | — | stop before the deadline: no extra row |

| Situation | Behavior |
|---|---|
| `queue_log_path` empty | No sampler, no file |
| `queue_log_path` cannot be created | The server exits non-zero before accepting ([../api.md#lifecycle](../api.md#lifecycle)) |
| Write fails during the run | One line on standard error, sampling stops, the file is closed, the server keeps serving |
| Run shorter than `interval_ms` | Header and the `t_ms = 0` row only |
| `interval_ms` very small (e.g. `1`) | Allowed. Rows may be late under load; the late-wake rule keeps `t_ms` increasing |

## Concurrency requirement

Every worker **must be able to burn on its own CPU in parallel** with the other workers, with every FrameReader
and with `/cancel` handling. If workers are coroutines on a shared scheduler, the scheduler must preempt a burning
coroutine or give it its own OS thread; otherwise RST flags and `/cancel` requests would not be seen until a burn
ends. Real parallelism needs at least `n_worker_threads + 1` CPUs or OS threads available to the process; the server
does not check this, and the ProcessPlacement `threads` of the Benchmark Runner is the knob for it
([../../benchmark_runner/config/api.md#processplacement-keys](../../benchmark_runner/config/api.md#processplacement-keys)).

The WorkQueue lock is held only to move one job in or out. A worker never holds it while running a job.

## Failure and edge cases

| Situation | Behavior |
|---|---|
| Push after `queue_close` | `Closed`. The Connection Handler answers the stream with `Aborted` (reset) |
| `queue_close` while workers wait in `queue_pop` | All wake and return `None`; the workers return |
| `queue_close` while a worker burns | The burn runs to its end (or its CancelCheckPoint). The worker fills `reply`, then pops `None` and returns |
| `queue_close` with jobs queued | Each gets `reply.put(Aborted)`, in pop order. They are not counted anywhere |
| Stream reset while its job is queued | The job stays in place. At its turn, `cooperative` mode skips it at CancelCheckPoint 1; `ignore` mode burns it fully |
| Connection closed while its jobs are queued or running | The jobs stay in place. The Connection Handler sets their RST flags when it closes ([../connection_handler/design.md#connection-close](../connection_handler/design.md#connection-close)), so they behave like reset streams; their writes are dropped |
| `queue_close` called twice | The second call does nothing |
| `queue_close` during the stall | As above: queued jobs `Aborted`, workers return, the timer's wake-up later finds the queue closed |
