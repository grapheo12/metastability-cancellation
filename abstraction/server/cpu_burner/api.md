# CPU Burner — API

## Types

### `BurnOutcome`
One of `COMPLETED`, `CANCELLED_RST` or `CANCELLED_ID`. See
[design.md#cooperativecpuburn](design.md#cooperativecpuburn).

## Functions

### `burn(duration_us: integer >= 0, cancel_flag: atomic boolean | NONE, check_interval_us: integer >= 1, id_check: (() -> boolean) | NONE, check_iter: integer >= 1) -> COMPLETED | CANCELLED_RST | CANCELLED_ID`

- With `cancel_flag = NONE` and `id_check = NONE`, it always returns `COMPLETED`, after at least
  `duration_us` µs.
- `CANCELLED_RST`: the flag was seen set, within about one `check_interval_us` of being set.
- `CANCELLED_ID`: `id_check()` returned `true`. It is called once every `check_iter` spin units.
- `duration_us = 0` returns `COMPLETED` at once, without reading either source.
- When several apply at the same loop iteration, the precedence is deadline, then RST, then id.

Called by [../ping_handler/design.md#handle_ping](../ping_handler/design.md#handle_ping). See
[design.md#cooperativecpuburn](design.md#cooperativecpuburn), [design.md#spin-unit](design.md#spin-unit)
and [design.md#timing](design.md#timing).
