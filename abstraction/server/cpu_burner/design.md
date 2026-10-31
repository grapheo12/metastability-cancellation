# CPU Burner — Design

## CooperativeCpuBurn

```
SPIN_UNIT_ITERATIONS = 64

function burn(duration_us, cancel_flag, check_interval_us, id_check, check_iter):
    start      = monotonic_now_ns()
    deadline   = start + duration_us * 1000
    next_check = start + check_interval_us * 1000
    units      = 0                                   # spin units since the last id check
    loop:
        t = monotonic_now_ns()
        if t >= deadline:
            return COMPLETED
        if cancel_flag is not NONE and t >= next_check:
            if cancel_flag.load():
                return CANCELLED_RST
            next_check = next_check + check_interval_us * 1000
        if id_check is not NONE and units >= check_iter:
            units = 0
            if id_check():
                return CANCELLED_ID
        spin_unit()
        units = units + 1
```

- **Precedence:** deadline, then RST, then id. A burn whose time has run out reports `COMPLETED`
  even if a cancellation arrived at the last moment. In `cooperative` mode the Connection Handler's
  check before writing still catches a late RST. A late `/cancel` is counted later as
  `completed_before_cancel`.
- **Two cadences:** the RST check is based on time (`check_interval_us`). The `/cancel` check is
  based on iterations (`check_iter = CHECK_ITER` spin units). The first id check happens after
  `check_iter` spin units, never at time 0. CancelCheckPoint 1 has just run at that point
  ([../ping_handler/design.md#cancelcheckpoints](../ping_handler/design.md#cancelcheckpoints)).
- The time spent in `id_check()` counts toward `duration_us` like any other time in the loop.

## Spin unit

A small, fixed amount of arithmetic that the compiler or runtime cannot remove:

```
sink : process-wide u64 (relaxed/volatile store, never read for logic)

procedure spin_unit():
    x = sink
    repeat SPIN_UNIT_ITERATIONS times:
        x = x * 6364136223846793005 + 1442695040888963407     # wrapping u64 (LCG step)
    sink = x                                                   # observable side effect
```

Races on `sink` between connections do no harm, because its value is never used.

## Timing

- The clock is a monotonic, high-resolution clock (nanosecond units, at least microsecond precision).
- No sleep, yield, blocking I/O or allocation happens inside the loop. The one exception is
  `id_check()`, which may free drained inbox nodes and insert into the pending set.
- The time is **wall-clock** time spent spinning, not CPU time accounting. If the OS preempts
  the thread, that time still counts toward `duration_us`.
