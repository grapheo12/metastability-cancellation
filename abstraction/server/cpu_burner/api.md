# CPU Burner — API

## `burn(duration_us: integer >= 0, cancel_flag: atomic boolean | NONE, check_interval_us: integer >= 1) -> COMPLETED | CANCELLED`

- With `cancel_flag = NONE`, it always returns `COMPLETED`, after at least `duration_us` µs.
- With a flag, it returns `CANCELLED` within about one `check_interval_us` of the flag being set,
  or `COMPLETED` if the deadline comes first.
- `duration_us = 0` returns `COMPLETED` at once, without reading the flag.

Called by [../ping_handler/design.md#handle_ping](../ping_handler/design.md#handle_ping).
