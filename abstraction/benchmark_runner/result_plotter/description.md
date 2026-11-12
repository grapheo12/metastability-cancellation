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
  and one pair of figures per backend when there is more than one.

Every figure is an interactive page (**FigurePage**, `plots/<name>.html`) that opens offline.

It needs only the output directory, so it can be re-run at any time, including on a partial run.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
