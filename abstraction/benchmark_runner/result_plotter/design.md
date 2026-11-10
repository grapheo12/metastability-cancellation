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
        row.cx_offered_fraction = cancel_offered_fraction(params)          # CancelOfferedFraction
        row.lb_imbalance = lb_imbalance(pdir/ingress.summary.json)          # LbImbalance
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
        if figure.x == "time": draw_queue_length(output_dir, done, swept_keys); continue   # QueueLengthPlot
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
| `throughput_latency_normalized` | `lg_goodput / (1 - cx_offered_fraction)` | `e2e_p50_us`, `e2e_p99_us`, `e2e_p999_us` | log |
| `server_counters_normalized` | `x_key` | `srv_cancelled_before_processing / cx_sent`, `srv_cancelled_during_processing / cx_sent`, `srv_completed_before_cancel / cx_sent` | linear, 0 to 1 |
| `queue_length` | `time` (s since server start) | one plot: `queue_len` summed over instances, one line per done point ([QueueLengthPlot](#queuelengthplot)) | linear, from 0 |

A fraction is empty when its denominator (`lg_scheduled` or `cx_sent`) is empty or `0`.

`server_counters_normalized` shows what share of the cancels sent ended each way on the server. The three shares
need not add up to 1: ids still pending or in an inbox at shutdown, and stale ids, are never counted
([../../server/cancel_tracker/design.md#cancelmatching](../../server/cancel_tracker/design.md#cancelmatching)).

| `cx_sent` | Counters (before, during, completed) | Plotted | Check |
|---|---|---|---|
| 1000 | (600, 50, 300) | (0.6, 0.05, 0.3) | 600/1000, 50/1000, 300/1000 |
| 0 or empty (no cancels) | any | (empty, empty, empty) | no denominator |

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

Worked example: `sweep_order = ["load_gen.global_rate", "ingress.cancel_rate_fraction"]` with 3 rates and 2
fractions gives 2 lines, labelled `ingress.cancel_rate_fraction=0.01` and `…=0.5`, of 3 points each:

| Line | Points in order (`global_rate` → (goodput, p99)) |
|---|---|
| `…=0.01` | 10 → (9.9, 1100), 100 → (99.0, 1300), 1000 → (950.0, 4000) |
| `…=0.5` | 10 → (5.0, 1050), 100 → (50.1, 1200), 1000 → (480.0, 2500) |

With no swept keys there is one line, `all`, of one point. A done point with an empty `lg_goodput` is left out of
this figure only.

## CancelOfferedFraction

The **CancelOfferedFraction** of a point is the cancel load it offered, relative to its request load: the
Ingress's `cancel_rate` over the load generator's `global_rate`, both read from the point's `params.json`
argument lists. With a CancelRateFraction sweep it equals the swept fraction up to float rounding
([../config/design.md#cancelratefraction](../config/design.md#cancelratefraction)).

```
function cancel_offered_fraction(params):
    if params has ingress_args:
        c = value of cancel_rate in params.ingress_args, or 0 if absent   # the Ingress default: no cancels
    else:                                                              # a run made before the Ingress
        if params.canceller_args is none: return empty                 # canceller disabled
        c = value of cancel_rate in params.canceller_args
    g = value of global_rate in params.loadgen_args
    if c or g is empty, or g == 0: return empty
    return float(c) / float(g)
```

| `global_rate` | `cancel_rate` | `cx_offered_fraction` |
|---|---|---|
| 1000 | 200.0 | 0.2 |
| 500 | 5.0 | 0.01 |
| 1000 | not set (Ingress) | 0.0 |
| 1000 | (old run, canceller disabled) | empty |

## LbImbalance

**LbImbalance** shows how evenly the Ingress's LoadBalancer spread the point's requests over the backends.

```
function lb_imbalance(summary_path):
    backends = read_json(summary_path).backends, or empty if missing
    if len(backends) < 2: return empty
    f = [b.forwarded for b in backends]
    if min(f) == 0: return empty
    return max(f) / min(f)                                             # float; 1.0 is perfectly even
```

| `forwarded` per backend | `lb_imbalance` |
|---|---|
| (15000, 15000) | 1.0 |
| (15300, 14700) | 1.0408… (15300 / 14700) |
| (30000) (one backend) | empty |
| (30000, 0) | empty |

## ThroughputLatencyNormalizedPlot

The **ThroughputLatencyNormalizedPlot** (`throughput_latency_normalized.png`) is the ThroughputLatencyPlot with
each point's goodput scaled by `1 / (1 - cx_offered_fraction)`. It answers what goodput the same points would
show if the share of load the LoadCanceller aims at had not been removed. Everything else (subplots, series, swept-order
joining, y scale, legend) is as in the ThroughputLatencyPlot.

```
function normalized_goodput(row):
    if row.lg_goodput is empty: return empty
    f = row.cx_offered_fraction, or 0 if empty                         # no cancel load
    if f >= 1: return empty                                            # undefined: point left out
    return row.lg_goodput / (1 - f)                                    # float division
```

x-axis label: `lg_goodput / (1 - cx_offered_fraction) (requests/s)`.

| `lg_goodput` | `cx_offered_fraction` | x | Check |
|---|---|---|---|
| 819.0 | 0.2 | 1023.75 | 819 / 0.8 |
| 491.0 | 0.01 | 495.9596… | 491 / 0.99 |
| 992.0 | empty | 992.0 | f = 0 |
| 50.0 | 1.0 | empty | point left out |

## QueueLengthPlot

The **QueueLengthPlot** (`queue_length.png`) shows each done point's WorkQueue backlog over time, from the servers'
QueueLengthLogs ([../../server/api.md#queuelengthlog-output](../../server/api.md#queuelengthlog-output)). Unlike the
other figures it has time on the x-axis and **one line per point**, not per series.

```
function read_queue_log(path):                     # -> list of (t_ms: u64, queue_len: u64)
    if path does not exist: return none
    rows = []
    for each line after the header "t_ms,queue_len":
        if the line is not two decimal integers separated by ",": stop   # e.g. a truncated last line
        rows.append((t_ms, queue_len))
    return rows

function point_queue_series(pdir, params):         # -> list of (x_s: float, y: u64) | none
    I = params.server_instances, or 1 if absent    # runs made before server instances
    logs = [read_queue_log(pdir/server_<k>.queue.csv) for k in 0 ..= I-1]
    logs = [l for l in logs if l is not none]      # in instance order
    if logs is empty: return none
    m = min(len(l) for l in logs)
    if m == 0: return none                         # some log has a header only
    return [ (logs[0][i].t_ms / 1000, sum(l[i].queue_len for l in logs)) for i in 0 ..= m-1 ]

function draw_queue_length(output_dir, done, swept_keys):
    lines = []
    for row in done, in point-id order:
        s = point_queue_series(output_dir/points/point_<row.point>, params.json of the point)
        if s is none: continue
        label = ", ".join(k + "=" + row[k] for k in swept_keys) or ("point " + row.point)
        lines.append((label, s))
    if lines is empty:
        delete output_dir/plots/queue_length.png if it exists; return     # not an error
    one plot; for each (label, s) in lines: a line without markers through s
    x-axis linear, label "time since server start (s)"
    y-axis linear from 0, label "queue_len (waiting jobs, all instances)"
    legend with the labels when there is more than one line
    save as output_dir/plots/queue_length.png, width 6 in
```

- **Instances are summed by row index.** All instances of a point use the same `queue_sample_interval_ms` and start
  within a few milliseconds of each other, so row `i` of every log is the same moment to within one interval.
  Rows beyond the shortest log are dropped, and the x value comes from instance 0's log (the first present one).
- **Time starts at the server's start**, not the load's. The first part of each line is the idle time before the
  Ingress and the load generator start.
- **Labels name the point**, not a series: the values of every swept key, so each line is one SweepPoint.

Worked example, one point with 2 instances:

| `i` | instance 0 (`t_ms`, `queue_len`) | instance 1 (`t_ms`, `queue_len`) | Plotted (x s, y) | Check |
|---|---|---|---|---|
| 0 | (0, 0) | (0, 1) | (0.0, 1) | 0 + 1 |
| 1 | (10, 2) | (10, 0) | (0.01, 2) | 2 + 0 |
| 2 | (20, 5) | (21, 1) | (0.02, 6) | 5 + 1; x from instance 0 |
| 3 | — (3 rows) | (30, 4) | not plotted | beyond the shortest log (m = 3) |

| Situation | Result |
|---|---|
| No QueueLengthLog files for a point (e.g. a run made before them) | Point left out |
| One instance's file missing, the other present | Plotted from the present instance only |
| A log with a header only | Point left out (`m = 0`) |
| Truncated last line | Rows up to it are used |
| No done point has a log | No `queue_length.png`; any earlier one is deleted |

Non-goal: no PointTable column summarizes the queue length in this version.

## Design specific to Python

Shared tool `implementation/scripts/` (tooling language Python 3.11+; see `AGENTS.md` "Shared tooling"). Identical on
every `impl/<lang>` branch. Plotting needs `matplotlib` (`python3 -m pip install -r implementation/scripts/requirements.txt`).
Tests: `python3 -B -m unittest discover -s implementation/scripts/tests -t implementation/scripts`.

### Python choices

- `all_points.csv` is written with the `csv` module (strings with commas get quoted). Floats are written with `repr`, and `None` becomes an empty field.
- Plots use `matplotlib` with the non-interactive `Agg` backend, imported lazily so the table can be built without it. Each figure is one row of subplots (`figsize` 6 in per subplot), saved at 120 dpi.
- A categorical x-axis is drawn at positions `0..k-1` with tick labels in first-appearance order. A log y scale is applied only when every plotted value is positive.
- Fractions use one helper, `_frac(name, denominator)`, for both `/ lg_scheduled` and `/ cx_sent`.
- `cx_offered_fraction` and `lb_imbalance` are computed by `cancel_offered_fraction(params)` and `lb_imbalance(ingress summary)` while building each row, and placed in the CSV right after `cx_accepted` (`DERIVED_AFTER`).
- A metric column may name several source files in order (`cx_*` read `ingress.summary.json`, then `canceller.summary.json`); the first file present is used, so runs made before the Ingress still plot. The normalized x of the ThroughputLatencyNormalizedPlot is `normalized_goodput(row)`. Both throughput figures share `throughput_latency_line(..., xvalue)`, with the x function looked up in `X_VALUES`.
- `FIGURES` entries carry their x source (`"x_key"` or `"lg_goodput"`). The ThroughputLatencyPlot line is built by the pure function `throughput_latency_line`, so its swept-order rule is unit-tested without `matplotlib`. Its x-axis is always numeric, so categorical tick labels are applied only to `x_key` figures.

### Traceability

Generated from the `# Design:` tags in the code by `implementation/spec_tools/tracegen.py` (see `AGENTS.md`). Each link points at the first code line after the tag.
Regenerate from the repository root with `python3 implementation/spec_tools/tracegen.py --lang Python --impl implementation/scripts --ext .py --comment '#' --write`.

<!-- trace:begin -->
| Design subsection | Implementation (file:line) |
|---|---|
| [PointTable](#pointtable) | [plotter.py:105](../../../implementation/scripts/benchrunner/plotter.py#L105), [test_plotter.py:33](../../../implementation/scripts/tests/test_plotter.py#L33), [test_plotter.py:120](../../../implementation/scripts/tests/test_plotter.py#L120) |
| [SweepPlots](#sweepplots) | [plotter.py:48](../../../implementation/scripts/benchrunner/plotter.py#L48), [plotter.py:63](../../../implementation/scripts/benchrunner/plotter.py#L63), [plotter.py:199](../../../implementation/scripts/benchrunner/plotter.py#L199), [test_plotter.py:52](../../../implementation/scripts/tests/test_plotter.py#L52), [test_plotter.py:79](../../../implementation/scripts/tests/test_plotter.py#L79) |
| [ThroughputLatencyPlot](#throughputlatencyplot) | [plotter.py:59](../../../implementation/scripts/benchrunner/plotter.py#L59), [plotter.py:145](../../../implementation/scripts/benchrunner/plotter.py#L145), [test_plotter.py:69](../../../implementation/scripts/tests/test_plotter.py#L69) |
| [CancelOfferedFraction](#cancelofferedfraction) | [plotter.py:157](../../../implementation/scripts/benchrunner/plotter.py#L157), [test_plotter.py:89](../../../implementation/scripts/tests/test_plotter.py#L89) |
| [LbImbalance](#lbimbalance) | [plotter.py:172](../../../implementation/scripts/benchrunner/plotter.py#L172), [test_plotter.py:111](../../../implementation/scripts/tests/test_plotter.py#L111) |
| [ThroughputLatencyNormalizedPlot](#throughputlatencynormalizedplot) | [plotter.py:61](../../../implementation/scripts/benchrunner/plotter.py#L61), [plotter.py:184](../../../implementation/scripts/benchrunner/plotter.py#L184), [test_plotter.py:102](../../../implementation/scripts/tests/test_plotter.py#L102) |
<!-- trace:end -->
