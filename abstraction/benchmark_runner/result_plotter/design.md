# Result Plotter — Design

## PointTable

```
function build_table(output_dir):
    rows = []
    for pdir in sorted(output_dir/points/point_*):             # id order
        params = read_json(pdir/params.json)                    # skip pdir if missing
        status = "done" if exists(pdir/done) else "failed" if exists(pdir/failed) else "pending"
        row = {point: params.id, status: status} + params.swept
        for each metric column (api.md#pointrow): read it from its source file if present, else empty
        rows.append(row)
    swept_keys = keys of params.swept of the first row (they are identical for every point of a run)
    write output_dir/all_points.csv: header = columns in api order, one line per row,
        numbers in decimal (floats as shortest round-trip form), strings as is
    return rows, swept_keys
```

The table includes failed and pending points so the operator sees the whole sweep. Plots use only `done` rows.

## SweepPlots

```
function plot(output_dir):
    rows, swept_keys = build_table(output_dir)
    done = [r for r in rows if r.status == "done"]
    if done is empty: return NothingToPlot
    if swept_keys is empty: x_key = "point"; series_keys = []
    else: x_key = swept_keys[0]; series_keys = swept_keys[1:]
    x_values = distinct values of x_key over done rows:
        sorted ascending if all numeric, else in first-appearance order (categorical axis, evenly spaced)
    series = group done rows by their tuple of series_keys values, in first-appearance order
    label(series) = ", ".join(k + "=" + v for k, v in series) or "all"
    for figure in FIGURES:
        one subplot per metric of the figure, sharing the x-axis, title = metric name
        in each subplot, one line with markers per series: points (x, metric) for the series' rows,
            sorted by x, skipping empty metric values
        y-axis log scale if the figure says so
        legend with series labels when there is more than one series
        x-axis label = x_key
        save as output_dir/plots/<figure name>.png (create plots/ first), width 6 in per subplot column
```

| Figure (file name) | Subplots (metric) | y scale |
|---|---|---|
| `latency_e2e` | `e2e_p50_us`, `e2e_p99_us`, `e2e_p999_us` | log |
| `goodput` | `lg_goodput` | linear |
| `outcomes` | `lg_ok / lg_scheduled`, `lg_rejected / lg_scheduled`, `lg_cancelled / lg_scheduled`, `lg_error / lg_scheduled`, `lg_not_sent / lg_scheduled` | linear, 0 to 1 |
| `server_counters` | `srv_cancelled_before_processing`, `srv_cancelled_during_processing`, `srv_completed_before_cancel` | linear |

A fraction is empty when `lg_scheduled` is empty or `0`.

Worked example: with swept keys `[load_gen.global_rate, server.cancel_mode, load_gen.num_clients]`, the x-axis
is `load_gen.global_rate` and the lines are labelled `server.cancel_mode=ignore, load_gen.num_clients=8`,
`server.cancel_mode=ignore, load_gen.num_clients=32`, and so on, one per combination that appears among the
`done` points.
