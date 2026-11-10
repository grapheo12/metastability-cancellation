# Load Generator — Description

Subcomponents: Run Controller, Client, Metrics.

Simulates `N` independent clients against the server, each on its own HTTP/2 connection. Each
client is a **SemiOpenLoopClient**: arrivals follow a Poisson process of rate `global_rate / N`, and a client
keeps up to `n_waiting_requests` requests outstanding, so an arrival that comes due while it is at that cap waits
for a slot (a **GatedArrival**). A request that has not been fully answered `slo_ms` after it was sent is
cancelled with `RST_STREAM` (**SLOCancellation**).

The run has an active phase of `duration_s`, followed by a **DrainPhase** in which in-flight
requests resolve. After that the generator writes one **RequestRecord** per scheduled arrival plus a summary.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
