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
        if figure.x == "x_key": draw as below
        else: draw as in ThroughputLatencyPlot; continue
        one subplot per metric of the figure, sharing the x-axis, title = metric name
        in each subplot, one line with markers per series: points (x, metric) for the series' rows,
            sorted by x, skipping empty metric values
        y-axis log scale if the figure says so
        legend with series labels when there is more than one series
        x-axis label = x_key
        save as output_dir/plots/<figure name>.png (create plots/ first), width 6 in per subplot column
```

| Figure (file name) | x | Subplots (metric) | y scale |
|---|---|---|---|
| `latency_e2e` | `x_key` | `e2e_p50_us`, `e2e_p99_us`, `e2e_p999_us` | log |
| `goodput` | `x_key` | `lg_goodput` | linear |
| `outcomes` | `x_key` | `lg_ok / lg_scheduled`, `lg_rejected / lg_scheduled`, `lg_cancelled / lg_scheduled`, `lg_error / lg_scheduled`, `lg_not_sent / lg_scheduled` | linear, 0 to 1 |
| `server_counters` | `x_key` | `srv_cancelled_before_processing`, `srv_cancelled_during_processing`, `srv_completed_before_cancel` | linear |
| `throughput_latency` | `lg_goodput` | `e2e_p50_us`, `e2e_p99_us`, `e2e_p999_us` | log |
| `server_counters_normalized` | `x_key` | `srv_cancelled_before_processing / cx_sent`, `srv_cancelled_during_processing / cx_sent`, `srv_completed_before_cancel / cx_sent` | linear, 0 to 1 |

A fraction is empty when its denominator (`lg_scheduled` or `cx_sent`) is empty or `0`.

`server_counters_normalized` shows what share of the cancels sent ended each way on the server. The three shares
need not add up to 1: ids still pending or in an inbox at shutdown, and stale ids, are never counted
([../../server/cancel_tracker/design.md#cancelmatching](../../server/cancel_tracker/design.md#cancelmatching)).

| `cx_sent` | Counters (before, during, completed) | Plotted | Check |
|---|---|---|---|
| 1000 | (600, 50, 300) | (0.6, 0.05, 0.3) | 600/1000, 50/1000, 300/1000 |
| 0 or empty (canceller disabled) | any | (empty, empty, empty) | no denominator |

Worked example: with swept keys `[load_gen.global_rate, server.cancel_mode, load_gen.num_clients]`, the x-axis
is `load_gen.global_rate` and the lines are labelled `server.cancel_mode=ignore, load_gen.num_clients=8`,
`server.cancel_mode=ignore, load_gen.num_clients=32`, and so on, one per combination that appears among the
`done` points.

## ThroughputLatencyPlot

The **ThroughputLatencyPlot** (`throughput_latency.png`) puts measured goodput, not a swept key, on the x-axis. The
series are the same as in every other figure, so with `sweep_order = [load_gen.global_rate, X]` there is one line
per value of `X`, and each line has one point per offered rate.

```
function draw_throughput_latency(figure, done, x_key, series, position):
    one subplot per metric, title = metric name
    in each subplot, for each series (same order and labels as SweepPlots):
        members = rows of the series where lg_goodput and the metric are both non-empty
        sort members by position[row[x_key]]          # the swept order, NOT by goodput
        one line with markers through (row.lg_goodput, metric(row)) for row in members
    x-axis linear, label "lg_goodput (requests/s)"
    y-axis log scale (same positivity rule as the other figures)
    legend with series labels when there is more than one series
    save as output_dir/plots/throughput_latency.png, width 6 in per subplot column
```

Points are joined in swept order, so past saturation, where goodput stops growing or falls while offered load
rises, a line bends back on itself instead of being re-sorted into a misleading monotone curve.

Worked example: `sweep_order = ["load_gen.global_rate", "canceller.cancel_rate_fraction"]` with 3 rates and 2
fractions gives 2 lines, labelled `canceller.cancel_rate_fraction=0.01` and `…=0.5`, of 3 points each:

| Line | Points in order (`global_rate` → (goodput, p99)) |
|---|---|
| `…=0.01` | 10 → (9.9, 1100), 100 → (99.0, 1300), 1000 → (950.0, 4000) |
| `…=0.5` | 10 → (5.0, 1050), 100 → (50.1, 1200), 1000 → (480.0, 2500) |

With no swept keys there is one line, `all`, of one point. A done point with an empty `lg_goodput` is left out of
this figure only.
