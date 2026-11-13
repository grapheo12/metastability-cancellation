# Load Generator — Description

Subcomponents: Run Controller, Client, Metrics.

Simulates `N` independent clients against the server, each on its own HTTP/2 connection. Each
client is a **SemiOpenLoopClient**: arrivals follow a Poisson process of rate `global_rate / N`, and a client
keeps up to `n_waiting_requests` requests outstanding, so an arrival that comes due while it is at that cap waits
for a slot (a **GatedArrival**). A request that has not been fully answered `slo_ms` after it was sent is
cancelled with `RST_STREAM` (**SLOCancellation**). Before that, each send of a request (a **RequestCopy**) has a
**RetryTimer** of `retry_timeout_ms`: when it fires, the client sends another copy with the same RequestId, up to
`max_retries` times, and drops the request when the last copy's timer fires. A request first sent in the
**StallWindow** (the first `initial_stall_ms`, while the servers' StallGate holds every request) never retries and
is left out of every metric.

The run has an active phase of `duration_s`, followed by a **DrainPhase** in which in-flight
requests resolve. After that the generator writes one **RequestRecord** per scheduled arrival plus a summary.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
