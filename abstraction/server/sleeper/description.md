# Sleeper — Description

Leaf component. Makes a worker wait out a given number of microseconds of wall-clock time **without using CPU**
(`workload = sleep` or `sleep_constant`). It sleeps the thread in quanta of at most `SLEEP_QUANTUM_US` (20 ms), measured against one
monotonic deadline, so the last quantum may be shorter (**QuantumSleep**). It watches the same two cancellation
sources as the CPU Burner, but only when the thread wakes up between quanta:

- An RST flag.
- An id-check callback (CancelMatching).

It returns early at the first wake-up that sees a cancellation, so a cancellation is noticed at most one quantum
late.

It exists so a server's ServiceTime can be modeled without CPU contention: many sleeping workers share a few cores
without slowing each other down, unlike busy-spinning ones.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
