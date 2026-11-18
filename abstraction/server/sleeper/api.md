# Sleeper — API

## Constants

| Name | Value | Meaning |
|---|---|---|
| `SLEEP_QUANTUM_US` | `20000` | Longest single sleep, µs. Cancellations are checked only after each one ([design.md#quantumsleep](design.md#quantumsleep)) |

## Types

### `BurnOutcome`

The CPU Burner's type ([../cpu_burner/api.md#burnoutcome](../cpu_burner/api.md#burnoutcome)): one of `COMPLETED`,
`CANCELLED_RST` or `CANCELLED_ID`, with the same meanings.

## Functions

### `sleep(duration_us: integer >= 0, cancel_flag: atomic boolean | NONE, id_check: (() -> boolean) | NONE) -> COMPLETED | CANCELLED_RST | CANCELLED_ID`

- With `cancel_flag = NONE` and `id_check = NONE`, it always returns `COMPLETED`, after at least `duration_us` µs.
- `CANCELLED_RST`: the flag was seen set at a wake-up, at most about one `SLEEP_QUANTUM_US` after it was set.
- `CANCELLED_ID`: `id_check()` returned `true` at a wake-up. It is called once per wake-up that is not the last.
- `duration_us = 0` returns `COMPLETED` at once, without sleeping or reading either source.
- At a wake-up the precedence is deadline, then RST, then id.
- It holds no lock while it sleeps.

Called by [../ping_handler/design.md#run_ping](../ping_handler/design.md#run_ping). See
[design.md#quantumsleep](design.md#quantumsleep) and [design.md#timing](design.md#timing).
