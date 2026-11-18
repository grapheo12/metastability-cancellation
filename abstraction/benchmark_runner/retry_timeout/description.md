# Retry Timeout — Description

Leaf component. The **RetryTimeoutCalculator** chooses a `retry_timeout_ms` for an experiment before it runs, from a
queueing model of the servers instead of by hand, and predicts whether retries at that timeout stay stable.

- It models the servers as one **M/M/k** queue: `k` workers, exponential service with the given mean, and Poisson
  arrivals at the given rate. Its utilization is `ρ = rate / (k × service rate per worker)`. The caller picks what
  the queue stands for: the whole system pooled (`k` = server instances × `n_worker_threads`, the global rate), or
  one server instance (`k = n_worker_threads`, its share of the rate).
- The **RetryTimeout** at a rate is the time `T` at which the model's sojourn time (queueing wait plus service)
  exceeds `T` with a target probability. The **EtaSlack** rule sets that target from a target load `η`:
  `Pr(sojourn > T) = max(0, min(1, η − ρ))`. With the default `η = 1` the target is `1 − ρ`: the busier the system,
  the further into the tail the timeout sits, so fewer healthy requests retry. A smaller `η` adds slack, and at
  `ρ >= η` the timeout is infinite (no request retries).
- Retries are extra load. The **RetryFeedback** model applies the fixed timeout `T` at the load that retries
  themselves create, and finds the **StableLoad** where that load settles, and the **TippingLoad** above which it
  grows past `ρ = 1` (collapse). Neither may exist.
- The **MaxStableEta** of a rate is the largest `η` whose RetryFeedback still has a StableLoad, found by binary
  search. It is an upper bound with no margin: at it, the StableLoad and the TippingLoad meet.
- It is computed numerically (the tail has no closed-form inverse), for one or more rates, and printed as a table;
  the RetryTimeouts also as a line ready to paste into a BenchConfig, for example as the linked values of a
  LinkedSweep ([../config/design.md#linkedsweep](../config/design.md#linkedsweep)).
- It is a calculator only: it needs no config file or machines and runs nothing remotely. The model is an
  approximation; it ignores the Ingress, the network, load imbalance and CPU contention, and RetryFeedback assumes
  Poisson retry copies, at most one retry per request (`max_retries = 1`), and that every retry copy costs a full
  service time.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
