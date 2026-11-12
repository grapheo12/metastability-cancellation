# Result Plotter — API

## Types

### `PointRow`

One row of the PointTable. The columns, in order, are:

| Column | Source |
|---|---|
| `point` | SweepPoint id |
| `status` | `done`, `failed` or `pending` (neither marker) |
| one column per swept key, named `<section>.<param>`, in sweep order | `params.json` `swept` |
| `lg_scheduled`, `lg_sent`, `lg_ok`, `lg_rejected`, `lg_cancelled`, `lg_error`, `lg_not_sent` | `loadgen.summary.json` `counts` |
| `lg_offered_rate`, `lg_goodput` | `loadgen.summary.json` `rates_per_s.offered`, `.goodput` |
| `e2e_p50_us`, `e2e_p99_us`, `e2e_p999_us` | `loadgen.summary.json` `e2e_latency_us` |
| `service_p50_us`, `service_p99_us` | `loadgen.summary.json` `service_latency_us` |
| `cx_sent`, `cx_accepted` | `ingress.summary.json` `counts`; for runs made before the Ingress, `canceller.summary.json` `counts` (empty if neither exists) |
| `cx_offered_fraction` | `params.json`: the Ingress's `cancel_rate` (`0` if not set) divided by the load generator's `global_rate`. See [design.md#cancelofferedfraction](design.md#cancelofferedfraction) |
| `lb_imbalance` | `ingress.summary.json` `backends`: largest `forwarded` divided by smallest. Empty with fewer than 2 backends, or if a backend forwarded nothing. See [design.md#lbimbalance](design.md#lbimbalance) |
| `srv_cancelled_before_processing`, `srv_cancelled_during_processing`, `srv_completed_before_cancel` | `server_counters.json` |

A value that is missing or `null` is an empty field. See [design.md#pointtable](design.md#pointtable).

## Functions

### `plot(output_dir) -> ok | NothingToPlot`
Writes `all_points.csv` and `plots/*.html` (interactive FigurePages, [design.md#sweepplots](design.md#sweepplots)) ([../api.md#output-directory](../api.md#output-directory)). Returns
`NothingToPlot` (after still writing the table) when no point is `done`. See
[design.md#pointtable](design.md#pointtable), [design.md#sweepplots](design.md#sweepplots) and
[design.md#throughputlatencyplot](design.md#throughputlatencyplot) and
[design.md#throughputlatencynormalizedplot](design.md#throughputlatencynormalizedplot) and
[design.md#queuelengthplot](design.md#queuelengthplot) and [design.md#forwardtimelineplots](design.md#forwardtimelineplots) and [design.md#gcoverlay](design.md#gcoverlay) and [design.md#availabilityplot](design.md#availabilityplot).

## Uses

- [../config/api.md](../config/api.md#sweeppoint): the `params.json` format
- [../../load_generator/metrics/api.md](../../load_generator/metrics/api.md#summary-json): load generator summary fields (and `run_start_unix_ns`)
- [../../load_generator/metrics/api.md](../../load_generator/metrics/api.md#requests-csv): the Requests CSV (`loadgen.requests.csv`)
- [../../ingress/api.md](../../ingress/api.md#output_pathsummaryjson): Ingress summary fields (`counts`, `backends`)
- [../../server/api.md](../../server/api.md#queuelengthlog-output): the QueueLengthLog CSV (`server_<k>.queue.csv`)
- [../../ingress/api.md](../../ingress/api.md#output_pathforwardscsv): the ForwardLog CSV (`ingress.forwards.csv`)
- [../experiment_executor/design.md](../experiment_executor/design.md#gcevents): the GcEvents CSV (`server_<k>.gc.csv`), placed with the Ingress summary's `run_start_unix_ns`
