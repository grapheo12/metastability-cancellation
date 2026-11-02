# Version — benchmark_runner/result_plotter

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-10-31T00:00:00Z | Initial spec: PointTable columns, SweepPlots figures and layout |
| 0.2.0 | 2026-11-02T00:00:00Z | ThroughputLatencyPlot: new figure `throughput_latency` (x = `lg_goodput`, e2e p50/p99/p999 on log y, points joined in swept order). FIGURES table gains an x column |
| 0.3.0 | 2026-11-02T00:00:00Z | New figure `server_counters_normalized`: each CancelCounter divided by `cx_sent`, linear 0 to 1 |
| 0.4.0 | 2026-11-02T00:00:00Z | PointTable column `cx_offered_fraction` (CancelOfferedFraction). New figure `throughput_latency_normalized`: goodput / (1 - cx_offered_fraction) on x |
| 0.5.0 | 2026-11-02T00:00:00Z | Canceller columns read `ingress.summary.json` (older runs: `canceller.summary.json`); CancelOfferedFraction reads `ingress_args` (cancel_rate 0 if unset). New column `lb_imbalance` (LbImbalance) |
