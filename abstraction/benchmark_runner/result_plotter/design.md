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
        if figure.x == "forward_time": draw_forward_timelines(output_dir, done, swept_keys); continue   # ForwardTimelinePlots
        if figure.x == "loadgen_time": draw_availability(output_dir, done, swept_keys); continue      # AvailabilityPlot
        if figure.x == "x_key": draw as below
        else: draw as in ThroughputLatencyPlot; continue
        one subplot per metric of the figure, sharing the x-axis, title = metric name
        in each subplot, one line with markers per series: points (x, metric) for the series' rows,
            sorted by x, skipping empty metric values
        y-axis log scale if the figure says so
        legend with series labels when there is more than one series
        x-axis label = x_key
        save as output_dir/plots/<figure name>.html (create plots/ first), about 600 px wide per subplot column
```

| Figure (file name) | x | Subplots (metric) | y scale |
|---|---|---|---|
| `latency_e2e` | `x_key` | `e2e_p50_us`, `e2e_p99_us`, `e2e_p999_us` | log |
| `goodput` | `x_key` | `lg_goodput` | linear |
| `outcomes` | `x_key` | `lg_ok / lg_scheduled`, `lg_rejected / lg_scheduled`, `lg_cancelled / lg_scheduled`, `lg_dropped / lg_scheduled`, `lg_error / lg_scheduled`, `lg_not_sent / lg_scheduled` | linear, 0 to 1 |
| `server_counters` | `x_key` | `srv_cancelled_before_processing`, `srv_cancelled_during_processing`, `srv_completed_before_cancel` | linear |
| `throughput_latency` | `lg_goodput` | `e2e_p50_us`, `e2e_p99_us`, `e2e_p999_us` | log |
| `throughput_latency_normalized` | `lg_goodput / (1 - cx_offered_fraction)` | `e2e_p50_us`, `e2e_p99_us`, `e2e_p999_us` | log |
| `server_counters_normalized` | `x_key` | `srv_cancelled_before_processing / cx_sent`, `srv_cancelled_during_processing / cx_sent`, `srv_completed_before_cancel / cx_sent` | linear, 0 to 1 |
| `queue_length` | `time` (s since server start) | one plot: `queue_len` summed over instances, one line per done point ([QueueLengthPlot](#queuelengthplot)) | linear, from 0 |
| `availability` | `loadgen_time` (s since the load generator's `run_start`) | one plot: Availability per `AVAILABILITY_WINDOW_MS` (200 ms) window, one line per done point ([AvailabilityPlot](#availabilityplot)) | linear, 0 to 1 |
| `forward_latency`, `ewma_latency` (one figure each per backend when a point has `B > 1`) | `forward_time` (s since the Ingress's `run_start`) | median and p99 per `FORWARD_BIN_MS` bin, one line per done point ([ForwardTimelinePlots](#forwardtimelineplots)) | log |

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

Every figure is an interactive page (**FigurePage**), `plots/<figure name>.html`:

- **Offline.** A page opens in a browser with no network access. Pages may share one script file that the
  plotter writes into `plots/` next to them; nothing else outside `plots/` is needed.
- **Interactive.** Each line can be hidden or shown from the legend, the axes can be zoomed and panned, and
  hovering over a point shows its x value, its y value and its line's label.
- **Layout.** A figure's subplots sit side by side in one row, each with its title above it, sharing the
  figure's legend. "About 600 px wide per subplot column" is the page's initial size; the page may resize.
- **Log scale.** A "log" y-axis is used only when every plotted y value of that subplot is above 0; otherwise the
  subplot stays linear, so an empty or zero-valued line never breaks the page.

## ThroughputLatencyPlot

The **ThroughputLatencyPlot** (`throughput_latency.html`) puts measured goodput, not a swept key, on the x-axis. The
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
    save as output_dir/plots/throughput_latency.html, about 600 px wide per subplot column
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

The **ThroughputLatencyNormalizedPlot** (`throughput_latency_normalized.html`) is the ThroughputLatencyPlot with
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

The **QueueLengthPlot** (`queue_length.html`) shows each done point's WorkQueue backlog over time, from the servers'
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
        delete output_dir/plots/queue_length.html if it exists; return     # not an error
    one plot; for each (label, s) in lines: a line without markers through s
    x-axis linear, label "time since server start (s)"
    y-axis linear from 0, label "queue_len (waiting jobs, all instances)"
    legend with the labels when there is more than one line
    save as output_dir/plots/queue_length.html, about 600 px wide
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
| No done point has a log | No `queue_length.html`; any earlier one is deleted |

Non-goal: no PointTable column summarizes the queue length in this version.

## ForwardTimelinePlots

The **ForwardTimelinePlots** show what the Ingress's LoadBalancer saw over time, from its ForwardLog
(`ingress.forwards.csv`, [../../ingress/api.md#output_pathforwardscsv](../../ingress/api.md#output_pathforwardscsv)):

- `forward_latency.html`: the latency of `ok` forwards;
- `ewma_latency.html`: the backend's EwmaLatency recorded with every forward.

Each figure has two subplots, the median and the p99 of the values in each time bin of `FORWARD_BIN_MS = 20` ms.
Like the QueueLengthPlot, each figure has **one line per done point**. When some done point has more than one
backend, each backend gets its own pair of figures, `forward_latency_server_<k>.html` and
`ewma_latency_server_<k>.html`, holding only that backend's rows.

```
FORWARD_BIN_MS = 20

function read_forward_log(path):                   # -> list of (t_ns, server_id, outcome, latency_ns, ewma_ns) | none
    if path does not exist: return none
    rows = []
    for each line after the header "t_ns,server_id,outcome,latency_ns,ewma_latency_ns":
        parse t_ns, server_id, latency_ns as decimal integers, outcome as a word, ewma_ns as a float
        if any field fails to parse or the line does not have 5 fields: stop    # e.g. a truncated last line
        rows.append(...)
    return rows

function percentile(values, q):                    # nearest rank, q in {50, 99}; values not empty
    v = sort ascending(values)
    return v[ (q * len(v) + 99) div 100 - 1 ]      # rank = ceil(q * n / 100), exact in integers

function bin_series(rows, value, q):               # -> list of (x_s: float, y_ms: float), bins in increasing order
    bins = group rows by t_ns div (FORWARD_BIN_MS * 1_000_000)
    return [ (b * FORWARD_BIN_MS / 1000, percentile([value(r) for r in bins[b]], q) / 1e6)
             for b in sorted keys of bins ]        # empty bins have no key, so they are skipped

FIGURES_FWD = [ ("forward_latency", rows -> [r for r in rows if r.outcome == ok], r -> r.latency_ns,
                 "ok forward latency (ms)"),
                ("ewma_latency",    rows -> rows,                                  r -> r.ewma_ns,
                 "EwmaLatency (ms)") ]

function draw_forward_timelines(output_dir, done, swept_keys):
    logs = []
    for row in done, in point-id order:
        pdir = output_dir/points/point_<row.point>
        rows = read_forward_log(pdir/ingress.forwards.csv)
        if rows is none: continue
        B = len(backends) of pdir/ingress.summary.json if it exists, else 1 + max server_id in rows, else 1
        label = ", ".join(k + "=" + row[k] for k in swept_keys) or ("point " + row.point)
        logs.append((label, B, rows))
    produced = []
    B_max = max(B over logs), or 0 if logs is empty
    for (name, select, value, ylabel) in FIGURES_FWD:
        targets = [(name, none)] if B_max == 1 else [(name + "_server_" + k, k) for k in 0 ..= B_max-1]
        for (file, k) in targets:
            lines = []
            for (label, B, rows) in logs:
                r = select(rows if k is none else [x for x in rows if x.server_id == k])
                if r is empty: continue
                lines.append((label, bin_series(r, value, 50), bin_series(r, value, 99)))
            if lines is empty: continue
            two subplots side by side, titles "median per 20 ms" and "p99 per 20 ms"
            in each, one line with small markers per (label, series), through its points in x order
            x-axis linear, label "time since first /ping at the Ingress (s)"
            y-axis log, label ylabel
            legend with the labels when there is more than one line
            if name == "forward_latency": overlay gc_events(pdir, [k or 0], origin(pdir, "ingress.summary.json"),
                                                         t_end_s) on both subplots ([GcOverlay](#gcoverlay))
            save as output_dir/plots/<file>.html, about 600 px wide per subplot
            produced.append(file)
    delete every output_dir/plots/forward_latency*.html and ewma_latency*.html not in produced   # stale ones
```

- **Time base.** `t_ns` is measured from the Ingress's `run_start`, its first `/ping`, so every point starts at 0
  with its traffic.
- **Only `ok` latencies.** Rejected, reset and failed forwards end early or late for reasons other than the
  backend's speed, as in the load generator's latency statistics. The EwmaLatency figure uses every row, because
  each one records the value the LoadBalancer held at that moment.
- **Labels name the point**, as in the QueueLengthPlot.
- **Bins are left-closed**: a row at exactly `20 ms` is in the second bin.

Worked example, one point, one backend, `ewma_beta = 0.7`:

| `t_ns` | outcome | `latency_ns` | `ewma_latency_ns` | Bin (`t_ns div 20_000_000`) |
|---|---|---|---|---|
| 1_000_000 | ok | 2_000_000 | 1_300_000 | 0 |
| 5_000_000 | ok | 4_000_000 | 2_110_000 | 0 |
| 19_999_999 | rejected | 1_000_000 | 2_110_000 | 0 |
| 20_000_000 | ok | 3_000_000 | 2_377_000 | 1 |
| 65_000_000 | ok | 10_000_000 | 4_663_900 | 3 |

| Figure | Bin (x s) | Values | median (rank) | p99 (rank) |
|---|---|---|---|---|
| `forward_latency` | 0 (0.0) | ok: 2, 4 ms | 2 ms (`(50×2+99) div 100 = 1`) | 4 ms (`(99×2+99) div 100 = 2`) |
| `forward_latency` | 1 (0.02) | 3 ms | 3 ms | 3 ms |
| `forward_latency` | 3 (0.06) | 10 ms | 10 ms | 10 ms |
| `ewma_latency` | 0 (0.0) | 1.3, 2.11, 2.11 ms | 2.11 ms (`(150+99) div 100 = 2`) | 2.11 ms (`(297+99) div 100 = 3`) |
| `ewma_latency` | 1 (0.02) | 2.377 ms | 2.377 ms | 2.377 ms |
| `ewma_latency` | 3 (0.06) | 4.6639 ms | 4.6639 ms | 4.6639 ms |

Bin 2 has no row, so the lines go straight from x = 0.02 to x = 0.06.

| Situation | Result |
|---|---|
| No `ingress.forwards.csv` for a point (e.g. a run made before the ForwardLog) | Point left out |
| A file with a header only, or no `ok` row for the latency figure | Point left out of that figure |
| Truncated last line | Rows up to it are used |
| Every done point has `B = 1` | `forward_latency.html` and `ewma_latency.html` |
| Some done point has `B = 2` | `forward_latency_server_0.html`, `…_server_1.html`, and the same for `ewma_latency`; a point with `B = 1` appears only in the `_server_0` figures |
| No done point has a ForwardLog | No forward timeline figure; earlier ones are deleted |

## AvailabilityPlot

The **AvailabilityPlot** (`availability.html`) shows, for each done point, the share of the client's requests that
finished in time, over time, from the load generator's Requests CSV
([../../load_generator/metrics/api.md#requests-csv](../../load_generator/metrics/api.md#requests-csv)).

**Availability** at time `t` is the number of requests that finished under the SLO divided by the number of
requests that finished, among the requests whose `end_ns` lies in the `AVAILABILITY_WINDOW_MS = 200` ms window
centred on `t`:

- **Finished:** the row has an `end_ns` (`ok`, `rejected`, `cancelled`, `dropped`, `error`); `not_sent` rows are
  ignored. A row is one logical request however many RequestCopies it sent, so retries are never counted twice.
- **StallWindowRequests are skipped:** a row with `in_stall_window = 1` is neither finished nor under the SLO
  ([../../load_generator/client/design.md#stallwindow](../../load_generator/client/design.md#stallwindow)). A CSV
  without that column (made before the StallWindow existed) has no such rows. Windows that hold only skipped rows
  are empty and are not drawn.
- **Under the SLO:** `outcome == ok` and `end_ns - send_ns <= slo_ms × 1_000_000`, the clock the client's SLO
  cancellation uses (`send_ns` is the first copy's send). A cancelled, dropped, rejected or failed request is
  finished but never under the SLO.

```
function read_requests(path):                      # -> list of (end_ns, send_ns, ok) | none
    if path does not exist: return none
    rows = []
    for each line after the header:
        split into the Requests CSV columns; stop at a line without all of them (e.g. a truncated last line)
        if end_ns is empty: continue                # not_sent
        if the in_stall_window column exists and is "1": continue   # StallWindowRequest
        rows.append((end_ns, send_ns, outcome == "ok"))
    return rows

AVAILABILITY_WINDOW_MS = 200

function availability_series(rows, slo_ns):        # -> list of (x_s, availability), windows in increasing order
    W = AVAILABILITY_WINDOW_MS * 1_000_000          # 200 ms
    windows = group rows by end_ns div W            # window b is [b W, (b+1) W), centred on (b + 1/2) W
    return [ ((b + 0.5) * AVAILABILITY_WINDOW_MS / 1000,
              count of r in windows[b] with r.ok and r.end_ns - r.send_ns <= slo_ns) / len(windows[b]))
             for b in sorted keys of windows ]      # empty windows have no key, so they are skipped

function draw_availability(output_dir, done, swept_keys):
    lines = []
    for row in done, in point-id order:
        pdir = output_dir/points/point_<row.point>
        rows = read_requests(pdir/loadgen.requests.csv)
        slo_ms = value of slo_ms in loadgen_args of pdir/params.json
        if rows is none or rows is empty or slo_ms is none: continue
        s = availability_series(rows, round(slo_ms * 1_000_000))
        t_end_s = s[last].x + AVAILABILITY_WINDOW_MS / 2000
        gc = gc_events(pdir, [0 ..= I-1], origin(pdir, "loadgen.summary.json"), t_end_s)   # GcOverlay
        lines.append((label as in the QueueLengthPlot, s, gc, the point's color index))
    if lines is empty: delete output_dir/plots/availability.html if it exists; return
    one plot; for each line: a line with small markers through s, in the point's color
    overlay each line's gc as in [GcOverlay](#gcoverlay), from y 0 to 1
    x-axis linear, label "time since the load generator's run start (s)"; y-axis linear 0 to 1, label "availability"
    legend with the labels when there is more than one line
    save as output_dir/plots/availability.html, about 600 px wide
```

- **Window and x.** The window centred on `t` is `[t - 100 ms, t + 100 ms)`. Evaluating it at the centres
  `t = 100, 300, 500, … ms` gives disjoint windows. They are 10 times as wide as the 20 ms bins of the
  ForwardTimelinePlots, so each window holds enough requests for a stable ratio (about 800 at 4000 req/s) while
  GC cycles of a second or more still span several windows.
- **Integer test.** `slo_ns = round(slo_ms × 1_000_000)` once per point; the per-row comparison is in integers.
- **Color.** The color index is the point's position among the done points with a Requests CSV, as in the
  ForwardTimelinePlots.

Worked example, `slo_ms = 50` (`slo_ns = 50_000_000`):

| `end_ns` | `send_ns` | outcome | Window (`end_ns div 200_000_000`) | Under SLO | Why |
|---|---|---|---|---|---|
| 10_000_000 | 0 | ok | 0 | yes | 10 ms |
| 80_000_000 | 0 | ok | 0 | no | 80 ms > 50 ms |
| 190_000_000 | 140_000_000 | cancelled | 0 | no | not ok |
| 250_000_000 | 210_000_000 | ok | 1 | yes | 40 ms |
| 300_000_000 | 250_000_000 | ok | 1 | yes | exactly 50 ms: `<=` |
| (empty) | (empty) | not_sent | — | — | not finished |
| 700_000_000 | 660_000_000 | rejected | 3 | no | not ok |

| Window | x (s) | Availability | Check |
|---|---|---|---|
| 0 | 0.1 | 1/3 | one of three |
| 1 | 0.3 | 1 | two of two |
| 2 | — | skipped | no row ends in it |
| 3 | 0.7 | 0 | zero of one |

| Situation | Result |
|---|---|
| No `loadgen.requests.csv`, or no finished row | Point left out |
| No `loadgen.summary.json` `run_start_unix_ns` (a run made before it) | Line drawn, no GC overlay |
| No `server_<k>.gc.csv` for any instance | Line drawn, no GC overlay |
| No done point drawable | No `availability.html`; an earlier one is deleted |

## GcOverlay

When a point has the servers' GcEvents (`server_<k>.gc.csv`, written with `[server] gc_trace`,
[../experiment_executor/design.md#gcevents](../experiment_executor/design.md#gcevents)), the `forward_latency`
figures and the [AvailabilityPlot](#availabilityplot) show each garbage-collection cycle as a vertical line at the
cycle's start, on the figure's own time axis. Each figure gives the instances whose cycles it shows and the
wall-clock origin of its time axis:

| Figure | Instances | Origin (`origin_unix_ns`) |
|---|---|---|
| `forward_latency` (every point has `B = 1`) | 0 | `run_start_unix_ns` of `ingress.summary.json` |
| `forward_latency_server_<k>` | `k` | `run_start_unix_ns` of `ingress.summary.json` |
| `availability` | `0 ..= I-1`, all of the point's instances together | `run_start_unix_ns` of `loadgen.summary.json` |

```
function read_gc_log(path):                        # -> list of (t_unix_ns, cycle, stw_start_ms, mark_ms, stw_end_ms) | none
    if path does not exist: return none
    rows = []
    for each line after the header "t_unix_ns,cycle,stw_start_ms,mark_ms,stw_end_ms":
        if the line does not have 5 fields, the first two decimal integers and the last three decimal numbers: stop
        rows.append(...)
    return rows

function origin(pdir, summary_file):               # -> u64 | none
    return run_start_unix_ns of pdir/<summary_file>, or none if the file or the key is missing, or it is 0

function gc_events(pdir, instances, start, t_end_s):  # -> list of (x_s, cycle, stw_start_ms, mark_ms, stw_end_ms) | none
    logs = [read_gc_log(pdir/server_<k>.gc.csv) for k in instances]
    logs = [l for l in logs if l is not none]
    if logs is empty or start is none: return none
    events = []
    for (t, c, a, m, b) in every row of logs, in instance order then file order:
        x = (t - start) / 1e9                       # integer difference first, then one division
        if 0 <= x <= t_end_s: events.append((x, c, a, m, b))
    return events sorted by x (stable)
```

- **Which instances.** As in the table: one backend per `forward_latency` figure, every instance for
  `availability` (the client's requests may go to any of them). With several instances, the hover text tells their
  cycles apart only by cycle number; the instance is not shown.
- **Range.** `t_end_s` is the end of the point's last window in that figure (its last window start plus
  `FORWARD_BIN_MS / 1000`). Cycles before the figure's origin (the server starts earlier, and fills its
  MessageBuffer) or after its last window are left out.
- **Clock offset.** The Ingress usually runs on the servers' machine, so its origin and the GcEvents share one clock.
  The load generator runs on the client machine; the `availability` overlay then also contains the offset between
  the two machines' clocks, which is accepted (on synchronized cloud machines it is usually well below one
  window).
- **Drawing.** For each point with events, one extra line per subplot: a vertical segment at every event's `x`, from
  the smallest to the largest y plotted in that subplot (`0` to `1` in the AvailabilityPlot), thin and dotted, in the point's color, in the point's legend
  group (hiding the point hides its GC lines too) and without its own legend entry. Hovering over a segment shows
  `GC <cycle> at <x> s: <stw_start_ms> + <mark_ms> + <stw_end_ms> ms`.
- **Colors.** Points get fixed colors in point-id order from one palette, so a point's GC lines match its latency
  line.
- A point without GcEvents, or without `run_start_unix_ns` (a run made before it), is drawn without an overlay.

Worked example: origin `start = 1_700_000_001_000_000_000`, one instance, `t_end_s = 30.02`:

| `t_unix_ns` | `x` (s) | Kept | Check |
|---|---|---|---|
| 1_700_000_000_500_000_000 | -0.5 | no | before the first `/ping` |
| 1_700_000_001_000_000_000 | 0.0 | yes | `0 <= x` |
| 1_700_000_004_507_000_000 | 3.507 | yes | 3_507_000_000 ns / 1e9 |
| 1_700_000_031_100_000_000 | 30.1 | no | after `t_end_s` |

