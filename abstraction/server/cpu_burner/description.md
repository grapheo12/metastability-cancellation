# CPU Burner — Description

Leaf component. Uses up CPU in a busy loop for a given number of microseconds of wall-clock time,
measured with a monotonic clock. It never sleeps or yields. It can watch two cancellation sources
(**CooperativeCpuBurn**):

- An RST flag, checked every `check_interval_us` of time.
- An id-check callback (CancelMatching), called every `check_iter` spin units.

It returns early as soon as either one reports a cancellation.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
