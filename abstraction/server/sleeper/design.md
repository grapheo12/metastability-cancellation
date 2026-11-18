# Sleeper — Design

## QuantumSleep

```
SLEEP_QUANTUM_US = 20000

function sleep(duration_us, cancel_flag, id_check):    # -> COMPLETED | CANCELLED_RST | CANCELLED_ID
    start    = monotonic_now_ns()
    deadline = start + duration_us * 1000              # unsigned 64-bit ns
    loop:
        t = monotonic_now_ns()
        if t >= deadline:
            return COMPLETED
        sleep_thread(min(SLEEP_QUANTUM_US * 1000, deadline - t))       # one quantum, in ns; blocks, no CPU
        if monotonic_now_ns() >= deadline:
            return COMPLETED                           # the last wake-up does not check
        if cancel_flag is not NONE and cancel_flag.load():
            return CANCELLED_RST
        if id_check is not NONE and id_check():
            return CANCELLED_ID
```

- **One deadline.** Each quantum is `min(SLEEP_QUANTUM_US, time left)`. If the OS wakes the thread late, the next
  quantum is shorter, so the total time is the ServiceTime plus at most one late wake-up, never the sum of every
  quantum's lateness.
- **Checks only after a wake-up.** There is no check before the first quantum: CancelCheckPoint 1 has just run
  ([../ping_handler/design.md#cancelcheckpoints](../ping_handler/design.md#cancelcheckpoints)). After the wake-up
  that reaches the deadline there is no check either.
- **Precedence:** deadline, then RST, then id, as in CooperativeCpuBurn
  ([../cpu_burner/design.md#cooperativecpuburn](../cpu_burner/design.md#cooperativecpuburn)). A sleep whose time has
  run out reports `COMPLETED` even if a cancellation arrived during its last quantum. In `cooperative` mode the
  Connection Handler's check before writing still catches a late RST; a late `/cancel` is counted later as
  `completed_before_cancel`.
- The server's `cancel_check_interval_us` and `CHECK_ITER` are not used.

Worked examples, times from the start of the sleep, an on-time OS (each quantum lasts exactly as asked):

| `duration_us` | Events | Quanta (ms) | Checks | Result |
|---|---|---|---|---|
| 50000 | none | 20, 20, 10 | after 20 and 40 ms | `COMPLETED` at 50 ms |
| 20000 | none | 20 | none (the only wake-up reaches the deadline) | `COMPLETED` at 20 ms |
| 0 | none | none | none | `COMPLETED` at once |
| 100000 | RST set at 25 ms | 20, 20 | after 20 ms (clear), after 40 ms (set) | `CANCELLED_RST` at 40 ms |
| 100000 | `/cancel` registered at 5 ms | 20 | after 20 ms: RST clear, `id_check()` true | `CANCELLED_ID` at 20 ms |
| 30000 | RST set at 25 ms | 20, 10 | after 20 ms (clear); the 30 ms wake-up reaches the deadline | `COMPLETED` at 30 ms |
| 50000 | the 20 ms quantum wakes at 23 ms | 20 (ends at 23), 20 (ends at 43), 7 | after 23 and 43 ms | `COMPLETED` at 50 ms |

## Timing

- The clock is the same monotonic, high-resolution clock as the burn
  ([../cpu_burner/design.md#timing](../cpu_burner/design.md#timing)).
- `sleep_thread` blocks only the calling worker. It uses no CPU while blocked, holds no lock, and never delays a
  Connection Handler, another worker or the QueueLengthLog sampler.
- The time is wall-clock time. A thread woken late by the OS still finishes at the first wake-up at or after the
  deadline.
- A worker that is sleeping still occupies its WorkerPool slot: the pool's concurrency is `n_worker_threads`
  whatever the workload.
