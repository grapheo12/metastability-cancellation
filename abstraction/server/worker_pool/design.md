# Worker Pool — Design

## PingJob

```
PingJob: { task, request_id: u64, client_id: u32, service_us: u64, reply: OneShot }

OneShot: a slot written exactly once and read by one waiter
    put(value)      # first and only write; never blocks
    take() -> value # blocks until put has happened
```

A PingJob is created only for a `POST /ping` whose body parses and whose client id matches the connection's
binding. Its ServiceTime is already drawn, so a worker never touches a connection's Rng. Every PingJob that was
pushed successfully gets exactly one `reply.put`: from the worker that ran it ([WorkerPool](#workerpool)), or from
`queue_close` if it was still queued ([WorkQueue](#workqueue)). A job whose push returned `Closed` gets none, and
its pusher answers the stream itself.

## WorkQueue

An unbounded multi-producer, multi-consumer FIFO. The reference design is one lock and one condition variable.
An implementation may use any library queue with the same four properties: FIFO order, `push` never blocks,
`pop` blocks while empty and open, and `close` wakes every blocked `pop`.

```
WorkQueue: { lock, not_empty: condition variable, items: FIFO of PingJob (unbounded), closed: boolean = false }

function queue_push(q, job):
    with q.lock:
        if q.closed: return Closed
        q.items.push_back(job)
        q.not_empty.notify_one()
    return ok

function queue_pop(q):
    with q.lock:
        while q.items is empty and not q.closed:
            q.not_empty.wait()
        if q.closed: return None                 # closed wins over queued items
        return q.items.pop_front()

procedure queue_close(q):
    with q.lock:
        if q.closed: return
        q.closed = true
        dropped = q.items.take_all()             # in FIFO order
        q.not_empty.notify_all()
    for job in dropped: job.reply.put(Aborted)    # outside the lock
```

- **Unbounded.** Nothing limits the queue length. Each connection's HTTP/2 stream limit bounds what one
  connection can have queued, and nothing bounds the total across connections. That is intended: the server
  does no admission control ([../../description.md#non-goals](../../description.md#non-goals)).
- **Closed wins.** After `queue_close`, `queue_pop` returns `None` even if a job was pushed just before the close
  and not yet popped. `queue_close` has already answered that job with `Aborted`.
- **No reordering.** A job is never pushed back or skipped. If its stream was reset while it was queued, it is
  still popped in turn, and the Ping Handler's CancelCheckPoint 1 sees the RST flag
  ([../ping_handler/design.md#cancelcheckpoints](../ping_handler/design.md#cancelcheckpoints)).

## WorkerPool

```
function pool_start(n_worker_threads, queue, config, registry, counters):
    workers = []
    for w in 0 .. n_worker_threads - 1:           # n_worker_threads >= 1, checked by config validation
        workers.append(spawn worker_loop(queue, config, registry, counters))
    return WorkerPool(workers)

procedure worker_loop(queue, config, registry, counters):
    loop:
        job = queue_pop(queue)
        if job is None: return
        result = ping_handler.run_ping(job, config, registry, counters)   # Response or Aborted
        job.reply.put(result)

procedure pool_join(pool):
    for w in pool.workers: wait until w has returned
```

Workers have no identity that matters: any worker may run any client's job, and no state is kept per worker.
The client's cancellation state lives in the CancelRegistry
([../cancel_tracker/design.md#cancelregistry](../cancel_tracker/design.md#cancelregistry)). The response is
written by the stream, not by the worker ([../connection_handler/design.md](../connection_handler/design.md)).

## Ordering

- Jobs **start** in push order: the `k`-th job popped is the `k`-th job pushed, whatever `n_worker_threads` is.
- With `n_worker_threads = 1`, jobs also **finish** in push order, one at a time.
- With `n_worker_threads > 1`, up to `n_worker_threads` jobs run at once, and a job that started later can finish
  earlier.
- One connection pushes its jobs in the order it admits them, which is the order their requests were fully
  received. A client sends its RequestIds in increasing order, so each client's jobs **start** with increasing
  `request_id`. Two jobs of the same client can only run at the same time after the client gave up on the
  earlier one (RST), because a client keeps at most one request outstanding. CancelMatching relies on this
  ([../cancel_tracker/design.md](../cancel_tracker/design.md)).

Worked example: jobs `A`, `B`, `C` pushed at `t = 0` in that order, with `service_us` 300, 100 and 100, no
cancellation:

| `n_worker_threads` | Runs (worker: job [start, end) µs) | Finish order | Check |
|---|---|---|---|
| 1 | w0: A [0, 300), B [300, 400), C [400, 500) | A, B, C | 300 + 100 + 100 = 500 |
| 2 | w0: A [0, 300); w1: B [0, 100), C [100, 200) | B, C, A | w1 is free at 100 and pops C, the next in order |
| 3 | w0: A [0, 300); w1: B [0, 100); w2: C [0, 100) | B and C (tie), then A | all start at 0 |
| 4 | as for 3, plus one worker blocked in `queue_pop` | B and C, then A | more workers than jobs: the extra one waits |

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
| `queue_close` with jobs queued | Each gets `reply.put(Aborted)`, in FIFO order. They are not counted anywhere |
| Stream reset while its job is queued | The job stays in place. At its turn, `cooperative` mode skips it at CancelCheckPoint 1; `ignore` mode burns it fully |
| Connection closed while its jobs are queued or running | The jobs stay in place. The Connection Handler sets their RST flags when it closes ([../connection_handler/design.md#connection-close](../connection_handler/design.md#connection-close)), so they behave like reset streams; their writes are dropped |
| `queue_close` called twice | The second call does nothing |
