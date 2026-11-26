#!/usr/bin/env python3
"""impl/envoy: plots Envoy's Peak EWMA internals over time from the ingress wrapper's trace outputs
(ingress.peak_ewma_{updates,picks,evals,queue}.csv; wrappers/convert.py merge_peak_ewma) into
<results dir>/plots/peak_ewma.html, one colour per done point, rows = metric, columns = server:

  1  active requests at the LB (rq_active of each evaluated host at each decision), mean per 500 ms
  2  EWMA RTT (ms) after each update; with peak_ewma_deadline also the median ProvisionalCurrentRTT (dotted) and
     the median time left (dashed) of the deadline decisions per 500 ms: a host is rejected while its provisional
     RTT is above the time left
  3  alpha of each update (1 - e^(-dt/tau); 1 when samples arrive out of order), log scale
  4  cost = EWMA RTT x (active + 1) of each evaluated host at each decision, mean per 500 ms, log scale (the
     choice among kept hosts)
  5  share of decisions choosing the server, per 1 s
  6  peak_ewma_deadline only: FilterWaitQueue length summed over workers (column 1) and requests dropped past
     their deadline per 1 s (column 2)
  7  AnxiousRetry only (ingress.anxious.csv): attempts published as anxious at each evaluation (column 1) and LB
     retries decided per 1 s (column 2)

Decisions are Peak EWMA's picks (two candidates) or, with peak_ewma_deadline, its evals (every host).

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
ROWS = ["active requests at the LB", "EWMA RTT (ms)", "alpha", "cost = EWMA × (active + 1) (ms)", "pick share"]
ANXIOUS_TITLES = ["anxious attempts (published set)", "anxious retries decided per s"]
QUEUE_TITLES = ["wait queue length (all workers; dashed: refused LB retries waiting)", "dropped past deadline per s"]


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


def decisions(picks, evals):
    """[(t_ns, server, active, cost, chosen 0/1, remaining_ms or None, provisional_ms or None)], one per host
    evaluated per decision."""
    out = []
    for p in picks:
        for side in ("a", "b"):
            out.append((int(p["t_ns"]), p[f"server_{side}"], float(p[f"active_{side}"]), float(p[f"cost_{side}"]),
                        1.0 if p["chosen"] == side else 0.0, None, None))
    for e in evals:
        remaining = int(e["remaining_ns"]) / 1e6 if e["remaining_ns"] else None
        provisional = float(e["provisional_ms"]) if e.get("provisional_ms") else None
        out.append((int(e["t_ns"]), e["server_id"], float(e["active"]), float(e["cost"]), float(e["chosen"]),
                    remaining, provisional))
    return out


def binned_median(points, bin_ms):
    """[(bin centre s, median y)] of (t_ns, y) points, bins in order."""
    bins = {}
    for t, y in points:
        bins.setdefault(t // (bin_ms * 1_000_000), []).append(y)
    return [((k + 0.5) * bin_ms / 1000, sorted(v)[len(v) // 2]) for k, v in sorted(bins.items())]


QUEUE_STEP = {"queued": 1, "sent": -1, "dropped": -1, "gone": -1}          # FilterWaitQueue (first attempts)
RETRY_STEP = {"retry_waiting": 1, "retry_sent": -1, "retry_gone": -1}      # refused LB retries waiting


def queue_series(queue):
    """(FIFO length after each event summed over workers, dropped per 1 s, refused LB retries waiting after each
    retry event) from the merged queue events."""
    length, steps, drops, waiting, retry_steps = 0, [], [], 0, []
    for q in queue:
        t = int(q["t_ns"])
        if q["event"] in QUEUE_STEP:
            length += QUEUE_STEP[q["event"]]
            steps.append((t / 1e9, length))
        elif q["event"] in RETRY_STEP:
            waiting += RETRY_STEP[q["event"]]
            retry_steps.append((t / 1e9, waiting))
        if q["event"] == "dropped":
            drops.append((t, 1.0))
    per_s = [(x, y * 1000 / SHARE_BIN_MS) for x, y in binned_sum(drops, SHARE_BIN_MS)]
    return steps, per_s, retry_steps


def anxious_series(rows):
    """(anxious count at each evaluation, retries decided per 1 s) from the merged anxious trace."""
    evals = [(int(r["t_ns"]) / 1e9, int(r["anxious"])) for r in rows if r["event"] == "eval" and r["anxious"]]
    retries = [(int(r["t_ns"]), 1.0) for r in rows if r["event"] == "retry"]
    return evals, [(x, y * 1000 / SHARE_BIN_MS) for x, y in binned_sum(retries, SHARE_BIN_MS)]


def binned_sum(points, bin_ms):
    bins = {}
    for t, y in points:
        k = t // (bin_ms * 1_000_000)
        bins[k] = bins.get(k, 0.0) + y
    return [((k + 0.5) * bin_ms / 1000, v) for k, v in sorted(bins.items())]


def point_series(updates, rows, server):
    """The five (xs, ys) series of one server, plus the median time left and the median ProvisionalCurrentRTT of
    the deadline decisions (ms)."""
    s = str(server)
    active, cost, share, remaining, provisional = [], [], [], [], []
    for t, srv, a, c, chosen, rem, prov in rows:
        if srv != s:
            continue
        active.append((t, a))
        cost.append((t, c))
        share.append((t, chosen))
        if rem is not None:
            remaining.append((t, rem))
            if prov is not None:
                provisional.append((t, prov))
    ups = [u for u in updates if u["server_id"] == s]
    ewma = [(int(u["t_ns"]) / 1e9, float(u["ewma_after_ms"])) for u in ups]
    alpha = [(int(u["t_ns"]) / 1e9, float(u["alpha"])) for u in ups if float(u["alpha"]) > 0]
    return [binned_mean(active, ACTIVE_BIN_MS), ewma, alpha, binned_mean(cost, ACTIVE_BIN_MS),
            binned_mean(share, SHARE_BIN_MS)], binned_median(remaining, ACTIVE_BIN_MS), \
        binned_median(provisional, ACTIVE_BIN_MS)


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
        evals = read_csv(os.path.join(pdir, "ingress.peak_ewma_evals.csv"))
        queue = read_csv(os.path.join(pdir, "ingress.peak_ewma_queue.csv"))
        if updates is None and picks is None and evals is None:
            print(f"plot_peak_ewma: point {row['point']}: no Peak EWMA trace, skipped")
            continue
        updates = updates or []
        rows = decisions(picks or [], evals or [])
        servers = sorted({int(u["server_id"]) for u in updates} | {int(r[1]) for r in rows})
        label = ", ".join(f"{k}={plotter._cell(row[k])}" for k in swept_keys) or f"point {row['point']}"
        anxious = read_csv(os.path.join(pdir, "ingress.anxious.csv"))
        lines.append((label, color, {s: point_series(updates, rows, s) for s in servers},
                      queue_series(queue) if queue is not None else None,
                      anxious_series(anxious) if anxious is not None else None))
        params = plotter._read_json(os.path.join(pdir, "params.json")) or {}
        f = plotter.failure_time_ms(params)
        if f is not None:
            failure_s.add(f / 1000)
    if not lines:
        print(f"plot_peak_ewma: {output_dir}: nothing to plot")
        return None
    servers = sorted({s for _, _, per, _, _ in lines for s in per})
    has_queue = any(line[3] is not None for line in lines)
    has_anxious = any(line[4] is not None for line in lines)
    n_rows = len(ROWS) + (1 if has_queue else 0) + (1 if has_anxious else 0)
    anxious_row = n_rows if has_anxious else None
    titles = [f"server {s}: {r}" for r in ROWS for s in servers]
    if has_queue:
        titles += [QUEUE_TITLES[c] if c < len(QUEUE_TITLES) else "" for c in range(len(servers))]
    if has_anxious:
        titles += [ANXIOUS_TITLES[c] if c < len(ANXIOUS_TITLES) else "" for c in range(len(servers))]
    fig = make_subplots(rows=n_rows, cols=len(servers), shared_xaxes=True, vertical_spacing=0.035,
                        subplot_titles=titles)
    for label, color, per, queue, anxious in lines:
        shown = False
        for col, s in enumerate(servers, start=1):
            if s not in per:
                continue
            series_list, remaining, provisional = per[s]
            if provisional:  # ProvisionalCurrentRTT of the deadline decisions, against the EWMA RTT
                fig.add_trace(go.Scatter(x=[p[0] for p in provisional], y=[p[1] for p in provisional],
                                         name=f"{label}: provisional RTT (ms)", legendgroup=label, showlegend=False,
                                         mode="lines", line=dict(color=plotter.palette_color(color), width=1,
                                                                 dash="dot")), row=2, col=col)
            if remaining:  # time left of the deadline decisions, against the EWMA RTT (the filter's test)
                fig.add_trace(go.Scatter(x=[p[0] for p in remaining], y=[p[1] for p in remaining],
                                         name=f"{label}: time left (ms)", legendgroup=label, showlegend=False,
                                         mode="lines", line=dict(color=plotter.palette_color(color), width=1,
                                                                 dash="dash")), row=2, col=col)
            for r, series in enumerate(series_list, start=1):
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
        if queue is not None:
            if queue[2]:  # refused LB retries waiting, dashed in the queue-length cell
                fig.add_trace(go.Scatter(x=[p[0] for p in queue[2]], y=[p[1] for p in queue[2]],
                                         name=f"{label}: refused retries waiting", legendgroup=label,
                                         showlegend=False, mode="lines",
                                         line=dict(color=plotter.palette_color(color), width=1, dash="dash",
                                                   shape="hv")), row=len(ROWS) + 1, col=1)
            for col, series in enumerate(queue[:min(2, len(servers))], start=1):
                if series:
                    fig.add_trace(go.Scatter(x=[p[0] for p in series], y=[p[1] for p in series], name=label,
                                             legendgroup=label, showlegend=not shown,
                                             mode="lines" if col == 1 else "lines+markers",
                                             line=dict(color=plotter.palette_color(color), width=1,
                                                       shape="hv" if col == 1 else "linear"),
                                             marker=dict(size=3, color=plotter.palette_color(color))),
                                  row=len(ROWS) + 1, col=col)
                    shown = True
        if anxious is not None:
            for col, series in enumerate(anxious[:min(2, len(servers))], start=1):
                if series:
                    fig.add_trace(go.Scatter(x=[p[0] for p in series], y=[p[1] for p in series], name=label,
                                             legendgroup=label, showlegend=not shown,
                                             mode="lines" if col == 1 else "lines+markers",
                                             line=dict(color=plotter.palette_color(color), width=1,
                                                       shape="hv" if col == 1 else "linear"),
                                             marker=dict(size=3, color=plotter.palette_color(color))),
                                  row=anxious_row, col=col)
                    shown = True
    for col in range(1, len(servers) + 1):
        fig.update_yaxes(type="log", row=3, col=col)
        fig.update_yaxes(type="log", row=4, col=col)
        fig.update_yaxes(range=[-0.05, 1.05], row=5, col=col)
        fig.update_xaxes(title_text="time since the Ingress's run start (s)", row=n_rows, col=col)
        for x in sorted(failure_s):
            for r in range(1, n_rows + 1):
                fig.add_vline(x=x, line_dash="dash", line_color="gray", row=r, col=col)
    fig.update_layout(title_text="peak_ewma (dashed: server freeze time)", template="plotly_white",
                      height=300 * n_rows, autosize=True, showlegend=len(lines) > 1,
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
