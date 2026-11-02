# Tooling language: Python 3.11+ (shared tool implementation/scripts)
# Source: abstraction/benchmark_runner/result_plotter/design.md (result_plotter v0.3.0)
"""PointTable (all_points.csv), SweepPlots and ThroughputLatencyPlot (plots/*.png)."""

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
    ("cx_sent", "canceller.summary.json", ["counts", "sent"]),
    ("cx_accepted", "canceller.summary.json", ["counts", "accepted"]),
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
    # Design: abstraction/benchmark_runner/result_plotter/design.md#sweepplots
    ("server_counters_normalized", "x_key", [(f"{m} / cx_sent", _frac(m, "cx_sent"))
                                             for m in ("srv_cancelled_before_processing",
                                                       "srv_cancelled_during_processing",
                                                       "srv_completed_before_cancel")], "linear", (0, 1)),
]
THROUGHPUT_LABEL = "lg_goodput (requests/s)"


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
        for column, source, keys in METRIC_COLUMNS:
            if source not in docs:
                docs[source] = _read_json(os.path.join(pdir, source))
            row[column] = _dig(docs[source], keys)
        rows.append(row)
    swept_keys = swept_keys or []
    header = ["point", "status"] + swept_keys + [c for c, _, _ in METRIC_COLUMNS]
    with open(os.path.join(output_dir, "all_points.csv"), "w", newline="") as f:
        w = csv.writer(f, lineterminator="\n")
        w.writerow(header)
        for row in rows:
            w.writerow([_cell(row.get(c)) for c in header])
    return rows, swept_keys


def _is_number(v):
    return isinstance(v, (int, float)) and not isinstance(v, bool)


# Design: abstraction/benchmark_runner/result_plotter/design.md#throughputlatencyplot
def throughput_latency_line(members, x_key, position, value):
    """(goodput, metric) points of one series, joined in swept order, not sorted by goodput."""
    kept = [r for r in members if r.get("lg_goodput") is not None and value(r) is not None]
    kept.sort(key=lambda r: position[r[x_key]])
    return [(r["lg_goodput"], value(r)) for r in kept]


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
        fig, axes = plt.subplots(1, len(metrics), figsize=(6 * len(metrics), 4.5), squeeze=False)
        for ax, (title, value) in zip(axes[0], metrics):
            for combo, members in series.items():
                if x == "x_key":
                    pts = sorted((position[r[x_key]], value(r)) for r in members if value(r) is not None)
                else:  # ThroughputLatencyPlot
                    pts = throughput_latency_line(members, x_key, position, value)
                if pts:
                    ax.plot([p[0] for p in pts], [p[1] for p in pts], marker="o", label=label(combo))
            ax.set_title(title)
            ax.set_xlabel(x_key if x == "x_key" else THROUGHPUT_LABEL)
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
