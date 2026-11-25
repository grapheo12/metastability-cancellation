"""impl/envoy wrappers: pure conversions from Envoy's access log and Nighthawk's per-request logs to the testbed's
output files (no I/O beyond reading and writing the given paths, so they are unit-tested without Envoy or Nighthawk).

Formats produced:
  ingress  <out>.forwards.csv, <out>.summary.json   abstraction/ingress/api.md#outputs
  loadgen  <out>.requests.csv, <out>.summary.json   abstraction/load_generator/metrics/api.md
"""
import csv
import glob
import json

# ---------------------------------------------------------------- ingress

# The Envoy access-log line the ingress wrapper configures (one per downstream request, comma-separated):
#   start (Unix s with 9 fractional digits), end offset from start (ns), forward latency (ns), upstream host,
#   response code, response flags, upstream attempt count.
ACCESS_LOG_FORMAT = ("%START_TIME(%s.%9f)%,%COMMON_DURATION(DS_RX_BEG:US_RX_END:ns)%,"
                     "%COMMON_DURATION(US_TX_BEG:US_RX_END:ns)%,%UPSTREAM_HOST%,%RESPONSE_CODE%,"
                     "%RESPONSE_FLAGS%,%UPSTREAM_REQUEST_ATTEMPT_COUNT%\n")
FORWARDS_HEADER = "t_ns,server_id,outcome,latency_ns,ewma_latency_ns,exec_ns,ewma_exec_ns,attempt,estimate_ns"
OUTCOMES = ("ok", "rejected", "bad_request", "bad_gateway", "reset")
# Envoy response flags meaning the downstream (the client) went away before the answer.
_RESET_FLAGS = {"DC", "DR"}


def _int(s):
    try:
        return int(s)
    except (TypeError, ValueError):
        return None


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


EWMA_INITIAL_LATENCY_NS = 1_000_000  # abstraction/ingress/load_balancer/api.md#constants


def write_forwards(rows, backends, path, ewma_beta=0.7):
    """forwards.csv (Go column order); returns run_start_unix_ns (the first request's start, 0 if none).
    ewma_latency_ns is the backend's EwmaLatency right after each forward, computed here from the log in end-time
    order exactly as defined (abstraction/ingress/load_balancer/design.md#ewmalatency: ok forwards only, start
    1 ms, weight ewma_beta on the old value); Envoy's own balancer does not use it. exec, ewma_exec and estimate
    columns are empty (no ExecReport in Envoy's access log); attempt is 0 (no LbRetries)."""
    run_start = min((r[0] for r in rows), default=0)
    index = {b: i for i, b in enumerate(backends)}
    out = []
    for start, end, lat, host, outcome in rows:
        sid = index.get(host)
        if sid is None:  # never reached a backend: count it against no server
            continue
        out.append((end - run_start, sid, outcome, lat if lat is not None else end - start))
    out.sort(key=lambda r: r[0])
    ewma = [float(EWMA_INITIAL_LATENCY_NS)] * len(backends)
    with open(path, "w", newline="") as f:
        f.write(FORWARDS_HEADER + "\n")
        for t, sid, outcome, lat in out:
            if outcome == "ok":
                ewma[sid] = float(float(ewma[sid] * ewma_beta) + float(float(lat) * (1 - ewma_beta)))
            f.write(f"{t},{sid},{outcome},{lat},{repr(ewma[sid])},,,0,\n")
    return run_start


def ingress_summary(rows, backends, config, run_start_unix_ns, run_end_s):
    """The Ingress Summary JSON (abstraction/ingress/api.md), with the LoadCanceller and LB-internal fields
    zero/null (not implemented on impl/envoy)."""
    index = {b: i for i, b in enumerate(backends)}
    per = [{"server_id": i, "addr": b, "forwarded": 0, **{o: 0 for o in OUTCOMES}, "ewma_latency_ns": None,
            "ewma_exec_ns": None, "ewma_anti_exec_ns": None, "worker_count": 0} for i, b in enumerate(backends)]
    for _, _, _, host, outcome in rows:
        i = index.get(host)
        if i is not None:
            per[i]["forwarded"] += 1
            per[i][outcome] += 1
    zero_counts = {k: 0 for k in ("scheduled", "sent", "accepted", "rejected", "bad_request", "error",
                                  "timeout", "skipped")}
    return {"config": config, "run_end_s": run_end_s, "run_start_unix_ns": run_start_unix_ns,
            "counts": zero_counts, "rates_per_s": {"offered": 0.0, "sent": 0.0, "accepted": 0.0},
            "clients_targeted": 0, "filter_dropped": 0, "filter_queued": 0, "filter_sent_from_queue": 0,
            "filter_queue_max": 0, "lb_retry_refused": 0, "lb_retries": 0, "backends": per}


# ---------------------------------------------------------------- loadgen

REQUESTS_HEADER = ("request_id,client_id,seq,outcome,http_status,scheduled_ns,send_ns,end_ns,e2e_latency_ns,"
                   "service_latency_ns,copies,in_stall_window,exec_ns,start_slack_ns")


def request_outcome(result, status):
    """The client Outcome (abstraction/protocol/design.md#status-classification-load-generator-client) of one
    Nighthawk per-request log line. A stream reset only happens when Nighthawk stops at the end of the run with the
    request outstanding: the DrainDeadline's 'cancelled'."""
    if result == "response":
        return {202: "ok", 503: "rejected"}.get(status, "error")
    if result == "reset":
        return "cancelled"
    return "error"  # pool_failure: no connection


def read_request_logs(prefix):
    """All rows of <prefix>.<n>.csv as dicts."""
    rows = []
    for path in sorted(glob.glob(f"{glob.escape(prefix)}.*.csv")):
        with open(path, newline="", encoding="utf-8", errors="replace") as f:
            rows.extend(r for r in csv.DictReader(f) if r.get("id"))
    return rows


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


def write_requests(records, path):
    cols = REQUESTS_HEADER.split(",")
    with open(path, "w", newline="") as f:
        f.write(REQUESTS_HEADER + "\n")
        for rec in records:
            f.write(",".join("" if rec[c] is None else str(rec[c]) for c in cols) + "\n")


def latency_stats(values):
    """Nearest rank (abstraction/load_generator/metrics/design.md#percentiles), in µs."""
    n = len(values)
    if n == 0:
        return {"count": 0, "mean": None, "p50": None, "p90": None, "p99": None, "p999": None, "max": None}
    v = sorted(values)
    pct = lambda pm: v[(pm * n + 99999) // 100000 - 1] / 1000
    return {"count": n, "mean": sum(v) / n / 1000, "p50": pct(50000), "p90": pct(90000), "p99": pct(99000),
            "p999": pct(99900), "max": v[-1] / 1000}


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
