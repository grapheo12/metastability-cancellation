# Version — benchmark_runner/result_plotter

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-10-31T00:00:00Z | Initial spec: PointTable columns, SweepPlots figures and layout |
| 0.2.0 | 2026-11-02T00:00:00Z | ThroughputLatencyPlot: new figure `throughput_latency` (x = `lg_goodput`, e2e p50/p99/p999 on log y, points joined in swept order). FIGURES table gains an x column |
| 0.3.0 | 2026-11-02T00:00:00Z | New figure `server_counters_normalized`: each CancelCounter divided by `cx_sent`, linear 0 to 1 |
| 0.4.0 | 2026-11-02T00:00:00Z | PointTable column `cx_offered_fraction` (CancelOfferedFraction). New figure `throughput_latency_normalized`: goodput / (1 - cx_offered_fraction) on x |
| 0.5.0 | 2026-11-02T00:00:00Z | Canceller columns read `ingress.summary.json` (older runs: `canceller.summary.json`); CancelOfferedFraction reads `ingress_args` (cancel_rate 0 if unset). New column `lb_imbalance` (LbImbalance) |
| 0.6.0 | 2026-11-10T00:00:00Z | QueueLengthPlot (`queue_length.png`): per-point WorkQueue length over time, instances summed by row index, one line per done point, worked example and edge cases |
| 0.7.0 | 2026-11-12T00:00:00Z | ForwardTimelinePlots (`forward_latency`, `ewma_latency`, per backend when B > 1): median and p99 per 20 ms bin from `ingress.forwards.csv`, one line per done point; worked example and edge cases |
| 0.8.0 | 2026-11-12T00:00:00Z | Every figure is an interactive FigurePage `plots/<name>.html` (offline, legend toggles, zoom, hover; log y only when all y > 0) instead of a `.png` |
| 0.9.0 | 2026-11-12T00:00:00Z | GcOverlay: `forward_latency` figures mark each GcEvent of their backend (from `server_<k>.gc.csv`, aligned with the Ingress's `run_start_unix_ns`) as a vertical line in the point's color and legend group; worked example |
| 0.10.0 | 2026-11-12T00:00:00Z | AvailabilityPlot (`availability.html`): per 20 ms window of `end_ns`, ok-under-SLO (from send) over finished requests from `loadgen.requests.csv`, with the GcOverlay; GcOverlay generalized to an instance list and a time origin; worked example |
