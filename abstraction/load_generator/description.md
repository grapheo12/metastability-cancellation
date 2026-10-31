# Load Generator — Description

Simulates `N` independent clients against the server, each on its own HTTP/2 connection. Each
client is a **ClosedLoopPoissonClient**: arrivals follow a Poisson process of rate
`global_rate / N`, but a client has at most one request outstanding, so an arrival that comes due
early waits for the previous request (a **GatedArrival**). A request that has not been fully answered
`slo_ms` after it was sent is cancelled with `RST_STREAM` (**SLOCancellation**). The client then
moves on.

The run has an active phase of `duration_s`, followed by a **DrainPhase** in which in-flight
requests resolve. After that the generator writes one **RequestRecord** per scheduled arrival plus a summary.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
