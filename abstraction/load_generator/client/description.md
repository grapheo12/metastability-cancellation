# Client — Description

Leaf component: the **ClosedLoopPoissonClient**. One instance simulates one user on its own
HTTP/2 connection. It:

- Generates scheduled arrival times from a Poisson process of rate `global_rate / N`.
- Sends at most one request at a time. An arrival that comes due while a request is outstanding
  waits for it (**GatedArrival**). The schedule itself is never shifted, so waiting builds up a
  backlog that later requests drain.
- Cancels a request with `RST_STREAM(CANCEL)` if it is not fully answered within `slo_ms` of being
  sent (**SLOCancellation**), and then stops waiting on it.
- Stops sending at `duration_s`. Arrivals that were scheduled but not sent are recorded as `not_sent`.
- Records one RequestRecord per scheduled arrival.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
