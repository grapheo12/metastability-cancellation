# Version — benchmark_runner/result_plotter

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-10-31T00:00:00Z | Initial spec: PointTable columns, SweepPlots figures and layout |
| 0.2.0 | 2026-11-02T00:00:00Z | ThroughputLatencyPlot: new figure `throughput_latency` (x = `lg_goodput`, e2e p50/p99/p999 on log y, points joined in swept order). FIGURES table gains an x column |
| 0.3.0 | 2026-11-02T00:00:00Z | New figure `server_counters_normalized`: each CancelCounter divided by `cx_sent`, linear 0 to 1 |
