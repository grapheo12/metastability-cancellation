# Tooling language: Python 3.11+ (shared tool implementation/scripts)
# Source: abstraction/benchmark_runner/result_plotter/design.md (result_plotter v0.12.0)
"""PointTable (all_points.csv), SweepPlots, ThroughputLatencyPlot, QueueLengthPlot and ForwardTimelinePlots,
drawn as interactive HTML FigurePages with plotly."""

import csv
import glob
import json
import os

# API: abstraction/benchmark_runner/result_plotter/api.md#pointrow
METRIC_COLUMNS = [
    ("lg_scheduled", "loadgen.summary.json", ["counts", "scheduled"]),
    ("lg_sent", "loadgen.summary.json", ["counts", "sent"]),
    ("lg_ok", "loadgen.summary.json", ["counts", "ok"]),
    ("lg_rejected", "loadgen.summary.json", ["counts", "rejected"]),
    ("lg_cancelled", "loadgen.summary.json", ["counts", "cancelled"]),
    ("lg_dropped", "loadgen.summary.json", ["counts", "dropped"]),
    ("lg_error", "loadgen.summary.json", ["counts", "error"]),
    ("lg_not_sent", "loadgen.summary.json", ["counts", "not_sent"]),
    ("lg_copies_sent", "loadgen.summary.json", ["counts", "copies_sent"]),
    ("lg_offered_rate", "loadgen.summary.json", ["rates_per_s", "offered"]),
    ("lg_goodput", "loadgen.summary.json", ["rates_per_s", "goodput"]),
    ("e2e_p50_us", "loadgen.summary.json", ["e2e_latency_us", "p50"]),
    ("e2e_p99_us", "loadgen.summary.json", ["e2e_latency_us", "p99"]),
    ("e2e_p999_us", "loadgen.summary.json", ["e2e_latency_us", "p999"]),
    ("service_p50_us", "loadgen.summary.json", ["service_latency_us", "p50"]),
    ("service_p99_us", "loadgen.summary.json", ["service_latency_us", "p99"]),
    ("cx_sent", ("ingress.summary.json", "canceller.summary.json"), ["counts", "sent"]),
    ("cx_accepted", ("ingress.summary.json", "canceller.summary.json"), ["counts", "accepted"]),
    ("srv_cancelled_before_processing", "server_counters.json", ["cancelled_before_processing"]),
    ("srv_cancelled_during_processing", "server_counters.json", ["cancelled_during_processing"]),
    ("srv_completed_before_cancel", "server_counters.json", ["completed_before_cancel"]),
]


def _frac(name, den_name="lg_scheduled"):
    """name / den_name; empty when the denominator is empty or 0."""
    def value(row):
        num, den = row.get(name), row.get(den_name)
        return None if num is None or not den else num / den
    return value


def _col(name):
    return lambda row: row.get(name)


# Design: abstraction/benchmark_runner/result_plotter/design.md#sweepplots
# (file name, x, [(subplot title, value of a row)], y scale, y limits); x is "x_key" or a metric column
E2E = [("e2e_p50_us", _col("e2e_p50_us")), ("e2e_p99_us", _col("e2e_p99_us")), ("e2e_p999_us", _col("e2e_p999_us"))]
FIGURES = [
    ("latency_e2e", "x_key", E2E, "log", None),
    ("goodput", "x_key", [("lg_goodput", _col("lg_goodput"))], "linear", None),
    ("outcomes", "x_key", [(f"{m} / lg_scheduled", _frac(m))
                           for m in ("lg_ok", "lg_rejected", "lg_cancelled", "lg_dropped", "lg_error",
                                     "lg_not_sent")],
     "linear", (0, 1)),
    ("server_counters", "x_key", [(m, _col(m)) for m in ("srv_cancelled_before_processing",
                                                         "srv_cancelled_during_processing",
                                                         "srv_completed_before_cancel")], "linear", None),
    # Design: abstraction/benchmark_runner/result_plotter/design.md#throughputlatencyplot
    ("throughput_latency", "lg_goodput", E2E, "log", None),
    # Design: abstraction/benchmark_runner/result_plotter/design.md#throughputlatencynormalizedplot
    ("throughput_latency_normalized", "lg_goodput_normalized", E2E, "log", None),
    # Design: abstraction/benchmark_runner/result_plotter/design.md#sweepplots
    ("server_counters_normalized", "x_key", [(f"{m} / cx_sent", _frac(m, "cx_sent"))
                                             for m in ("srv_cancelled_before_processing",
                                                       "srv_cancelled_during_processing",
                                                       "srv_completed_before_cancel")], "linear", (0, 1)),
    # Design: abstraction/benchmark_runner/result_plotter/design.md#queuelengthplot
    ("queue_length", "time", [], "linear", None),
    # Design: abstraction/benchmark_runner/result_plotter/design.md#forwardtimelineplots
    ("forward_latency", "forward_time", [], "log", None),  # also draws ewma_latency
    # Design: abstraction/benchmark_runner/result_plotter/design.md#availabilityplot
    ("availability", "loadgen_time", [], "linear", (0, 1)),
]
X_LABELS = {"lg_goodput": "lg_goodput (requests/s)",
            "lg_goodput_normalized": "lg_goodput / (1 - cx_offered_fraction) (requests/s)"}
# Derived PointTable columns, placed right after cx_accepted (api.md#pointrow).
DERIVED_AFTER = {"cx_accepted": ["cx_offered_fraction", "lb_imbalance"]}


class NothingToPlot(Exception):
    """No point is done yet."""


def _read_json(path):
    try:
        with open(path) as f:
            return json.load(f)
    except (OSError, json.JSONDecodeError):
        return None


def _dig(doc, keys):
    for k in keys:
        if not isinstance(doc, dict) or k not in doc:
            return None
        doc = doc[k]
    return doc


def _cell(v):
    if v is None:
        return ""
    if isinstance(v, bool):
        return "true" if v else "false"
    if isinstance(v, float):
        return repr(v)
    return str(v)


# Design: abstraction/benchmark_runner/result_plotter/design.md#pointtable
def build_table(output_dir):
    rows, swept_keys = [], None
    for pdir in sorted(glob.glob(os.path.join(output_dir, "points", "point_*"))):
        params = _read_json(os.path.join(pdir, "params.json"))
        if params is None:
            continue
        if swept_keys is None:
            swept_keys = list(params["swept"])
        status = "done" if os.path.exists(os.path.join(pdir, "done")) else \
            "failed" if os.path.exists(os.path.join(pdir, "failed")) else "pending"
        row = {"point": params["id"], "status": status, **params["swept"]}
        docs = {}
        for column, sources, keys in METRIC_COLUMNS:
            for source in (sources if isinstance(sources, tuple) else (sources,)):  # first file present wins
                if source not in docs:
                    docs[source] = _read_json(os.path.join(pdir, source))
                if docs[source] is not None:
                    break
            row[column] = _dig(docs[source], keys)
        row["cx_offered_fraction"] = cancel_offered_fraction(params)
        row["lb_imbalance"] = lb_imbalance(_read_json(os.path.join(pdir, "ingress.summary.json")))
        rows.append(row)
    swept_keys = swept_keys or []
    metric_header = []
    for c, _, _ in METRIC_COLUMNS:
        metric_header += [c] + DERIVED_AFTER.get(c, [])
    header = ["point", "status"] + swept_keys + metric_header
    with open(os.path.join(output_dir, "all_points.csv"), "w", newline="") as f:
        w = csv.writer(f, lineterminator="\n")
        w.writerow(header)
        for row in rows:
            w.writerow([_cell(row.get(c)) for c in header])
    return rows, swept_keys


def _is_number(v):
    return isinstance(v, (int, float)) and not isinstance(v, bool)


# Design: abstraction/benchmark_runner/result_plotter/design.md#throughputlatencyplot
def throughput_latency_line(members, x_key, position, value, xvalue=lambda r: r.get("lg_goodput")):
    """(x, metric) points of one series, joined in swept order, not sorted by x. x is goodput by default."""
    kept = [r for r in members if xvalue(r) is not None and value(r) is not None]
    kept.sort(key=lambda r: position[r[x_key]])
    return [(xvalue(r), value(r)) for r in kept]


def _number(v):
    return v if isinstance(v, (int, float)) and not isinstance(v, bool) else None


# Design: abstraction/benchmark_runner/result_plotter/design.md#cancelofferedfraction
def cancel_offered_fraction(params):
    """cancel_rate / global_rate from params.json; None when undefined."""
    if "ingress_args" in params:
        c = _number(dict(params["ingress_args"]).get("cancel_rate", 0))  # Ingress default: no cancels
    elif params.get("canceller_args") is None:  # a run made before the Ingress, canceller disabled
        return None
    else:
        c = _number(dict(params["canceller_args"]).get("cancel_rate"))
    g = _number(dict(params.get("loadgen_args") or []).get("global_rate"))
    if c is None or g is None or g == 0:
        return None
    return float(c) / float(g)


# Design: abstraction/benchmark_runner/result_plotter/design.md#lbimbalance
def lb_imbalance(summary):
    """max/min of the backends' forwarded counts; None with < 2 backends or a zero count."""
    backends = (summary or {}).get("backends") or []
    if len(backends) < 2:
        return None
    f = [b.get("forwarded") for b in backends]
    if not all(_number(x) is not None for x in f) or min(f) == 0:
        return None
    return max(f) / min(f)


# Design: abstraction/benchmark_runner/result_plotter/design.md#throughputlatencynormalizedplot
def normalized_goodput(row):
    """lg_goodput / (1 - cx_offered_fraction); empty fraction counts as 0, fraction >= 1 is undefined."""
    g = row.get("lg_goodput")
    if g is None:
        return None
    f = row.get("cx_offered_fraction") or 0.0
    if f >= 1:
        return None
    return g / (1 - f)


X_VALUES = {"lg_goodput": lambda r: r.get("lg_goodput"), "lg_goodput_normalized": normalized_goodput}


# Design: abstraction/benchmark_runner/result_plotter/design.md#sweepplots
COLUMN_PX, HEIGHT_PX = 600, 450  # FigurePage: about 600 px per subplot column
HOVER = "%{fullData.name}<br>x=%{x}<br>y=%{y}<extra></extra>"


# Design: abstraction/benchmark_runner/result_plotter/design.md#sweepplots
def new_figure(titles):
    """One row of subplots, titles above them, one shared legend."""
    from plotly.subplots import make_subplots
    fig = make_subplots(rows=1, cols=len(titles), subplot_titles=titles)
    fig.update_layout(width=COLUMN_PX * len(titles), height=HEIGHT_PX, template="plotly_white",
                      legend=dict(font=dict(size=10)))
    return fig


# Design: abstraction/benchmark_runner/result_plotter/design.md#sweepplots
def add_line(fig, col, xs, ys, label, mode, first_col, color=None):
    """One line in subplot col; its legend entry (shown once) toggles it in every subplot."""
    import plotly.graph_objects as go
    fig.add_trace(go.Scatter(x=list(xs), y=list(ys), name=label, mode=mode, legendgroup=label,
                             showlegend=first_col, hovertemplate=HOVER,
                             marker=dict(size=5, color=color), line=dict(color=color)), row=1, col=col)


# Design: abstraction/benchmark_runner/result_plotter/design.md#sweepplots
def set_log_y_if_positive(fig, col, ys):
    """Log y only when the subplot has points and every y is above 0."""
    if ys and all(y > 0 for y in ys):
        fig.update_yaxes(type="log", row=1, col=col)


# Design: abstraction/benchmark_runner/result_plotter/design.md#sweepplots
def save_page(fig, path):
    """Writes the FigurePage; plotly.min.js is written once next to it, so pages open offline."""
    fig.write_html(path, include_plotlyjs="directory", full_html=True)


# Design: abstraction/benchmark_runner/result_plotter/design.md#sweepplots
def plot(output_dir):
    rows, swept_keys = build_table(output_dir)
    done = [r for r in rows if r["status"] == "done"]
    if not done:
        raise NothingToPlot("no point is done")
    x_key, series_keys = (swept_keys[0], swept_keys[1:]) if swept_keys else ("point", [])
    xs = [r[x_key] for r in done]
    numeric = all(_is_number(x) for x in xs)
    if numeric:
        position = {x: x for x in xs}
        ticks = None
    else:
        order = list(dict.fromkeys(_cell(x) for x in xs))
        position = {x: order.index(_cell(x)) for x in xs}
        ticks = order
    series = {}
    for r in done:
        series.setdefault(tuple(r[k] for k in series_keys), []).append(r)

    def label(combo):
        return ", ".join(f"{k}={_cell(v)}" for k, v in zip(series_keys, combo)) or "all"

    os.makedirs(os.path.join(output_dir, "plots"), exist_ok=True)
    for name, x, metrics, yscale, ylim in FIGURES:
        if x == "time":  # QueueLengthPlot
            draw_queue_length(output_dir, done, swept_keys)
            continue
        if x == "loadgen_time":  # AvailabilityPlot
            draw_availability(output_dir, done, swept_keys)
            continue
        if x == "forward_time":  # ForwardTimelinePlots
            draw_forward_timelines(output_dir, done, swept_keys)
            continue
        fig = new_figure([title for title, _ in metrics])
        for col, (title, value) in enumerate(metrics, start=1):
            ys = []
            for combo, members in series.items():
                if x == "x_key":
                    pts = sorted((position[r[x_key]], value(r)) for r in members if value(r) is not None)
                else:  # ThroughputLatencyPlot and ThroughputLatencyNormalizedPlot
                    pts = throughput_latency_line(members, x_key, position, value, X_VALUES[x])
                if pts:
                    add_line(fig, col, (p[0] for p in pts), (p[1] for p in pts), label(combo), "lines+markers",
                             col == 1)
                    ys += [p[1] for p in pts]
            fig.update_xaxes(title_text=x_key if x == "x_key" else X_LABELS[x], row=1, col=col)
            if yscale == "log":
                set_log_y_if_positive(fig, col, ys)
            if ylim:
                fig.update_yaxes(range=list(ylim), row=1, col=col)
            if ticks is not None and x == "x_key":
                fig.update_xaxes(tickvals=list(range(len(ticks))), ticktext=ticks, row=1, col=col)
        fig.update_layout(title_text=name, showlegend=len(series) > 1)
        save_page(fig, os.path.join(output_dir, "plots", f"{name}.html"))


# Design: abstraction/benchmark_runner/result_plotter/design.md#queuelengthplot
def read_queue_log(path):
    """QueueLengthLog rows [(t_ms, queue_len)], or None if the file is missing."""
    if not os.path.exists(path):
        return None
    rows = []
    with open(path, encoding="utf-8", errors="replace") as f:
        lines = f.read().split("\n")
    for line in lines[1:]:  # after the header t_ms,queue_len
        parts = line.split(",")
        if len(parts) != 2 or not (parts[0].isdigit() and parts[1].isdigit()):
            break  # e.g. a truncated last line, or the final empty string
        rows.append((int(parts[0]), int(parts[1])))
    return rows


# Design: abstraction/benchmark_runner/result_plotter/design.md#queuelengthplot
def point_queue_series(pdir, params):
    """[(x_s, summed queue_len)] aligned by row index, or None."""
    instances = params.get("server_instances") or 1  # runs made before server instances
    logs = [read_queue_log(os.path.join(pdir, f"server_{k}.queue.csv")) for k in range(instances)]
    logs = [log for log in logs if log is not None]
    if not logs:
        return None
    m = min(len(log) for log in logs)
    if m == 0:
        return None
    return [(logs[0][i][0] / 1000, sum(log[i][1] for log in logs)) for i in range(m)]


# Design: abstraction/benchmark_runner/result_plotter/design.md#queuelengthplot
def draw_queue_length(output_dir, done, swept_keys):
    path = os.path.join(output_dir, "plots", "queue_length.html")
    lines = []
    for row in sorted(done, key=lambda r: int(r["point"])):
        pdir = os.path.join(output_dir, "points", f"point_{int(row['point']):04d}")
        params = _read_json(os.path.join(pdir, "params.json")) or {}
        series = point_queue_series(pdir, params)
        if series is None:
            continue
        label = ", ".join(f"{k}={_cell(row[k])}" for k in swept_keys) or f"point {row['point']}"
        lines.append((label, series))
    if not lines:
        if os.path.exists(path):
            os.remove(path)
        return
    fig = new_figure(["queue_length"])
    for label, series in lines:
        add_line(fig, 1, (p[0] for p in series), (p[1] for p in series), label, "lines", True)
    fig.update_xaxes(title_text="time since server start (s)")
    fig.update_yaxes(title_text="queue_len (waiting jobs, all instances)", rangemode="tozero")
    fig.update_layout(showlegend=len(lines) > 1)
    save_page(fig, path)


# Design: abstraction/benchmark_runner/result_plotter/design.md#forwardtimelineplots
FORWARD_BIN_MS = 20
FORWARD_HEADER = "t_ns,server_id,outcome,latency_ns,ewma_latency_ns"


# Design: abstraction/benchmark_runner/result_plotter/design.md#forwardtimelineplots
def read_forward_log(path):
    """ForwardLog rows [(t_ns, server_id, outcome, latency_ns, ewma_ns)], or None if the file is missing."""
    if not os.path.exists(path):
        return None
    rows = []
    with open(path, encoding="utf-8", errors="replace") as f:
        lines = f.read().split("\n")
    for line in lines[1:]:  # after the header
        parts = line.split(",")
        if len(parts) != 5 or not (parts[0].isdigit() and parts[1].isdigit() and parts[3].isdigit()) \
                or not parts[2]:
            break  # e.g. a truncated last line, or the final empty string
        try:
            ewma = float(parts[4])
        except ValueError:
            break
        rows.append((int(parts[0]), int(parts[1]), parts[2], int(parts[3]), ewma))
    return rows


# Design: abstraction/benchmark_runner/result_plotter/design.md#forwardtimelineplots
def percentile(values, q):
    """Nearest rank: rank = ceil(q * n / 100), in integers; values not empty."""
    v = sorted(values)
    return v[(q * len(v) + 99) // 100 - 1]


# Design: abstraction/benchmark_runner/result_plotter/design.md#forwardtimelineplots
def bin_series(rows, value, q):
    """[(bin start s, percentile q of value(row) in ms)] for the non-empty bins, in increasing order."""
    bins = {}
    for r in rows:
        bins.setdefault(r[0] // (FORWARD_BIN_MS * 1_000_000), []).append(value(r))
    return [(b * FORWARD_BIN_MS / 1000, percentile(bins[b], q) / 1e6) for b in sorted(bins)]


# (figure name, rows kept, value of a row, y label)
FIGURES_FWD = [
    ("forward_latency", lambda rows: [r for r in rows if r[2] == "ok"], lambda r: r[3], "ok forward latency (ms)"),
    ("ewma_latency", lambda rows: rows, lambda r: r[4], "EwmaLatency (ms)"),
]


# Design: abstraction/benchmark_runner/result_plotter/design.md#forwardtimelineplots
def forward_timelines(output_dir, done, swept_keys):
    """[(file name, y label, [(label, median series, p99 series, GcEvents | None, color index)])] for every
    figure that has a line."""
    logs = []
    for row in sorted(done, key=lambda r: int(r["point"])):
        pdir = os.path.join(output_dir, "points", f"point_{int(row['point']):04d}")
        rows = read_forward_log(os.path.join(pdir, "ingress.forwards.csv"))
        if rows is None:
            continue
        summary = _read_json(os.path.join(pdir, "ingress.summary.json"))
        if summary is not None and summary.get("backends"):
            b = len(summary["backends"])
        else:
            b = 1 + max((r[1] for r in rows), default=0)
        label = ", ".join(f"{k}={_cell(row[k])}" for k in swept_keys) or f"point {row['point']}"
        logs.append((label, b, rows, pdir, len(logs)))  # len(logs): the point's fixed color index
    b_max = max((log[1] for log in logs), default=0)
    figures = []
    for name, select, value, ylabel in FIGURES_FWD:
        targets = [(name, None)] if b_max == 1 else [(f"{name}_server_{k}", k) for k in range(b_max)]
        for file, k in targets:
            lines = []
            for label, _, rows, pdir, color in logs:
                r = select(rows if k is None else [x for x in rows if x[1] == k])
                if r:
                    med, p99 = bin_series(r, value, 50), bin_series(r, value, 99)
                    gc = None
                    if name == "forward_latency":  # GcOverlay
                        t_end_s = max(med[-1][0], p99[-1][0]) + FORWARD_BIN_MS / 1000
                        gc = gc_events(pdir, [0 if k is None else k], origin(pdir, "ingress.summary.json"),
                                       t_end_s)
                    lines.append((label, med, p99, gc, color))
            if lines:
                figures.append((file, ylabel, lines))
    return figures


# Design: abstraction/benchmark_runner/result_plotter/design.md#forwardtimelineplots
def draw_forward_timelines(output_dir, done, swept_keys):
    plots = os.path.join(output_dir, "plots")
    produced = set()
    for file, ylabel, lines in forward_timelines(output_dir, done, swept_keys):
        fig = new_figure([f"median per {FORWARD_BIN_MS} ms", f"p99 per {FORWARD_BIN_MS} ms"])
        ys_by_col = {}
        for col in (1, 2):
            ys = []
            for line in lines:
                s = line[col]
                add_line(fig, col, (p[0] for p in s), (p[1] for p in s), line[0], "lines+markers", col == 1,
                         color=palette_color(line[4]))
                ys += [p[1] for p in s]
            fig.update_xaxes(title_text="time since first /ping at the Ingress (s)", row=1, col=col)
            fig.update_yaxes(title_text=ylabel, row=1, col=col)
            set_log_y_if_positive(fig, col, ys)
            ys_by_col[col] = ys
        fig.update_traces(marker=dict(size=3), line=dict(width=1))
        for col in (1, 2):  # GcOverlay, after the restyle so it keeps its own dotted line
            for line in lines:
                if line[3]:
                    add_gc_overlay(fig, col, line[3], line[0], palette_color(line[4]), ys_by_col[col])
        fig.update_layout(title_text=file, showlegend=len(lines) > 1)
        save_page(fig, os.path.join(plots, f"{file}.html"))
        produced.add(f"{file}.html")
    for path in glob.glob(os.path.join(plots, "forward_latency*.html")) + \
            glob.glob(os.path.join(plots, "ewma_latency*.html")):
        if os.path.basename(path) not in produced:
            os.remove(path)  # stale figure


# Design: abstraction/benchmark_runner/result_plotter/design.md#gcoverlay
GC_HEADER = "t_unix_ns,cycle,stw_start_ms,mark_ms,stw_end_ms"


# Design: abstraction/benchmark_runner/result_plotter/design.md#gcoverlay
def read_gc_log(path):
    """GcEvents rows [(t_unix_ns, cycle, stw_start_ms, mark_ms, stw_end_ms)], or None if the file is missing."""
    if not os.path.exists(path):
        return None
    rows = []
    with open(path, encoding="utf-8", errors="replace") as f:
        lines = f.read().split("\n")
    for line in lines[1:]:  # after the header
        parts = line.split(",")
        if len(parts) != 5 or not (parts[0].isdigit() and parts[1].isdigit()):
            break  # e.g. a truncated last line, or the final empty string
        try:
            rows.append((int(parts[0]), int(parts[1]), float(parts[2]), float(parts[3]), float(parts[4])))
        except ValueError:
            break
    return rows


# Design: abstraction/benchmark_runner/result_plotter/design.md#gcoverlay
def origin(pdir, summary_file):
    """run_start_unix_ns of pdir/<summary_file>, or None if missing or 0."""
    return (_read_json(os.path.join(pdir, summary_file)) or {}).get("run_start_unix_ns") or None


# Design: abstraction/benchmark_runner/result_plotter/design.md#gcoverlay
def gc_events(pdir, instances, start, t_end_s):
    """[(x_s, cycle, stw_start_ms, mark_ms, stw_end_ms)] of the instances within [0, t_end_s] of start, or None."""
    logs = [read_gc_log(os.path.join(pdir, f"server_{k}.gc.csv")) for k in instances]
    logs = [log for log in logs if log is not None]
    if not logs or not start:
        return None
    events = []
    for log in logs:  # instance order, then file order
        for t, c, a, m, b in log:
            x = (t - start) / 1e9  # integer difference first, then one division
            if 0 <= x <= t_end_s:
                events.append((x, c, a, m, b))
    return sorted(events, key=lambda e: e[0])  # stable


# Design: abstraction/benchmark_runner/result_plotter/design.md#gcoverlay
def palette_color(i):
    """The fixed color of the i-th point (point-id order)."""
    from plotly.colors import qualitative
    return qualitative.Plotly[i % len(qualitative.Plotly)]


# Design: abstraction/benchmark_runner/result_plotter/design.md#gcoverlay
def add_gc_overlay(fig, col, events, label, color, ys):
    """One trace: a dotted vertical segment per GcEvent from the subplot's smallest to largest y."""
    import plotly.graph_objects as go
    if not ys:
        return
    lo, hi = min(ys), max(ys)
    xs, yv, text = [], [], []
    for x, c, a, m, b in events:
        hover = f"GC {c} at {x:.3f} s: {a:g} + {m:g} + {b:g} ms"
        xs += [x, x, None]
        yv += [lo, hi, None]
        text += [hover, hover, None]
    fig.add_trace(go.Scatter(x=xs, y=yv, mode="lines", name=f"GC ({label})", legendgroup=label, showlegend=False,
                             line=dict(color=color, width=1, dash="dot"), opacity=0.6, text=text,
                             hovertemplate="%{text}<extra></extra>"), row=1, col=col)


# Design: abstraction/benchmark_runner/result_plotter/design.md#availabilityplot
def read_requests(path):
    """[(end_ns, send_ns, ok)] of the finished rows of a Requests CSV, or None if the file is missing."""
    if not os.path.exists(path):
        return None
    rows = []
    with open(path, encoding="utf-8", errors="replace") as f:
        header = f.readline().rstrip("\n").split(",")
        try:
            i_out, i_send, i_end = header.index("outcome"), header.index("send_ns"), header.index("end_ns")
        except ValueError:
            return []
        for line in f:
            parts = line.rstrip("\n").split(",")
            if len(parts) != len(header):
                break  # e.g. a truncated last line
            end = parts[i_end]
            if not end:
                continue  # not_sent
            try:
                rows.append((int(end), int(parts[i_send]), parts[i_out] == "ok"))
            except ValueError:
                break
    return rows


# Design: abstraction/benchmark_runner/result_plotter/design.md#availabilityplot
AVAILABILITY_WINDOW_MS = 200


# Design: abstraction/benchmark_runner/result_plotter/design.md#availabilityplot
def availability_series(rows, slo_ns):
    """[(window centre s, available / finished)] for the non-empty 200 ms windows of end_ns, in order."""
    w = AVAILABILITY_WINDOW_MS * 1_000_000
    windows = {}
    for end, send, ok in rows:
        n = windows.setdefault(end // w, [0, 0])
        n[1] += 1
        if ok and end - send <= slo_ns:
            n[0] += 1
    return [((b + 0.5) * AVAILABILITY_WINDOW_MS / 1000, windows[b][0] / windows[b][1]) for b in sorted(windows)]


# Design: abstraction/benchmark_runner/result_plotter/design.md#availabilityplot
def availability_lines(output_dir, done, swept_keys):
    """[(label, series, GcEvents | None, color index)] for the done points that have finished requests."""
    lines = []
    for row in sorted(done, key=lambda r: int(r["point"])):
        pdir = os.path.join(output_dir, "points", f"point_{int(row['point']):04d}")
        params = _read_json(os.path.join(pdir, "params.json")) or {}
        slo_ms = dict((k, v) for k, v in params.get("loadgen_args") or []).get("slo_ms")
        rows = read_requests(os.path.join(pdir, "loadgen.requests.csv"))
        if not rows or slo_ms is None:
            continue
        s = availability_series(rows, round(slo_ms * 1_000_000))
        t_end_s = s[-1][0] + AVAILABILITY_WINDOW_MS / 2000
        instances = range(params.get("server_instances") or 1)
        gc = gc_events(pdir, instances, origin(pdir, "loadgen.summary.json"), t_end_s)  # GcOverlay
        label = ", ".join(f"{k}={_cell(row[k])}" for k in swept_keys) or f"point {row['point']}"
        lines.append((label, s, gc, len(lines)))
    return lines


# Design: abstraction/benchmark_runner/result_plotter/design.md#availabilityplot
def draw_availability(output_dir, done, swept_keys):
    path = os.path.join(output_dir, "plots", "availability.html")
    lines = availability_lines(output_dir, done, swept_keys)
    if not lines:
        if os.path.exists(path):
            os.remove(path)
        return
    fig = new_figure([f"availability per {AVAILABILITY_WINDOW_MS} ms (ok within slo_ms of send / finished)"])
    for label, s, _, color in lines:
        add_line(fig, 1, (p[0] for p in s), (p[1] for p in s), label, "lines+markers", True,
                 color=palette_color(color))
    fig.update_traces(marker=dict(size=3), line=dict(width=1))
    for label, _, gc, color in lines:
        if gc:
            add_gc_overlay(fig, 1, gc, label, palette_color(color), [0, 1])
    fig.update_xaxes(title_text="time since the load generator's run start (s)")
    fig.update_yaxes(title_text="availability", range=[0, 1.02])
    fig.update_layout(title_text="availability", showlegend=len(lines) > 1)
    save_page(fig, path)

