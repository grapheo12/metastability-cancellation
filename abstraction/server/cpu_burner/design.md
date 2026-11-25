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

## Design specific to Go

Implementation: `implementation/go` (module `testbed`, Go 1.21, `golang.org/x/net v0.17.0`).
Build and test with `CGO_ENABLED=0 go build ./...` / `CGO_ENABLED=0 go test ./...`. On recent macOS the dynamic
loader rejects cgo-linked Go 1.21 binaries (missing `LC_UUID`), so cgo is disabled.

### Go choices

- File `internal/server/cpuburner.go`. `burn` is `Burn`, returning `BurnCompleted`, `BurnCancelledRST` or `BurnCancelledID`.
- The monotonic clock is `time.Since(clockBase)`. Go's `time.Time` carries a monotonic reading, so wall-clock adjustments have no effect.
- `NONE` is `nil` for both the flag pointer and the `idCheck` function.
- `sink` is an `atomic.Uint64`. The atomic store keeps the work observable and keeps the race detector quiet.

### Traceability

Generated from the `// Design:` tags in the code by `implementation/spec_tools/tracegen.py` (see `AGENTS.md`). Each link points at the first code line after the tag.
Regenerate from the repository root with `python3 implementation/spec_tools/tracegen.py --lang Go --ext .go --comment // --write`.

<!-- trace:begin -->
| Design subsection | Implementation (file:line) |
|---|---|
| [CooperativeCpuBurn](#cooperativecpuburn) | [cpuburner.go:36](../../../implementation/go/internal/server/cpuburner.go#L36), [server_test.go:111](../../../implementation/go/internal/server/server_test.go#L111) |
| [Spin unit](#spin-unit) | [cpuburner.go:19](../../../implementation/go/internal/server/cpuburner.go#L19), [cpuburner.go:66](../../../implementation/go/internal/server/cpuburner.go#L66), [cpuburner.go:69](../../../implementation/go/internal/server/cpuburner.go#L69) |
| [Timing](#timing) | [cpuburner.go:25](../../../implementation/go/internal/server/cpuburner.go#L25), [cpuburner.go:28](../../../implementation/go/internal/server/cpuburner.go#L28) |
<!-- trace:end -->
