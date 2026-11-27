# Tooling language: Python 3.11+ (shared tool implementation/scripts)
# Source: abstraction/benchmark_runner/experiment_executor/design.md (experiment_executor v0.14.0)
"""run_point: ServerLifecycle, IngressLifecycle, ClientRun and point outputs for one SweepPoint."""

import json
import os
import re
import shutil
import time
from dataclasses import dataclass

from .config import arg_words, format_arg
from .transport import TransportError, join, quote

# API: abstraction/benchmark_runner/experiment_executor/api.md#constants
READY_LINE = "listening on"
COUNTERS_PREFIX = '{"cancelled_before_processing":'
POLL_INTERVAL_S = 0.2


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


def _as_list(x):
    """servers (and their remote dirs) may be given as one machine for a single ServerMachine."""
    return x if isinstance(x, list) else [x]


# Design: abstraction/benchmark_runner/experiment_executor/design.md#serverlifecycle
def local_index(hosts, k):
    """Position of instance k on its own server machine: it listens on listen_port + local_index."""
    return sum(1 for j in range(k) if hosts[j] == hosts[k])


def _float(value, default=0.0):
    try:
        return float(value)
    except (TypeError, ValueError):
        return default


# Design: abstraction/benchmark_runner/experiment_executor/design.md#run_point
def run_point(point, config, servers, client, run_id):
    pdir = point_dir(config.run.output_dir, point.id)
    if os.path.exists(os.path.join(pdir, "done")):
        return PointResult("skipped")
    shutil.rmtree(pdir, ignore_errors=True)  # drop any previous partial attempt
    os.makedirs(pdir)
    with open(os.path.join(pdir, "params.json"), "w") as f:
        json.dump(point.to_json(), f, indent=2)
    # Design: abstraction/benchmark_runner/experiment_executor/design.md#ingresslifecycle
    servers = _as_list(servers)  # ServerMachines, in config order
    on_server = getattr(point, "ingress_machine", "client") == "server"
    ingress_host = servers[0] if on_server else client  # Config allows "server" with one server machine only
    write_placement(point, servers, client, ingress_host, pdir)
    rd_servers = [remote_dir(m, run_id, point.id) for m in servers]
    rd_client = remote_dir(client, run_id, point.id)
    rd_ingress = rd_servers[0] if on_server else rd_client
    reason, interrupted = None, False
    try:
        for m, rd in list(zip(servers, rd_servers)) + [(client, rd_client)]:
            m.transport.run(f"mkdir -p {quote(rd)}", 30)
        for m in servers + [client]:  # leftovers from a crash, either placement
            stop_ingress(m, config.run.stop_timeout_s)
        for m in servers:
            stop_leftover_servers(m, config.run.stop_timeout_s)
        start_servers(point, config, servers, rd_servers)
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
            if not stop_servers(servers, point.hosts(), config.run.stop_timeout_s) and reason is None:
                reason = "server did not stop on SIGINT"
            collect_outputs(point, servers, client, rd_servers, rd_client, pdir, ingress_host, rd_ingress,
                            gc_trace=config.gc_trace)
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
def start_servers(point, config, servers, rds):
    """Instance k (global id) on servers[hosts[k]], port listen_port + local_index, files under that machine's rd."""
    servers, rds, hosts = _as_list(servers), _as_list(rds), point.hosts()
    for k in range(point.server_instances):  # in id order; each waits for readiness
        m, rd = servers[hosts[k]], rds[hosts[k]]
        words = placement_prefix("server", point, m) + gc_trace_prefix(config, m) + \
            [m.binaries["server"], "--listen_addr", f"0.0.0.0:{config.listen_port + local_index(hosts, k)}",
             "--queue_log_path", f"{rd}/server_{k}.queue.csv"] + \
            arg_words(point.server_args)
        if point.freeze is not None and point.freeze["instance"] == k:  # FreezeTarget: only this instance
            words += ["--freeze_after_ms", format_arg(point.freeze["freeze_after_ms"])]
        # Design: abstraction/benchmark_runner/experiment_executor/design.md#serverlifecycle
        scen = point.scenarios[k] if point.scenarios is not None else None
        if scen is not None:  # ScenarioAssignment: its JSON (one word) and its ScenarioLog
            words += ["--service_scenario", scen["service_scenario"],
                      "--scenario_log_path", f"{rd}/server_{k}.scenario.csv"]
        start_program(m, f"server {k}", words, f"{rd}/server_{k}.out", _server_pid_file(m, k),
                      config.run.server_ready_timeout_s)


# Design: abstraction/benchmark_runner/experiment_executor/design.md#serverlifecycle
def stop_servers(servers, server_hosts, stop_timeout_s):
    servers, ok = _as_list(servers), True
    for k, h in enumerate(server_hosts):
        ok = stop_program(servers[h], _server_pid_file(servers[h], k), stop_timeout_s) and ok
    return ok


# Design: abstraction/benchmark_runner/experiment_executor/design.md#serverlifecycle
def stop_leftover_servers(m, stop_timeout_s):
    """Stop every server_<k>.pid present on machine m, listed in one command."""
    r = m.transport.run(f"cd {quote(m.workdir)} 2>/dev/null && ls -1 server_*.pid 2>/dev/null; true", 30)
    ks = sorted(int(n[len("server_"):-len(".pid")]) for n in r.stdout.split()
                if n.startswith("server_") and n.endswith(".pid") and n[len("server_"):-len(".pid")].isdigit())
    for k in ks:
        stop_program(m, _server_pid_file(m, k), stop_timeout_s)


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
    if point.arg("loadgen", "slo_cancel") is False:  # DrainDeadline: the run may last drain_timeout_ms longer
        expected += _float(point.arg("loadgen", "drain_timeout_ms"), 10000.0) / 1000
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
def write_placement(point, servers, client, ingress_host, pdir):
    doc = {"server": placement_prefix("server", point, _as_list(servers)[0]),  # the same for every instance
           "loadgen": placement_prefix("loadgen", point, client),
           "ingress": placement_prefix("ingress", point, ingress_host)}
    with open(os.path.join(pdir, "placement.json"), "w") as f:
        json.dump(doc, f, indent=2)


# Design: abstraction/benchmark_runner/experiment_executor/design.md#gcevents
def gc_trace_prefix(config, m):
    """`env NAME=value ...` from the GcTraceHook of the instance's machine m, after any ProcessPlacement prefix."""
    if not config.gc_trace:
        return []
    return ["env"] + list(m.gc_trace_env)


# Design: abstraction/benchmark_runner/experiment_executor/design.md#gcevents
def seconds_to_ns(s):
    """Exact: a decimal string "I" or "I.F" of seconds to integer ns, truncating beyond 9 fraction digits."""
    i, _, f = s.partition(".")
    return int(i) * 1_000_000_000 + int((f + "000000000")[:9])


GC_HEADER = "t_unix_ns,cycle,stw_start_ms,mark_ms,stw_end_ms"
_START = re.compile(r"process_start_unix_ns (\d+)")


# Design: abstraction/benchmark_runner/experiment_executor/design.md#gcevents
def write_gc_events(point, k, pdir, regex):
    """server_<k>.out -> server_<k>.gc.csv on the wall clock; problems only warn."""
    out = os.path.join(pdir, f"server_{k}.out")
    if not os.path.exists(out):
        print(f"point {point.id}: server_{k}.out missing (no GcEvents)")
        return
    pattern = re.compile(regex)
    start, events = None, []
    with open(out, encoding="utf-8", errors="replace") as f:
        for line in f:
            line = line.rstrip("\n")
            m = _START.fullmatch(line) if start is None else None
            if m:
                start = int(m.group(1))  # the server's ProcessStart line
                continue
            m = pattern.match(line)
            if m:
                events.append(m)
    if start is None:
        print(f"point {point.id}: server_{k}.out has no process_start_unix_ns line (no GcEvents)")
        return
    if not events:
        print(f"point {point.id}: server_{k}.out has no GcTrace lines")
    with open(os.path.join(pdir, f"server_{k}.gc.csv"), "w") as f:
        f.write(GC_HEADER + "\n")
        for m in events:
            f.write(f"{start + seconds_to_ns(m.group('start_s'))},{m.group('cycle')},{m.group('stw_start_ms')},"
                    f"{m.group('mark_ms')},{m.group('stw_end_ms')}\n")


COUNTER_KEYS = ("cancelled_before_processing", "cancelled_during_processing", "completed_before_cancel",
                "rejected_past_deadline")  # a key an older server does not print counts as 0


# Design: abstraction/benchmark_runner/experiment_executor/design.md#point-outputs
def collect_outputs(point, servers, client, rd_servers, rd_client, pdir, ingress_host=None, rd_ingress=None,
                    gc_trace=False):
    servers, rd_servers, hosts = _as_list(servers), _as_list(rd_servers), point.hosts()
    for name in ("loadgen.requests.csv", "loadgen.summary.json", "loadgen.log"):
        client.transport.fetch(f"{rd_client}/{name}", os.path.join(pdir, name))
    ingress_host, rd_ingress = ingress_host or client, rd_ingress or rd_client
    for name in ("ingress.cancels.csv", "ingress.forwards.csv", "ingress.liveness.csv", "ingress.picks.csv",
                 "ingress.summary.json", "ingress.log"):  # LivenessLog, PickLog: absent from an older Ingress, not reported
        ingress_host.transport.fetch(f"{rd_ingress}/{name}", os.path.join(pdir, name))
    # ForwardLog: optional, a missing one only warns and never fails the point
    if not os.path.exists(os.path.join(pdir, "ingress.forwards.csv")):
        print(f"point {point.id}: ingress.forwards.csv missing (left out of the forward timeline plots)")
    counters = []
    for k in range(point.server_instances):
        m, rd = servers[hosts[k]], rd_servers[hosts[k]]  # fetched from the machine the instance ran on
        out = os.path.join(pdir, f"server_{k}.out")
        m.transport.fetch(f"{rd}/server_{k}.out", out)
        # QueueLengthLog: optional, a missing one only warns and never fails the point
        queue_csv = os.path.join(pdir, f"server_{k}.queue.csv")
        m.transport.fetch(f"{rd}/server_{k}.queue.csv", queue_csv)
        if not os.path.exists(queue_csv):
            print(f"point {point.id}: server_{k}.queue.csv missing (left out of queue_length)")
        # Design: abstraction/benchmark_runner/experiment_executor/design.md#point-outputs
        if point.scenarios is not None and point.scenarios[k] is not None:  # ScenarioLog: optional, like the queue log
            scen_csv = os.path.join(pdir, f"server_{k}.scenario.csv")
            m.transport.fetch(f"{rd}/server_{k}.scenario.csv", scen_csv)
            if not os.path.exists(scen_csv):
                print(f"point {point.id}: server_{k}.scenario.csv missing")
        if gc_trace:
            write_gc_events(point, k, pdir, m.gc_trace_regex)  # GcEvents
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
