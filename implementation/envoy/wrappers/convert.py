# Source: abstraction/load_generator/design.md (load_generator v0.9.2)
# Source: abstraction/load_generator/client/design.md (client v0.10.0)
# Source: abstraction/load_generator/metrics/design.md (metrics v0.6.1)
# Source: abstraction/ingress/design.md (ingress v0.20.1)
# Source: abstraction/ingress/load_balancer/design.md (load_balancer v0.26.0)
# Source: abstraction/ingress/frontend/design.md (frontend v0.11.0)
# Source: abstraction/protocol/design.md (protocol v0.8.0)
"""impl/envoy wrappers: pure conversions from Envoy's access log and Nighthawk's per-request logs to the testbed's
output files (no I/O beyond reading and writing the given paths, so they are unit-tested without Envoy or Nighthawk).

Formats produced:
  ingress  <out>.forwards.csv, <out>.summary.json   abstraction/ingress/api.md#outputs
           <out>.peak_ewma_{updates,picks,evals,queue,tokens,bbr}.csv   impl/envoy only: Envoy's Peak EWMA trace
  loadgen  <out>.requests.csv, <out>.summary.json   abstraction/load_generator/metrics/api.md
"""
import csv
import glob
import json
import os

# ---------------------------------------------------------------- ingress

# The Envoy access-log line the ingress wrapper configures (one per downstream request, comma-separated):
#   start (Unix s with 9 fractional digits), end offset from start (ns), forward latency (ns), upstream host,
#   response code, response flags, upstream attempt count.
ACCESS_LOG_FORMAT = ("%START_TIME(%s.%9f)%,%COMMON_DURATION(DS_RX_BEG:US_RX_END:ns)%,"
                     "%COMMON_DURATION(US_TX_BEG:US_RX_END:ns)%,%UPSTREAM_HOST%,%RESPONSE_CODE%,"
                     "%RESPONSE_FLAGS%,%UPSTREAM_REQUEST_ATTEMPT_COUNT%\n")
# The router's upstream (per-attempt) log line: one per upstream attempt, written when the attempt ends (answered, or
# reset, e.g. a hedging loser): attempt start, end offset (ns, when an answer arrived), forward latency (ns), host,
# response code, response flags, attempt duration (ms, also set for a reset attempt), and the downstream request's
# x-request-id (shared by all attempts of one request).
UPSTREAM_LOG_FORMAT = ("%START_TIME(%s.%9f)%,%COMMON_DURATION(DS_RX_BEG:US_RX_END:ns)%,"
                       "%COMMON_DURATION(US_TX_BEG:US_RX_END:ns)%,%UPSTREAM_HOST%,%RESPONSE_CODE%,"
                       "%RESPONSE_FLAGS%,%DURATION%,%REQ(X-REQUEST-ID)%\n")
FORWARDS_HEADER = "t_ns,server_id,outcome,latency_ns,ewma_latency_ns,exec_ns,ewma_exec_ns,attempt,estimate_ns"
OUTCOMES = ("ok", "rejected", "bad_request", "bad_gateway", "reset")
# Envoy response flags meaning the downstream (the client) went away before the answer.
_RESET_FLAGS = {"DC", "DR"}


def _int(s):
    try:
        return int(s)
    except (TypeError, ValueError):
        return None


# Design: abstraction/ingress/load_balancer/design.md#forwardlog
def forward_outcome(code, flags):
    """The ForwardOutcome of one access-log line (abstraction/ingress/load_balancer/api.md#forwardoutcome)."""
    flag_set = set(flags.split(",")) if flags and flags != "-" else set()
    if flag_set & _RESET_FLAGS:
        return "reset"
    if flag_set:  # any other flag (no healthy upstream, connection failure, overflow, timeout, ...)
        return "bad_gateway"
    return {202: "ok", 503: "rejected", 400: "bad_request"}.get(code, "bad_gateway")


def parse_access_log(path):
    """[(start_unix_ns, end_unix_ns, latency_ns or None, upstream 'host:port' or None, outcome)]."""
    rows = []
    with open(path, encoding="utf-8", errors="replace") as f:
        for line in f:
            parts = line.rstrip("\n").split(",")
            if len(parts) < 7:
                continue
            start_s, end_off, lat, host, code, flags = parts[0], parts[1], parts[2], parts[3], parts[4], \
                ",".join(parts[5:-1])
            sec, _, frac = start_s.partition(".")
            if not sec.isdigit():
                continue
            start = int(sec) * 1_000_000_000 + int((frac + "000000000")[:9])
            off = _int(end_off)
            rows.append((start, start + (off if off is not None else 0), _int(lat),
                         host if host not in ("", "-") else None, forward_outcome(_int(code), flags)))
    return rows


# Design: abstraction/ingress/frontend/design.md#hedgedforwarding
def lb_retries(attempt_rows):
    """LbRetries started, as Go counts them (a refused retry is not one): upstream attempts beyond the first of
    each request, from parse_upstream_log rows (the last field is the request id). Envoy's own attempt count
    (%UPSTREAM_REQUEST_ATTEMPT_COUNT%) also counts retries that never got a host, so it is not used."""
    with_id = [r for r in attempt_rows if len(r) > 5 and r[5] not in ("", "-")]
    return len(with_id) - len({r[5] for r in with_id})


# Upstream-log flags for an attempt that ended because Envoy itself reset it (a hedging loser once another attempt
# answered, or every attempt when the client went away): the attempt has no answer of its own.
_ATTEMPT_RESET_FLAGS = {"UR", "DC", "DR"}


# Design: abstraction/ingress/frontend/design.md#forwardattempt
def attempt_outcome(code, flags):
    """The ForwardOutcome of one upstream-log line. UT (its per-try timeout fired) is not a failure under hedging:
    the attempt went on, and either answered (202 UT: ok) or was reset once another attempt answered (0 UT: reset).
    Otherwise: reset when the attempt was reset without an answer (code 0, no failure flag), else as
    forward_outcome."""
    flag_set = (set(flags.split(",")) if flags and flags != "-" else set()) - {"UT"}
    if not code and flag_set <= _ATTEMPT_RESET_FLAGS:
        return "reset"
    return forward_outcome(code, ",".join(sorted(flag_set)) or "-")


def parse_upstream_log(path):
    """[(start_unix_ns, end_unix_ns, latency_ns or None, upstream 'host:port' or None, outcome, request id)], one per
    upstream attempt (UPSTREAM_LOG_FORMAT). The end is the answer's arrival, or start + the attempt's duration for
    an attempt without one (reset)."""
    rows = []
    with open(path, encoding="utf-8", errors="replace") as f:
        for line in f:
            parts = line.rstrip("\n").split(",")
            if len(parts) < 8:
                continue
            start_s, end_off, lat, host, code = parts[0], parts[1], parts[2], parts[3], parts[4]
            flags, duration_ms, rid = ",".join(parts[5:-2]), parts[-2], parts[-1]
            sec, _, frac = start_s.partition(".")
            if not sec.isdigit():
                continue
            start = int(sec) * 1_000_000_000 + int((frac + "000000000")[:9])
            off = _int(end_off)
            if off is None:
                off = (_int(duration_ms) or 0) * 1_000_000
            rows.append((start, start + off, _int(lat), host if host not in ("", "-") else None,
                         attempt_outcome(_int(code), flags), rid))
    return rows


EWMA_INITIAL_LATENCY_NS = 1_000_000  # abstraction/ingress/load_balancer/api.md#constants


# Design: abstraction/ingress/load_balancer/design.md#forwardlog
# Design: abstraction/ingress/load_balancer/design.md#ewmalatency
# Design: abstraction/ingress/design.md#time-base
def write_forwards(rows, backends, path, ewma_beta=0.7, run_start=None):
    """forwards.csv (Go column order); returns run_start_unix_ns (given, else the first row's start, 0 if none).
    rows are access-log rows (one per request, attempt 0) or upstream-log rows (one per attempt, with a request id:
    the attempt column numbers a request's attempts 0, 1, ... in start order). ewma_latency_ns is the backend's
    EwmaLatency right after each forward, computed here from the log in end-time order exactly as defined
    (abstraction/ingress/load_balancer/design.md#ewmalatency: ok forwards only, start 1 ms, weight ewma_beta on the
    old value); Envoy's own balancer does not use it. exec, ewma_exec and estimate columns are empty (no
    ExecReport in Envoy's logs)."""
    if run_start is None:
        run_start = min((r[0] for r in rows), default=0)
    attempt = {}
    by_request = {}
    for r in rows:
        if len(r) > 5:
            by_request.setdefault(r[5], []).append(r)
    for rs in by_request.values():
        for k, r in enumerate(sorted(rs, key=lambda x: x[0])):
            attempt[id(r)] = k
    index = {b: i for i, b in enumerate(backends)}
    out = []
    for r in rows:
        start, end, lat, host, outcome = r[:5]
        sid = index.get(host)
        if sid is None:  # never reached a backend: count it against no server
            continue
        out.append((end - run_start, sid, outcome, lat if lat is not None else end - start, attempt.get(id(r), 0)))
    out.sort(key=lambda r: r[0])
    ewma = [float(EWMA_INITIAL_LATENCY_NS)] * len(backends)
    with open(path, "w", newline="") as f:
        f.write(FORWARDS_HEADER + "\n")
        for t, sid, outcome, lat, k in out:
            if outcome == "ok":
                ewma[sid] = float(float(ewma[sid] * ewma_beta) + float(float(lat) * (1 - ewma_beta)))
            f.write(f"{t},{sid},{outcome},{lat},{repr(ewma[sid])},,,{k},\n")
    return run_start


# Design: abstraction/ingress/design.md#shutdown
def ingress_summary(rows, backends, config, run_start_unix_ns, run_end_s, filter_counters=None):
    """The Ingress Summary JSON (abstraction/ingress/api.md), with the LoadCanceller and LB-internal fields
    zero/null (not implemented on impl/envoy), except the counters given in filter_counters (FilterWaitQueue
    counters from filter_counts, lb_retries, lb_retry_refused from retry_counts). rows: one per forward (attempt)."""
    index = {b: i for i, b in enumerate(backends)}
    per = [{"server_id": i, "addr": b, "forwarded": 0, **{o: 0 for o in OUTCOMES}, "ewma_latency_ns": None,
            "ewma_exec_ns": None, "ewma_anti_exec_ns": None, "worker_count": 0} for i, b in enumerate(backends)]
    for r in rows:
        host, outcome = r[3], r[4]
        i = index.get(host)
        if i is not None:
            per[i]["forwarded"] += 1
            per[i][outcome] += 1
    zero_counts = {k: 0 for k in ("scheduled", "sent", "accepted", "rejected", "bad_request", "error",
                                  "timeout", "skipped")}
    summary = {"config": config, "run_end_s": run_end_s, "run_start_unix_ns": run_start_unix_ns,
               "counts": zero_counts, "rates_per_s": {"offered": 0.0, "sent": 0.0, "accepted": 0.0},
               "clients_targeted": 0, "filter_dropped": 0, "filter_queued": 0, "filter_sent_from_queue": 0,
               "filter_queue_max": 0, "lb_retry_refused": 0, "lb_retries": 0, "backends": per}
    summary.update(filter_counters or {})
    return summary


# ---------------------------------------------------------------- Peak EWMA trace (impl/envoy only)

# Envoy's vendored Peak EWMA balancers write, with PEAK_EWMA_TRACE_PREFIX=<prefix>, files per balancer instance n
# (one instance per worker; envoy/contrib/peak_ewma/load_balancing_policies/source/peak_ewma_lb.h, PeakEwmaTrace):
#   <prefix>.ewma.<n>.csv   t_unix_ns,host,rtt_ms,dt_ns,alpha,ewma_before_ms,ewma_after_ms
#   <prefix>.picks.<n>.csv  t_unix_ns,host_a,active_a,ewma_a_ms,cost_a,host_b,active_b,ewma_b_ms,cost_b,chosen
#   <prefix>.evals.<n>.csv  t_unix_ns,decision,kind,host,active,ewma_ms,cost,remaining_ns,eligible,chosen,provisional_ms
#                           (files from before provisional_ms have 10 columns; merged with an empty value)
#   <prefix>.queue.<n>.csv  t_unix_ns,event,wait_ns,remaining_ns,host,refused   (older files: no refused)
#   <prefix>.anxious.0.csv  t_unix_ns,event,seq,host,current_ns,remaining_ns,in_flight,anxious   (AnxiousRetry:
#                           event eval (in_flight, anxious) or retry (seq, host, current_ns, remaining_ns))
#   <prefix>.exec.0.csv     t_unix_ns,host,exec_ns,backend,ewma_exec_ns,ewma_anti_ns   (one row per backend
#                           after each ExecReport sample)
#   <prefix>.tokens.0.csv   t_unix_ns,event,host,latency_ms,rate_before,rate_after,tokens   (TokenBucketFilter: take,
#                           race, untaken at a pick; stuck (StuckAttempt) at a decision, latency_ms the oldest pending
#                           attempt's age; slow, slow_held, fast, fast_held, mid at an answered attempt)
#   <prefix>.bbr.0.csv      t_unix_ns,event,host,mode,btl_bw,min_rtt_ms,pacing_rate,cwnd,inflight,in_recovery,
#                           lt_use_bw,cycle_idx,rtt_ms   (BbrFilter: admit, race at a pick; send, answer, cancel (HedgeCancel),
#                           loss of an attempt; the host's BbrState after the event, rtt_ms the attempt's age at answer or loss)
# (picks: Peak EWMA; evals and queue: peak_ewma_deadline; anxious and exec: the testbed_anxious filter). The merged outputs replace hosts by server ids
# (--backends order; an empty host stays empty) and Unix times by t_ns since run_start_unix_ns (the forwards.csv
# time base), add the instance number as `worker` where decisions or queues are per worker, and are sorted by t_ns
# (stable, in instance order).
PEAK_EWMA_UPDATES_HEADER = "t_ns,server_id,rtt_ms,dt_ns,alpha,ewma_before_ms,ewma_after_ms"
PEAK_EWMA_PICKS_HEADER = "t_ns,server_a,active_a,ewma_a_ms,cost_a,server_b,active_b,ewma_b_ms,cost_b,chosen"
PEAK_EWMA_EVALS_HEADER = ("t_ns,worker,decision,kind,server_id,active,ewma_ms,cost,remaining_ns,eligible,chosen,"
                          "provisional_ms")
PEAK_EWMA_QUEUE_HEADER = "t_ns,worker,event,wait_ns,remaining_ns,server_id,refused"
ANXIOUS_HEADER = "t_ns,event,seq,server_id,current_ns,remaining_ns,in_flight,anxious"
EXEC_HEADER = "t_ns,server_id,exec_ns,backend,ewma_exec_ns,ewma_anti_ns"
PEAK_EWMA_TOKENS_HEADER = "t_ns,event,server_id,latency_ms,rate_before,rate_after,tokens"
PEAK_EWMA_BBR_HEADER = ("t_ns,event,server_id,mode,btl_bw,min_rtt_ms,pacing_rate,cwnd,inflight,in_recovery,lt_use_bw,"
                        "cycle_idx,rtt_ms")

# kind -> (raw width, raw host columns, output file suffix, output header, add the worker column)
_PEAK_EWMA_KINDS = {
    "ewma": (7, (1,), "peak_ewma_updates", PEAK_EWMA_UPDATES_HEADER, False),
    "picks": (10, (1, 5), "peak_ewma_picks", PEAK_EWMA_PICKS_HEADER, False),
    "evals": (11, (3,), "peak_ewma_evals", PEAK_EWMA_EVALS_HEADER, True),
    "queue": (6, (4,), "peak_ewma_queue", PEAK_EWMA_QUEUE_HEADER, True),
    "anxious": (8, (3,), "anxious", ANXIOUS_HEADER, False),
    "exec": (6, (1, 3), "exec", EXEC_HEADER, False),
    "tokens": (7, (2,), "peak_ewma_tokens", PEAK_EWMA_TOKENS_HEADER, False),
    "bbr": (13, (2,), "peak_ewma_bbr", PEAK_EWMA_BBR_HEADER, False),
}


def _trace_rows(prefix, kind):
    """(paths, [(instance n, row as a list of strings)]) of every <prefix>.<kind>.<n>.csv in n order, header and
    malformed lines (e.g. a truncated last line) dropped."""
    width = _PEAK_EWMA_KINDS[kind][0]
    found = []
    for path in glob.glob(f"{glob.escape(prefix)}.{kind}.*.csv"):
        n = path[len(prefix) + len(kind) + 2:-len(".csv")]
        if n.isdigit():
            found.append((int(n), path))
    rows = []
    for n, path in sorted(found):
        with open(path, encoding="utf-8", errors="replace") as f:
            next(f, None)
            for line in f:
                parts = line.rstrip("\n").split(",")
                if kind in ("evals", "queue") and len(parts) == width - 1:  # before provisional_ms / refused
                    parts.append("")
                if len(parts) == width and parts[0].lstrip("-").isdigit():
                    rows.append((n, parts))
    return [p for _, p in sorted(found)], rows


def merge_peak_ewma(prefix, backends, run_start_unix_ns, out_prefix, remove=True):
    """Writes <out_prefix>.<suffix>.csv for every trace kind that has files (_PEAK_EWMA_KINDS); returns
    {kind: merged rows (lists of strings)}, empty lists for kinds without files. Rows naming a host that is not a
    backend are dropped. The per-instance files are deleted when remove is true."""
    index = {b: i for i, b in enumerate(backends)}
    merged_by_kind = {}
    for kind, (_, host_cols, suffix, header, add_worker) in _PEAK_EWMA_KINDS.items():
        paths, rows = _trace_rows(prefix, kind)
        merged = []
        for n, parts in rows:
            if any(parts[c] != "" and parts[c] not in index for c in host_cols):
                continue
            row = [str(int(parts[0]) - run_start_unix_ns)] + parts[1:]
            for c in host_cols:
                row[c] = str(index[parts[c]]) if parts[c] != "" else ""
            if add_worker:
                row.insert(1, str(n))
            merged.append(row)
        merged.sort(key=lambda r: int(r[0]))
        if paths:
            with open(f"{out_prefix}.{suffix}.csv", "w", newline="") as f:
                f.write(header + "\n")
                for row in merged:
                    f.write(",".join(row) + "\n")
        if remove:
            for path in paths:
                os.remove(path)
        merged_by_kind[kind] = merged
    return merged_by_kind


# Design: abstraction/ingress/load_balancer/design.md#ewmaexectime
# Design: abstraction/ingress/load_balancer/design.md#ewmaantiservicetime
def exec_ewmas(exec_rows, n_backends):
    """[(ewma_exec_ns, ewma_anti_exec_ns)] per backend (floats or None): the last values in the merged exec trace
    (t_ns,server_id,exec_ns,backend,ewma_exec_ns,ewma_anti_ns); all None without one."""
    out = [(None, None)] * n_backends
    for row in exec_rows:
        if row[3] != "":
            b = int(row[3])
            out[b] = (float(row[4]) if row[4] else None, float(row[5]) if row[5] else None)
    return out


# Design: abstraction/ingress/load_balancer/design.md#peakewmadeadline
def retry_counts(queue_rows):
    """lb_retry_refused: refused picks of peak_ewma_deadline LB retries, one per refused pick as in Go (a refused
    LbRetry is re-picked at every tick), from the refused counts of the retry_sent / retry_gone queue events
    (merged queue rows: t_ns,worker,event,wait_ns,remaining_ns,server_id,refused). A retry still waiting when
    the trace ends is not counted."""
    return {"lb_retry_refused": sum(int(row[6]) for row in queue_rows
                                    if row[2] in ("retry_sent", "retry_gone") and row[6] != "")}


# Design: abstraction/ingress/load_balancer/design.md#filterwaitqueue
# Design: abstraction/ingress/load_balancer/design.md#peakewmadeadline
def filter_counts(queue_rows):
    """The Ingress summary's FilterWaitQueue counters from merged queue rows (t_ns,worker,event,...): requests
    queued, sent from the queue, dropped past their deadline, and the longest total queue over all workers (events
    replayed in t_ns order: queued +1; sent, dropped, gone -1)."""
    counts = {"filter_queued": 0, "filter_sent_from_queue": 0, "filter_dropped": 0, "filter_queue_max": 0}
    length = 0
    for row in queue_rows:
        event = row[2]
        if event == "queued":
            counts["filter_queued"] += 1
            length += 1
            counts["filter_queue_max"] = max(counts["filter_queue_max"], length)
        elif event in ("sent", "dropped", "gone"):
            length -= 1
            if event == "sent":
                counts["filter_sent_from_queue"] += 1
            elif event == "dropped":
                counts["filter_dropped"] += 1
    return counts


# ---------------------------------------------------------------- loadgen

REQUESTS_HEADER = ("request_id,client_id,seq,outcome,http_status,scheduled_ns,send_ns,end_ns,e2e_latency_ns,"
                   "service_latency_ns,copies,in_stall_window,exec_ns,start_slack_ns")


# Design: abstraction/protocol/design.md#status-classification-load-generator-client
# Design: abstraction/load_generator/client/design.md#outcome-classification
def request_outcome(result, status):
    """The client Outcome (abstraction/protocol/design.md#status-classification-load-generator-client) of one
    Nighthawk per-request log line. A stream reset only happens when Nighthawk stops at the end of the run with the
    request outstanding: the DrainDeadline's 'cancelled'."""
    if result == "response":
        return {202: "ok", 503: "rejected"}.get(status, "error")
    if result == "reset":
        return "cancelled"
    return "error"  # pool_failure: no connection


# Design: abstraction/load_generator/metrics/design.md#collection
def read_request_logs(prefix):
    """All rows of <prefix>.<n>.csv as dicts."""
    rows = []
    for path in sorted(glob.glob(f"{glob.escape(prefix)}.*.csv")):
        with open(path, newline="", encoding="utf-8", errors="replace") as f:
            rows.extend(r for r in csv.DictReader(f) if r.get("id"))
    return rows


# Design: abstraction/load_generator/metrics/design.md#requestrecord
# Design: abstraction/load_generator/client/design.md#stallwindow
# Design: abstraction/load_generator/design.md#time-base
def requests_records(rows, initial_stall_ms=0):
    """Rows of the Requests CSV, sorted by (client_id, seq), and run_start_unix_ns (the first scheduled time)."""
    sched = [_int(r["scheduled_unix_ns"]) for r in rows if _int(r["scheduled_unix_ns"])]
    run_start = min(sched, default=0)
    stall_ns = initial_stall_ms * 1_000_000
    out = []
    for r in rows:
        rid = int(r["id"])
        status = _int(r["http_status"]) or 0
        sched_ns = (_int(r["scheduled_unix_ns"]) or run_start) - run_start
        send = _int(r["send_unix_ns"])
        send_ns = send - run_start if send else None
        end_ns = (_int(r["end_unix_ns"]) or run_start) - run_start
        out.append({"request_id": rid, "client_id": rid >> 32, "seq": rid & 0xFFFFFFFF,
                    "outcome": request_outcome(r["result"], status),
                    "http_status": status if r["result"] == "response" else None,
                    "scheduled_ns": sched_ns, "send_ns": send_ns, "end_ns": end_ns,
                    "e2e_latency_ns": end_ns - sched_ns,
                    "service_latency_ns": end_ns - send_ns if send_ns is not None else None,
                    "copies": 1 if send_ns is not None else 0,
                    "in_stall_window": 1 if send_ns is not None and send_ns < stall_ns else 0,
                    "exec_ns": r.get("exec_ns") or None, "start_slack_ns": r.get("start_slack_ns") or None})
    out.sort(key=lambda x: (x["client_id"], x["seq"]))
    return out, run_start


# Design: abstraction/load_generator/metrics/design.md#requestrecord
def write_requests(records, path):
    cols = REQUESTS_HEADER.split(",")
    with open(path, "w", newline="") as f:
        f.write(REQUESTS_HEADER + "\n")
        for rec in records:
            f.write(",".join("" if rec[c] is None else str(rec[c]) for c in cols) + "\n")


# Design: abstraction/load_generator/metrics/design.md#percentiles
def latency_stats(values):
    """Nearest rank (abstraction/load_generator/metrics/design.md#percentiles), in µs."""
    n = len(values)
    if n == 0:
        return {"count": 0, "mean": None, "p50": None, "p90": None, "p99": None, "p999": None, "max": None}
    v = sorted(values)
    pct = lambda pm: v[(pm * n + 99999) // 100000 - 1] / 1000
    return {"count": n, "mean": sum(v) / n / 1000, "p50": pct(50000), "p90": pct(90000), "p99": pct(99000),
            "p999": pct(99900), "max": v[-1] / 1000}


# Design: abstraction/load_generator/metrics/design.md#summary
def loadgen_summary(records, config, run_start_unix_ns, run_end_s):
    """The LoadGen Summary JSON (abstraction/load_generator/metrics/api.md#summary-json)."""
    duration_s = float(config["duration_s"])
    stall_ms = float(config.get("initial_stall_ms", 0) or 0)
    slo_ns = int(round(float(config["slo_ms"]) * 1_000_000))
    measured_s = duration_s - stall_ms / 1000
    stall = [r for r in records if r["in_stall_window"]]
    rs = [r for r in records if not r["in_stall_window"]]
    counts = {k: 0 for k in ("scheduled", "sent", "ok", "rejected", "cancelled", "dropped", "error", "not_sent",
                             "copies_sent", "stall_window", "ok_late")}
    counts["stall_window"] = len(stall)
    for r in rs:
        counts["scheduled"] += 1
        if r["send_ns"] is None:
            counts["not_sent"] += 1
            continue
        counts["sent"] += 1
        counts["copies_sent"] += r["copies"]
        counts[r["outcome"]] += 1
        if r["outcome"] == "ok" and r["end_ns"] - r["send_ns"] > slo_ns:
            counts["ok_late"] += 1
    ok = [r for r in rs if r["outcome"] == "ok"]
    return {"config": config, "run_end_s": run_end_s, "run_start_unix_ns": run_start_unix_ns, "counts": counts,
            "rates_per_s": {"offered": counts["scheduled"] / measured_s, "sent": counts["sent"] / measured_s,
                            "goodput": (counts["ok"] - counts["ok_late"]) / measured_s},
            "e2e_latency_us": latency_stats([r["e2e_latency_ns"] for r in ok]),
            "service_latency_us": latency_stats([r["service_latency_ns"] for r in ok])}


def write_json(obj, path):
    with open(path, "w") as f:
        json.dump(obj, f, indent=2)
        f.write("\n")
