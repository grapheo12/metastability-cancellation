# Tooling language: Python 3.11+ (shared tool implementation/scripts)
# Source: abstraction/benchmark_runner/result_plotter/design.md (result_plotter v0.6.0)
"""PointTable (all_points.csv), SweepPlots, ThroughputLatencyPlot and QueueLengthPlot (plots/*.png)."""

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
    ("lg_error", "loadgen.summary.json", ["counts", "error"]),
    ("lg_not_sent", "loadgen.summary.json", ["counts", "not_sent"]),
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
                           for m in ("lg_ok", "lg_rejected", "lg_cancelled", "lg_error", "lg_not_sent")],
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
def plot(output_dir):
    import matplotlib
    matplotlib.use("Agg")
    import matplotlib.pyplot as plt

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
            draw_queue_length(plt, output_dir, done, swept_keys)
            continue
        fig, axes = plt.subplots(1, len(metrics), figsize=(6 * len(metrics), 4.5), squeeze=False)
        for ax, (title, value) in zip(axes[0], metrics):
            for combo, members in series.items():
                if x == "x_key":
                    pts = sorted((position[r[x_key]], value(r)) for r in members if value(r) is not None)
                else:  # ThroughputLatencyPlot and ThroughputLatencyNormalizedPlot
                    pts = throughput_latency_line(members, x_key, position, value, X_VALUES[x])
                if pts:
                    ax.plot([p[0] for p in pts], [p[1] for p in pts], marker="o", label=label(combo))
            ax.set_title(title)
            ax.set_xlabel(x_key if x == "x_key" else X_LABELS[x])
            if yscale == "log" and any(line.get_ydata().min() > 0 for line in ax.get_lines()):
                ax.set_yscale("log")
            if ylim:
                ax.set_ylim(*ylim)
            if ticks is not None and x == "x_key":
                ax.set_xticks(range(len(ticks)), ticks)
            if len(series) > 1 and ax is axes[0][0]:
                ax.legend(fontsize="small")
        fig.tight_layout()
        fig.savefig(os.path.join(output_dir, "plots", f"{name}.png"), dpi=120)
        plt.close(fig)


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
def draw_queue_length(plt, output_dir, done, swept_keys):
    path = os.path.join(output_dir, "plots", "queue_length.png")
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
    fig, ax = plt.subplots(figsize=(6, 4.5))
    for label, series in lines:
        ax.plot([p[0] for p in series], [p[1] for p in series], label=label)
    ax.set_xlabel("time since server start (s)")
    ax.set_ylabel("queue_len (waiting jobs, all instances)")
    ax.set_ylim(bottom=0)
    ax.set_title("queue_length")
    if len(lines) > 1:
        ax.legend(fontsize="small")
    fig.tight_layout()
    fig.savefig(path, dpi=120)
    plt.close(fig)
