# CPU Burner — Design

## CooperativeCpuBurn

```
SPIN_UNIT_ITERATIONS = 64

function burn(duration_us, cancel_flag, check_interval_us):
    start      = monotonic_now_ns()
    deadline   = start + duration_us * 1000
    next_check = start + check_interval_us * 1000
    loop:
        t = monotonic_now_ns()
        if t >= deadline:
            return COMPLETED
        if cancel_flag is not NONE and t >= next_check:
            if cancel_flag.load():
                return CANCELLED
            next_check = next_check + check_interval_us * 1000
        spin_unit()
```

The deadline check comes before the flag check, so a burn whose time has run out reports
`COMPLETED` even if the flag was set at the last moment. The Connection Handler's check before
writing catches that case in `cooperative` mode.

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
- No sleep, yield, blocking I/O or allocation happens inside the loop.
- The time is **wall-clock** time spent spinning, not CPU time accounting. If the OS preempts
  the thread, that time still counts toward `duration_us`.
