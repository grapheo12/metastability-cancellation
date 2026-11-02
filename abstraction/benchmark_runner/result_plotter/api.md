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
| `cx_sent`, `cx_accepted` | `canceller.summary.json` `counts` (empty if the canceller is disabled) |
| `cx_offered_fraction` | `params.json`: the canceller's `cancel_rate` divided by the load generator's `global_rate`. Empty if the canceller is disabled or either value is missing, not a number, or `global_rate` is `0`. See [design.md#cancelofferedfraction](design.md#cancelofferedfraction) |
| `srv_cancelled_before_processing`, `srv_cancelled_during_processing`, `srv_completed_before_cancel` | `server_counters.json` |

A value that is missing or `null` is an empty field. See [design.md#pointtable](design.md#pointtable).

## Functions

### `plot(output_dir) -> ok | NothingToPlot`
Writes `all_points.csv` and `plots/*.png` ([../api.md#output-directory](../api.md#output-directory)). Returns
`NothingToPlot` (after still writing the table) when no point is `done`. See
[design.md#pointtable](design.md#pointtable), [design.md#sweepplots](design.md#sweepplots) and
[design.md#throughputlatencyplot](design.md#throughputlatencyplot) and
[design.md#throughputlatencynormalizedplot](design.md#throughputlatencynormalizedplot).

## Uses

- [../config/api.md](../config/api.md#sweeppoint): the `params.json` format
- [../../load_generator/metrics/api.md](../../load_generator/metrics/api.md#summary-json): load generator summary fields
- [../../ingress/load_canceller/api.md](../../ingress/load_canceller/api.md#outputs): canceller summary fields
