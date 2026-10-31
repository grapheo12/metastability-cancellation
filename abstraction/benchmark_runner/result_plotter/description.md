# Result Plotter — Description

Leaf component. It reads every point directory of a run and produces:

- the **PointTable** (`all_points.csv`): one row per SweepPoint with its swept values, its status, and the key
  metrics from the load generator summary, the canceller summary and the server's CancelCounters;
- the **SweepPlots**: four standard figures (end-to-end latency percentiles, goodput, outcome mix, server
  counters) drawn over every swept value. The first swept key is the x-axis, and every combination of the other
  swept keys is one line.

It needs only the output directory, so it can be re-run at any time, including on a partial run.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
