#!/usr/bin/env python3
"""impl/envoy: plots Envoy's Peak EWMA internals over time from the ingress wrapper's trace outputs
(ingress.peak_ewma_updates.csv, ingress.peak_ewma_picks.csv; wrappers/convert.py merge_peak_ewma) into
<results dir>/plots/peak_ewma.html, one colour per done point, rows = metric, columns = server:

  1  active requests at the LB (rq_active of each candidate at each pick), mean per 500 ms
  2  EWMA RTT (ms) after each update
  3  alpha of each update (1 - e^(-dt/tau); 1 when samples arrive out of order), log scale
  4  cost = EWMA RTT x (active + 1) of each candidate at each pick, mean per 500 ms, log scale
  5  share of picks choosing the server, per 1 s

usage: python3 implementation/envoy/wrappers/plot_peak_ewma.py <run or combined results dir> [...]
Reuses the shared ResultPlotter's helpers (implementation/scripts/benchrunner/plotter.py); needs plotly.
"""
import csv
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, "..", "..", "scripts"))
from benchrunner import plotter  # noqa: E402

ACTIVE_BIN_MS = 500
SHARE_BIN_MS = 1000
ROWS = ["active requests at the LB", "EWMA RTT (ms)", "alpha", "cost = EWMA × (active + 1)", "pick share"]


def read_csv(path):
    if not os.path.exists(path):
        return None
    with open(path, newline="", encoding="utf-8", errors="replace") as f:
        return list(csv.DictReader(f))


def binned_mean(points, bin_ms):
    """[(bin centre s, mean y)] of (t_ns, y) points, bins in order."""
    bins = {}
    w = bin_ms * 1_000_000
    for t, y in points:
        b = bins.setdefault(t // w, [0.0, 0])
        b[0] += y
        b[1] += 1
    return [((k + 0.5) * bin_ms / 1000, s / n) for k, (s, n) in sorted(bins.items())]


def point_series(updates, picks, server):
    """The five (xs, ys) series of one server."""
    s = str(server)
    active, cost, share = [], [], []
    for p in picks:
        t = int(p["t_ns"])
        for side in ("a", "b"):
            if p[f"server_{side}"] == s:
                active.append((t, float(p[f"active_{side}"])))
                cost.append((t, float(p[f"cost_{side}"])))
        if s in (p["server_a"], p["server_b"]):
            share.append((t, 1.0 if p[f"server_{p['chosen']}"] == s else 0.0))
    ups = [u for u in updates if u["server_id"] == s]
    ewma = [(int(u["t_ns"]) / 1e9, float(u["ewma_after_ms"])) for u in ups]
    alpha = [(int(u["t_ns"]) / 1e9, float(u["alpha"])) for u in ups if float(u["alpha"]) > 0]
    return [binned_mean(active, ACTIVE_BIN_MS), ewma, alpha, binned_mean(cost, ACTIVE_BIN_MS),
            binned_mean(share, SHARE_BIN_MS)]


def plot_dir(output_dir):
    import plotly.graph_objects as go
    from plotly.subplots import make_subplots
    rows, swept_keys = plotter.build_table(output_dir)
    done = sorted((r for r in rows if r["status"] == "done"), key=lambda r: int(r["point"]))
    lines, failure_s = [], set()
    for color, row in enumerate(done):
        pdir = os.path.join(output_dir, "points", f"point_{int(row['point']):04d}")
        updates = read_csv(os.path.join(pdir, "ingress.peak_ewma_updates.csv"))
        picks = read_csv(os.path.join(pdir, "ingress.peak_ewma_picks.csv"))
        if updates is None and picks is None:
            print(f"plot_peak_ewma: point {row['point']}: no Peak EWMA trace, skipped")
            continue
        updates, picks = updates or [], picks or []
        servers = sorted({int(u["server_id"]) for u in updates} |
                         {int(p[k]) for p in picks for k in ("server_a", "server_b")})
        label = ", ".join(f"{k}={plotter._cell(row[k])}" for k in swept_keys) or f"point {row['point']}"
        lines.append((label, color, {s: point_series(updates, picks, s) for s in servers}))
        params = plotter._read_json(os.path.join(pdir, "params.json")) or {}
        f = plotter.failure_time_ms(params)
        if f is not None:
            failure_s.add(f / 1000)
    if not lines:
        print(f"plot_peak_ewma: {output_dir}: nothing to plot")
        return None
    servers = sorted({s for _, _, per in lines for s in per})
    fig = make_subplots(rows=len(ROWS), cols=len(servers), shared_xaxes=True, vertical_spacing=0.04,
                        subplot_titles=[f"server {s}: {r}" for r in ROWS for s in servers])
    for label, color, per in lines:
        shown = False
        for col, s in enumerate(servers, start=1):
            if s not in per:
                continue
            for r, series in enumerate(per[s], start=1):
                if not series:
                    continue
                mode = "markers" if r == 3 else ("lines" if r == 2 else "lines+markers")
                fig.add_trace(go.Scatter(x=[p[0] for p in series], y=[p[1] for p in series], name=label,
                                         legendgroup=label, showlegend=not shown, mode=mode,
                                         line=dict(color=plotter.palette_color(color), width=1,
                                                   shape="hv" if r == 2 else "linear"),
                                         marker=dict(size=3, color=plotter.palette_color(color))),
                              row=r, col=col)
                shown = True
    for col in range(1, len(servers) + 1):
        fig.update_yaxes(type="log", row=3, col=col)
        fig.update_yaxes(type="log", row=4, col=col)
        fig.update_yaxes(range=[-0.05, 1.05], row=5, col=col)
        fig.update_xaxes(title_text="time since the Ingress's run start (s)", row=len(ROWS), col=col)
        for x in sorted(failure_s):
            for r in range(1, len(ROWS) + 1):
                fig.add_vline(x=x, line_dash="dash", line_color="gray", row=r, col=col)
    fig.update_layout(title_text="peak_ewma (dashed: server freeze time)", template="plotly_white",
                      height=300 * len(ROWS), autosize=True, showlegend=len(lines) > 1,
                      legend=dict(orientation="h", x=0, y=-0.03, yanchor="top"))
    os.makedirs(os.path.join(output_dir, "plots"), exist_ok=True)
    path = os.path.join(output_dir, "plots", "peak_ewma.html")
    plotter.save_page(fig, path)
    print(f"plot_peak_ewma: wrote {path}")
    return path


if __name__ == "__main__":
    if len(sys.argv) < 2:
        sys.exit(__doc__)
    for d in sys.argv[1:]:
        plot_dir(d)
