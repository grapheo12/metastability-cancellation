# Client — Description

Leaf component: the **SemiOpenLoopClient**. One instance simulates one user on its own HTTP/2 connection. It:

- Generates scheduled arrival times from a Poisson process of rate `global_rate / N`.
- Keeps up to `n_waiting_requests` requests outstanding at once. A **sender** activity sends each arrival when it
  comes due; a **receiver** activity collects responses, timeouts and errors. An arrival that comes due while the
  client is at its cap waits for a slot (**GatedArrival**). The schedule itself is never shifted, so waiting builds
  up a backlog that later requests drain. With `n_waiting_requests = 1` this is a closed loop.
- Starts a **RetryTimer** of `retry_timeout_ms` for each send of a request (a **RequestCopy**). When it fires, the
  client sends another copy with the same RequestId and waits only for that copy, up to `max_retries` times; when
  the last copy's timer fires, the request is `dropped`.
- Never retries a request first sent in the **StallWindow** (the first `initial_stall_ms` of the run, while the
  servers' StallGate holds every request). Such a **StallWindowRequest** is flagged in its record and left out of
  every metric.
- Cancels each request with `RST_STREAM(CANCEL)` on every copy if it is not fully answered within `slo_ms` of its
  first send (**SLOCancellation**), and then stops waiting on it.
- Stops sending at `duration_s`. Arrivals that were scheduled but not sent are recorded as `not_sent`.
- Records one RequestRecord per scheduled arrival.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
