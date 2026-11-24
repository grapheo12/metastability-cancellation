# Result Plotter — Description

Leaf component. It reads every point directory of a run and produces:

- the **PointTable** (`all_points.csv`): one row per SweepPoint with its swept values, its status, and the key
  metrics from the load generator summary, the Ingress summary (LoadCanceller counts, per-backend counts) and the
  servers' summed CancelCounters;
- the **SweepPlots**: seven standard figures over the swept values (end-to-end latency percentiles, goodput, outcome mix, server
  counters, server counters per cancel sent, the throughput–latency curve, and the same curve with goodput scaled
  by `1 / (1 - CancelOfferedFraction)`, the **ThroughputLatencyNormalizedPlot**). The first swept key is the x-axis, except in the
  **ThroughputLatencyPlot**, where measured goodput is the x-axis and the first swept key orders the points.
  Every combination of the other swept keys is one line.
- the **QueueLengthPlot**: the eighth figure, the servers' WorkQueue length over time (from their QueueLengthLogs),
  one line per done SweepPoint.
- the **ForwardTimelinePlots**: the latency of `ok` forwards and the EwmaLatency seen by the Ingress's
  LoadBalancer over time (from its ForwardLog), as the median and p99 of 20 ms bins, one line per done SweepPoint,
  and one pair of figures per backend when there is more than one. With GcEvents, the latency figures also mark
  every garbage-collection cycle of the server (the **GcOverlay**).
- the **AvailabilityPlot**: per 1 s window of send times, the share of the client's finished requests that finished under the
  SLO (the **Availability**), one line per done SweepPoint, with the GcOverlay.
- the **EstimateErrorHistogram**: per done SweepPoint, the distribution of the EstimateError of the LoadBalancer's
  EstimatedFinish (`(latency − estimate) / latency` of each ok forward), split before and after the failure.
- the **StartSlackHistogram**: per done SweepPoint, the distribution of the requests' StartSlack (time left before
  the RequestDeadline when a worker picked the request up), as fractions per 100 ms bin; for a run with a
  WorkerFreeze also split into requests sent before and after the failure (the **FailureTime**).
- the **ExpectedWaitPlot**: per backend, one subplot each on one page, `(outstanding + 1) × EwmaExecTime` as the
  LoadBalancer saw it at every pick (from its PickLog), averaged per 200 ms, one line per done SweepPoint.

Every figure is an interactive page (**FigurePage**, `plots/<name>.html`) that opens offline.

It needs only the output directory, so it can be re-run at any time, including on a partial run.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
