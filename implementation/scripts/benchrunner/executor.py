# Tooling language: Python 3.11+ (shared tool implementation/scripts)
# Source: abstraction/benchmark_runner/experiment_executor/design.md (experiment_executor v0.2.0)
"""run_point: ServerLifecycle, ClientRun and point outputs for one SweepPoint."""

import json
import os
import shutil
import time
from dataclasses import dataclass

from .config import arg_words
from .transport import TransportError, join, quote

# API: abstraction/benchmark_runner/experiment_executor/api.md#constants
READY_LINE = "listening on"
COUNTERS_PREFIX = '{"cancelled_before_processing":'
POLL_INTERVAL_S = 0.2
CANCELLER_DRAIN_S = 1.0


@dataclass
class PointResult:
    status: str  # "done" | "skipped" | "failed"
    reason: str | None = None


class PointFailure(Exception):
    """A step of a point failed; the message is the reason."""


def point_dir(output_dir, pid):
    return os.path.join(output_dir, "points", f"point_{pid:04d}")


def remote_dir(machine, run_id, pid):
    return f"{machine.workdir}/runs/{run_id}/point_{pid:04d}"


def _float(value, default=0.0):
    try:
        return float(value)
    except (TypeError, ValueError):
        return default


# Design: abstraction/benchmark_runner/experiment_executor/design.md#run_point
def run_point(point, config, server, client, run_id):
    pdir = point_dir(config.run.output_dir, point.id)
    if os.path.exists(os.path.join(pdir, "done")):
        return PointResult("skipped")
    shutil.rmtree(pdir, ignore_errors=True)  # drop any previous partial attempt
    os.makedirs(pdir)
    with open(os.path.join(pdir, "params.json"), "w") as f:
        json.dump(point.to_json(), f, indent=2)
    write_placement(point, server, client, pdir)
    rd_server, rd_client = remote_dir(server, run_id, point.id), remote_dir(client, run_id, point.id)
    reason, interrupted = None, False
    try:
        server.transport.run(f"mkdir -p {quote(rd_server)}", 30)
        client.transport.run(f"mkdir -p {quote(rd_client)}", 30)
        stop_server(server, config.run.stop_timeout_s)  # leftover from a crash
        start_server(point, config, server, rd_server)
        if not client_run(point, config, server, client, rd_client):
            reason = "client run failed or timed out"
    except (PointFailure, TransportError) as e:
        reason = str(e)
    except KeyboardInterrupt:
        reason, interrupted = "interrupted", True
    finally:
        try:
            if not stop_server(server, config.run.stop_timeout_s) and reason is None:
                reason = "server did not stop on SIGINT"
            collect_outputs(point, server, client, rd_server, rd_client, pdir)
        except TransportError as e:
            reason = reason or f"while stopping/collecting: {e}"
    if reason is None:
        reason = validate_outputs(point, pdir)
    if reason is None:
        open(os.path.join(pdir, "done"), "w").close()
        result = PointResult("done")
    else:
        with open(os.path.join(pdir, "failed"), "w") as f:
            f.write(reason + "\n")
        result = PointResult("failed", reason)
    if interrupted:
        raise KeyboardInterrupt
    return result


def _pid_file(server):
    return f"{server.workdir}/server.pid"


# Design: abstraction/benchmark_runner/experiment_executor/design.md#serverlifecycle
def start_server(point, config, server, rd):
    t = server.transport
    log = f"{rd}/server.out"
    words = placement_prefix("server", point, server) + \
        [server.binaries["server"], "--listen_addr", f"0.0.0.0:{config.listen_port}"] + arg_words(point.server_args)
    pid = t.start_background("exec " + join(words), log)  # exec: the pid is the server itself
    t.run(f"echo {pid} > {quote(_pid_file(server))}", 30)
    deadline = time.monotonic() + config.run.server_ready_timeout_s
    while True:
        if t.run(f"grep -q {quote(READY_LINE)} {quote(log)}", 30).exit_status == 0:
            return
        if not t.alive(pid):
            tail = t.run(f"tail -n 20 {quote(log)}", 30).stdout.strip()
            raise PointFailure(f"server exited during startup: {tail}")
        if time.monotonic() >= deadline:
            raise PointFailure(f"server not ready after {config.run.server_ready_timeout_s} s")
        time.sleep(POLL_INTERVAL_S)


# Design: abstraction/benchmark_runner/experiment_executor/design.md#serverlifecycle
def stop_server(server, stop_timeout_s):
    """Interrupt the recorded server, if any. False if it had to be killed."""
    t = server.transport
    r = t.run(f"cat {quote(_pid_file(server))}", 30)
    if r.exit_status != 0 or not r.stdout.strip().isdigit():
        return True  # no server recorded
    pid, ok = int(r.stdout.strip()), True
    if t.alive(pid):
        t.signal(pid, "INT")  # the server prints CancelCounters and exits 0
        deadline = time.monotonic() + stop_timeout_s
        while t.alive(pid) and time.monotonic() < deadline:
            time.sleep(POLL_INTERVAL_S)
        if t.alive(pid):
            t.signal(pid, "KILL")
            ok = False
    t.run(f"rm -f {quote(_pid_file(server))}", 30)
    return ok


# Design: abstraction/benchmark_runner/experiment_executor/design.md#clientrun
def client_run(point, config, server, client, rd):
    target = f"{server.data_addr}:{config.listen_port}"
    lg = placement_prefix("loadgen", point, client) + \
        [client.binaries["loadgen"], "--target", target] + arg_words(point.loadgen_args) + \
        ["--output_path", f"{rd}/loadgen"]
    lines = [f"cd {quote(rd)} || exit 97", "CP=; C=0"]
    enabled = point.canceller_args is not None
    if enabled:
        cx = placement_prefix("canceller", point, client) + \
            [client.binaries["canceller"], "--target", target] + arg_words(point.canceller_args) + \
            ["--output_path", f"{rd}/canceller"]
        lines += [f"{join(cx)} > {quote(rd + '/canceller.log')} 2>&1 &",
                  f"CP=$!; echo $CP > {quote(rd + '/canceller.pid')}"]
    lines += [f"{join(lg)} > {quote(rd + '/loadgen.log')} 2>&1 &",
              f"LP=$!; echo $LP > {quote(rd + '/loadgen.pid')}",
              "wait $LP; L=$?"]
    if enabled:
        lines.append("wait $CP; C=$?")
    lines += ['echo "loadgen_exit=$L canceller_exit=$C"', '[ "$L" -eq 0 ] && [ "$C" -eq 0 ]']
    expected = _float(point.arg("loadgen", "duration_s")) + _float(point.arg("loadgen", "slo_ms")) / 1000
    if enabled:
        expected = max(expected, _float(point.arg("canceller", "duration_s")) + CANCELLER_DRAIN_S)
    r = client.transport.run("\n".join(lines), expected + config.run.client_timeout_margin_s)
    if r.exit_status == -1:  # timed out: kill what is left
        pids = " ".join(quote(f"{rd}/{p}.pid") for p in ("loadgen", "canceller"))
        client.transport.run(f'for f in {pids}; do [ -f "$f" ] && kill -s KILL "$(cat "$f")"; done; true', 30)
    return r.exit_status == 0


# Design: abstraction/benchmark_runner/experiment_executor/design.md#processplacement
def placement_prefix(program, point, ready):
    p = (point.placement or {}).get(program) or {}
    words = []
    if p.get("threads") is not None:
        words += ["env", f"{ready.thread_env}={p['threads']}"]  # thread_env is set: the run flow checked it
    if p.get("cpu_set") is not None and ready.cpu_pinning:
        words += ["taskset", "-c", p["cpu_set"]]
    return words


# Design: abstraction/benchmark_runner/experiment_executor/design.md#processplacement
def write_placement(point, server, client, pdir):
    doc = {"server": placement_prefix("server", point, server), "loadgen": placement_prefix("loadgen", point, client)}
    if point.canceller_args is not None:
        doc["canceller"] = placement_prefix("canceller", point, client)
    with open(os.path.join(pdir, "placement.json"), "w") as f:
        json.dump(doc, f, indent=2)


# Design: abstraction/benchmark_runner/experiment_executor/design.md#point-outputs
def collect_outputs(point, server, client, rd_server, rd_client, pdir):
    names = ["loadgen.requests.csv", "loadgen.summary.json", "loadgen.log"]
    if point.canceller_args is not None:
        names += ["canceller.cancels.csv", "canceller.summary.json", "canceller.log"]
    for name in names:
        client.transport.fetch(f"{rd_client}/{name}", os.path.join(pdir, name))
    server.transport.fetch(f"{rd_server}/server.out", os.path.join(pdir, "server.out"))
    out = os.path.join(pdir, "server.out")
    if os.path.exists(out):
        with open(out, encoding="utf-8", errors="replace") as f:
            lines = [l.strip() for l in f if l.startswith(COUNTERS_PREFIX)]
        if lines:
            try:
                counters = json.loads(lines[-1])
            except json.JSONDecodeError:
                return
            with open(os.path.join(pdir, "server_counters.json"), "w") as f:
                json.dump(counters, f, indent=2)


# Design: abstraction/benchmark_runner/experiment_executor/design.md#point-outputs
def validate_outputs(point, pdir):
    if not os.path.exists(os.path.join(pdir, "loadgen.summary.json")):
        return "loadgen summary missing"
    if point.canceller_args is not None and not os.path.exists(os.path.join(pdir, "canceller.summary.json")):
        return "canceller summary missing"
    if not os.path.exists(os.path.join(pdir, "server_counters.json")):
        return "server CancelCounters line missing"
    return None
