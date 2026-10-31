# CPU Burner — Description

Leaf component. Uses up CPU in a busy loop for a given number of microseconds of wall-clock time,
measured with a monotonic clock. It never sleeps or yields. When given a cancel flag, it checks
the flag every `check_interval_us` and returns early once the flag is set (**CooperativeCpuBurn**).

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
