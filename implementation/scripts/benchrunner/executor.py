# Tooling language: Python 3.11+ (shared tool implementation/scripts)
# Source: abstraction/benchmark_runner/experiment_executor/design.md (experiment_executor v0.4.0)
"""run_point: ServerLifecycle, IngressLifecycle, ClientRun and point outputs for one SweepPoint."""

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
MAX_INSTANCES = 64


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
    # Design: abstraction/benchmark_runner/experiment_executor/design.md#ingresslifecycle
    on_server = getattr(point, "ingress_machine", "client") == "server"
    ingress_host = server if on_server else client
    write_placement(point, server, client, ingress_host, pdir)
    rd_server, rd_client = remote_dir(server, run_id, point.id), remote_dir(client, run_id, point.id)
    rd_ingress = rd_server if on_server else rd_client
    reason, interrupted = None, False
    try:
        server.transport.run(f"mkdir -p {quote(rd_server)}", 30)
        client.transport.run(f"mkdir -p {quote(rd_client)}", 30)
        for m in (server, client):  # leftovers from a crash, either placement
            stop_ingress(m, config.run.stop_timeout_s)
        stop_leftover_servers(server, config.run.stop_timeout_s)
        start_servers(point, config, server, rd_server)
        start_ingress(point, config, ingress_host, rd_ingress)
        if not client_run(point, config, client, rd_client):
            reason = "client run failed or timed out"
    except (PointFailure, TransportError) as e:
        reason = str(e)
    except KeyboardInterrupt:
        reason, interrupted = "interrupted", True
    finally:  # reverse start order
        try:
            if not stop_ingress(ingress_host, config.run.stop_timeout_s) and reason is None:
                reason = "ingress did not stop on SIGINT"
            if not stop_servers(server, point.server_instances, config.run.stop_timeout_s) and reason is None:
                reason = "server did not stop on SIGINT"
            collect_outputs(point, server, client, rd_server, rd_client, pdir, ingress_host, rd_ingress)
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


# Design: abstraction/benchmark_runner/experiment_executor/design.md#backgroundprogram
def start_program(m, name, words, log, pid_file, ready_timeout_s):
    """Start `exec <words>` in the background, record its pid, wait for READY_LINE."""
    t = m.transport
    pid = t.start_background("exec " + join(words), log)  # exec: the pid is the program itself
    t.run(f"echo {pid} > {quote(pid_file)}", 30)
    deadline = time.monotonic() + ready_timeout_s
    while True:
        if t.run(f"grep -q {quote(READY_LINE)} {quote(log)}", 30).exit_status == 0:
            return
        if not t.alive(pid):
            tail = t.run(f"tail -n 20 {quote(log)}", 30).stdout.strip()
            raise PointFailure(f"{name} exited during startup: {tail}")
        if time.monotonic() >= deadline:
            raise PointFailure(f"{name} not ready after {ready_timeout_s} s")
        time.sleep(POLL_INTERVAL_S)


# Design: abstraction/benchmark_runner/experiment_executor/design.md#backgroundprogram
def stop_program(m, pid_file, stop_timeout_s):
    """Interrupt the recorded program, if any. False if it had to be killed."""
    t = m.transport
    r = t.run(f"cat {quote(pid_file)}", 30)
    if r.exit_status != 0 or not r.stdout.strip().isdigit():
        return True  # nothing recorded
    pid, ok = int(r.stdout.strip()), True
    if t.alive(pid):
        t.signal(pid, "INT")  # it writes its outputs and exits 0
        deadline = time.monotonic() + stop_timeout_s
        while t.alive(pid) and time.monotonic() < deadline:
            time.sleep(POLL_INTERVAL_S)
        if t.alive(pid):
            t.signal(pid, "KILL")
            ok = False
    t.run(f"rm -f {quote(pid_file)}", 30)
    return ok


def _server_pid_file(server, k):
    return f"{server.workdir}/server_{k}.pid"


def _ingress_pid_file(client):
    return f"{client.workdir}/ingress.pid"


# Design: abstraction/benchmark_runner/experiment_executor/design.md#serverlifecycle
def start_servers(point, config, server, rd):
    for k in range(point.server_instances):
        words = placement_prefix("server", point, server) + \
            [server.binaries["server"], "--listen_addr", f"0.0.0.0:{config.listen_port + k}"] + \
            arg_words(point.server_args)
        start_program(server, f"server {k}", words, f"{rd}/server_{k}.out", _server_pid_file(server, k),
                      config.run.server_ready_timeout_s)


# Design: abstraction/benchmark_runner/experiment_executor/design.md#serverlifecycle
def stop_servers(server, count, stop_timeout_s):
    ok = True
    for k in range(count):
        ok = stop_program(server, _server_pid_file(server, k), stop_timeout_s) and ok
    return ok


# Design: abstraction/benchmark_runner/experiment_executor/design.md#run_point
def stop_leftover_servers(server, stop_timeout_s):
    """Stop every server_<k>.pid present (k < MAX_INSTANCES), listed in one command."""
    r = server.transport.run(f"cd {quote(server.workdir)} 2>/dev/null && ls -1 server_*.pid 2>/dev/null; true", 30)
    ks = sorted(int(n[len("server_"):-len(".pid")]) for n in r.stdout.split()
                if n.startswith("server_") and n.endswith(".pid") and n[len("server_"):-len(".pid")].isdigit())
    for k in ks:
        if k < MAX_INSTANCES:
            stop_program(server, _server_pid_file(server, k), stop_timeout_s)


# Design: abstraction/benchmark_runner/experiment_executor/design.md#ingresslifecycle
def start_ingress(point, config, m, rd):
    """Start the Ingress on machine m (the IngressPlacement's ingress_host)."""
    words = placement_prefix("ingress", point, m) + \
        [m.binaries["ingress"], "--listen_addr", f"0.0.0.0:{config.ingress_port}"] + \
        arg_words(point.ingress_args) + ["--output_path", f"{rd}/ingress"]
    start_program(m, "ingress", words, f"{rd}/ingress.log", _ingress_pid_file(m), config.run.server_ready_timeout_s)


# Design: abstraction/benchmark_runner/experiment_executor/design.md#ingresslifecycle
def stop_ingress(m, stop_timeout_s):
    return stop_program(m, _ingress_pid_file(m), stop_timeout_s)


# Design: abstraction/benchmark_runner/experiment_executor/design.md#clientrun
def client_run(point, config, client, rd):
    target = getattr(point, "loadgen_target", "") or f"127.0.0.1:{config.ingress_port}"
    lg = placement_prefix("loadgen", point, client) + \
        [client.binaries["loadgen"], "--target", target] + arg_words(point.loadgen_args) + \
        ["--output_path", f"{rd}/loadgen"]
    lines = [f"cd {quote(rd)} || exit 97",
             f"{join(lg)} > {quote(rd + '/loadgen.log')} 2>&1 &",
             f"LP=$!; echo $LP > {quote(rd + '/loadgen.pid')}",
             "wait $LP; L=$?",
             'echo "loadgen_exit=$L"',
             '[ "$L" -eq 0 ]']
    expected = _float(point.arg("loadgen", "duration_s")) + _float(point.arg("loadgen", "slo_ms")) / 1000
    r = client.transport.run("\n".join(lines), expected + config.run.client_timeout_margin_s)
    if r.exit_status == -1:  # timed out: kill what is left
        pid = quote(f"{rd}/loadgen.pid")
        client.transport.run(f'[ -f {pid} ] && kill -s KILL "$(cat {pid})"; true', 30)
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
def write_placement(point, server, client, ingress_host, pdir):
    doc = {"server": placement_prefix("server", point, server),  # the same for every instance
           "loadgen": placement_prefix("loadgen", point, client),
           "ingress": placement_prefix("ingress", point, ingress_host)}
    with open(os.path.join(pdir, "placement.json"), "w") as f:
        json.dump(doc, f, indent=2)


COUNTER_KEYS = ("cancelled_before_processing", "cancelled_during_processing", "completed_before_cancel")


# Design: abstraction/benchmark_runner/experiment_executor/design.md#point-outputs
def collect_outputs(point, server, client, rd_server, rd_client, pdir, ingress_host=None, rd_ingress=None):
    for name in ("loadgen.requests.csv", "loadgen.summary.json", "loadgen.log"):
        client.transport.fetch(f"{rd_client}/{name}", os.path.join(pdir, name))
    ingress_host, rd_ingress = ingress_host or client, rd_ingress or rd_client
    for name in ("ingress.cancels.csv", "ingress.summary.json", "ingress.log"):
        ingress_host.transport.fetch(f"{rd_ingress}/{name}", os.path.join(pdir, name))
    counters = []
    for k in range(point.server_instances):
        out = os.path.join(pdir, f"server_{k}.out")
        server.transport.fetch(f"{rd_server}/server_{k}.out", out)
        c = _last_counters(out)
        if c is not None:
            with open(os.path.join(pdir, f"server_counters_{k}.json"), "w") as f:
                json.dump(c, f, indent=2)
            counters.append(c)
    if len(counters) == point.server_instances:  # every instance reported
        total = {key: sum(c.get(key, 0) for c in counters) for key in COUNTER_KEYS}
        with open(os.path.join(pdir, "server_counters.json"), "w") as f:
            json.dump(total, f, indent=2)


def _last_counters(path):
    if not os.path.exists(path):
        return None
    with open(path, encoding="utf-8", errors="replace") as f:
        lines = [l.strip() for l in f if l.startswith(COUNTERS_PREFIX)]
    if not lines:
        return None
    try:
        return json.loads(lines[-1])
    except json.JSONDecodeError:
        return None


# Design: abstraction/benchmark_runner/experiment_executor/design.md#point-outputs
def validate_outputs(point, pdir):
    if not os.path.exists(os.path.join(pdir, "loadgen.summary.json")):
        return "loadgen summary missing"
    if not os.path.exists(os.path.join(pdir, "ingress.summary.json")):
        return "ingress summary missing"
    if not os.path.exists(os.path.join(pdir, "server_counters.json")):
        missing = [k for k in range(point.server_instances)
                   if not os.path.exists(os.path.join(pdir, f"server_counters_{k}.json"))]
        return f"server CancelCounters line missing (instance {', '.join(map(str, missing)) or '?'})"
    return None
