# Retry Timeout — Description

Leaf component. The **RetryTimeoutCalculator** chooses a `retry_timeout_ms` for an experiment before it runs, from a
queueing model of the servers instead of by hand.

- It models the whole system as one pooled **M/M/k** queue: `k` workers in total (server instances ×
  `n_worker_threads`), exponential service with the given mean, and Poisson arrivals at the offered rate. Its
  utilization is `ρ = rate / (k × service rate per worker)`.
- The **RetryTimeout** at a rate is the time `T` at which the model's sojourn time (queueing wait plus service)
  exceeds `T` with probability `1 − ρ`: `Pr(sojourn > T) = 1 − ρ`. The busier the system, the further into the tail the
  timeout sits, so fewer healthy requests retry.
- It is computed numerically (the tail has no closed-form inverse), for one or more rates, and printed as a table plus
  a line ready to paste into a BenchConfig, for example as the linked values of a LinkedSweep
  ([../config/design.md#linkedsweep](../config/design.md#linkedsweep)).
- It is a calculator only: it needs no config file or machines and runs nothing remotely. The model is an
  approximation; it ignores the Ingress, the network, the per-server split of the load and CPU contention.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
