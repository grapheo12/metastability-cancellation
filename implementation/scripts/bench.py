#!/usr/bin/env python3
# Tooling language: Python 3.11+ (shared tool implementation/scripts)
# Source: abstraction/benchmark_runner/design.md (benchmark_runner v0.5.1)
"""bench: the BenchmarkRunner.

Usage (from anywhere inside an impl/<lang> checkout):
    python3 implementation/scripts/bench.py run   <config.toml>   # setup, every SweepPoint, plots
    python3 implementation/scripts/bench.py setup <config.toml>   # setup every machine only
    python3 implementation/scripts/bench.py plot  <output_dir>    # rebuild table and plots
    python3 implementation/scripts/bench.py retry-timeout --workers K --mean_service_ms S [--eta E] RATE [RATE ...]
                                                                  # RetryTimeout per rate (M/M/k), with RetryFeedback
    python3 implementation/scripts/bench.py stable-eta --workers K --mean_service_ms S RATE [RATE ...]
                                                                  # MaxStableEta per rate

Exit status: 0 all good, 1 some SweepPoint failed, 2 config/pin/setup/usage error.
Plotting needs plotly: python3 -m pip install -r implementation/scripts/requirements.txt
"""

import argparse
import datetime
import json
import os
import shutil
import subprocess
import sys
import time

from benchrunner import config as cfgmod
from benchrunner import executor, plotter, remote_setup, retry_timeout, transport

RUNNER_DIR = os.path.dirname(os.path.abspath(__file__))


def log(msg):
    print(f"[bench {datetime.datetime.now().strftime('%H:%M:%S')}] {msg}", flush=True)


def repo_root():
    p = subprocess.run(["git", "-C", RUNNER_DIR, "rev-parse", "--show-toplevel"], capture_output=True, text=True)
    return p.stdout.strip()


# Design: abstraction/benchmark_runner/design.md#run-flow
def prepare_output_dir(config, config_path, pin, n_points):
    out = config.run.output_dir
    os.makedirs(out, exist_ok=True)
    run_json = os.path.join(out, "run.json")
    if os.path.exists(run_json):
        with open(run_json) as f:
            previous = json.load(f)
        if previous.get("commit") != pin.commit:
            raise remote_setup.PinError(f"{out} holds a run of commit {previous.get('commit')}; "
                                        f"refusing to mix it with {pin.commit}")
    shutil.copyfile(config_path, os.path.join(out, "config.toml"))
    with open(run_json, "w") as f:
        json.dump({"branch": pin.branch, "commit": pin.commit, "repo_url": pin.repo_url, "lang": pin.lang,
                   "started_utc": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
                   "points": n_points}, f, indent=2)


# Design: abstraction/benchmark_runner/design.md#run-flow
def check_placement_support(config, pin):
    if any(p["threads"] is not None for p in config.placement.values()) and pin.manifest.thread_env is None:
        raise remote_setup.PinError(f"threads is set, but implementation/{pin.lang}/benchmark_manifest.toml "
                                    "has no thread_env")
    if config.gc_trace and pin.manifest.gc_trace_env is None:  # GcTraceHook
        raise remote_setup.PinError(f"server.gc_trace is set, but implementation/{pin.lang}/benchmark_manifest.toml "
                                    "has no gc_trace_env")


# Design: abstraction/benchmark_runner/design.md#run-flow
def warn_unpinned(config, servers, client):
    on_server = config.ingress_machine == "server"  # IngressPlacement
    for role, readies, sections in (("server", servers, ["server"] + (["ingress"] if on_server else [])),
                                    ("client", [client], ["load_gen"] + ([] if on_server else ["ingress"]))):
        if (any(config.placement[s]["cpu_set"] is not None for s in sections)
                and not all(r.cpu_pinning for r in readies)):
            print(f"warning: cpu_set ignored on {role}: taskset not found", file=sys.stderr, flush=True)


def require_plotly():
    try:
        import plotly  # noqa: F401
    except ImportError:
        raise cfgmod.ConfigError("plotly is missing: python3 -m pip install -r "
                                 "implementation/scripts/requirements.txt") from None


# Design: abstraction/benchmark_runner/design.md#structure
# Design: abstraction/benchmark_runner/design.md#run-flow
def bench_run(config_path, setup_only):
    try:
        config = cfgmod.load_config(config_path, RUNNER_DIR)
        points = cfgmod.expand_sweep(config)
        if not setup_only:
            require_plotly()
        pin = remote_setup.source_pin(config.repo_url, repo_root())
        log(f"pinned {pin.branch} @ {pin.commit[:12]} ({len(points)} sweep points)")
        check_placement_support(config, pin)
        if not setup_only:
            prepare_output_dir(config, config_path, pin, len(points))
        server_ts = [transport.open_transport(m) for m in config.machines["server"]]  # ServerMachines
        client_t = transport.open_transport(config.machines["client"])
        # Design: abstraction/benchmark_runner/design.md#why-setup-happens-once
        log(f"setting up {len(server_ts) + 1} machines (clone, checkout, tool check, build)")
        servers, client = remote_setup.setup_machines(server_ts, client_t, config, pin)
    except (cfgmod.ConfigError, remote_setup.PinError, remote_setup.SetupError, transport.TransportError) as e:
        print(f"error: {e}", file=sys.stderr)
        return 2
    log("setup done")
    warn_unpinned(config, servers, client)
    if setup_only:
        return 0

    run_id = os.path.basename(config.run.output_dir.rstrip("/"))
    any_failed = False
    try:
        for i, point in enumerate(points):
            log(f"point {point.id}/{len(points) - 1} {json.dumps(point.swept)}")
            result = executor.run_point(point, config, servers, client, run_id)
            log(f"point {point.id}: {result.status}" + (f" ({result.reason})" if result.reason else ""))
            if result.status == "failed":
                any_failed = True
                if config.run.on_failure == "stop":
                    break
            if result.status != "skipped" and i < len(points) - 1:
                time.sleep(config.run.cooldown_s)
    # Design: abstraction/benchmark_runner/design.md#interrupts
    except KeyboardInterrupt:
        log("interrupted; plotting the points finished so far")
        any_failed = True
    status = bench_plot(config.run.output_dir)
    log(f"results in {config.run.output_dir}")
    return 1 if any_failed or status != 0 else 0


# Design: abstraction/benchmark_runner/design.md#run-flow
def bench_plot(output_dir):
    try:
        plotter.plot(output_dir)
    except plotter.NothingToPlot as e:
        log(f"table written, no plots: {e}")
        return 1
    except ImportError:
        print("error: plotly is missing: python3 -m pip install -r implementation/scripts/requirements.txt",
              file=sys.stderr)
        return 2
    return 0


# Design: abstraction/benchmark_runner/design.md#run-flow
# Design: abstraction/benchmark_runner/retry_timeout/design.md#failure-and-edge-cases
def bench_retry_timeout(workers, mean_service_ms, rates, eta=1.0):
    try:
        results = [retry_timeout.retry_timeout(workers, mean_service_ms, r, eta) for r in rates]
    except ValueError as e:
        print(f"error: {e}", file=sys.stderr)
        return 2
    sys.stdout.write(retry_timeout.format_results(results))
    return 0


# Design: abstraction/benchmark_runner/design.md#run-flow
# Design: abstraction/benchmark_runner/retry_timeout/design.md#failure-and-edge-cases
def bench_stable_eta(workers, mean_service_ms, rates):
    try:
        results = [retry_timeout.max_stable_eta(workers, mean_service_ms, r) for r in rates]
    except ValueError as e:
        print(f"error: {e}", file=sys.stderr)
        return 2
    sys.stdout.write(retry_timeout.format_stable_eta(results))
    return 0


def main(argv=None):
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = p.add_subparsers(dest="command", required=True)
    for name in ("run", "setup"):
        sub.add_parser(name).add_argument("config")
    sub.add_parser("plot").add_argument("output_dir")
    rt = sub.add_parser("retry-timeout", help="RetryTimeout per offered rate from an M/M/k model")
    se = sub.add_parser("stable-eta", help="largest eta whose RetryFeedback settles below rho = 1, per rate")
    for q in (rt, se):
        q.add_argument("--workers", type=int, required=True, help="workers k of the modelled queue")
        q.add_argument("--mean_service_ms", type=float, required=True, help="mean exponential service time")
        q.add_argument("rates", type=float, nargs="+", metavar="RATE", help="offered rate into that queue, req/s")
    rt.add_argument("--eta", type=float, default=1.0, help="EtaSlack: Pr(sojourn > T) = clamp(eta - rho); default 1")
    args = p.parse_args(argv)
    if args.command == "retry-timeout":
        return bench_retry_timeout(args.workers, args.mean_service_ms, args.rates, args.eta)
    if args.command == "stable-eta":
        return bench_stable_eta(args.workers, args.mean_service_ms, args.rates)
    if args.command == "plot":
        if not os.path.isdir(args.output_dir):
            print(f"error: no such directory {args.output_dir}", file=sys.stderr)
            return 2
        return bench_plot(args.output_dir)
    return bench_run(args.config, setup_only=args.command == "setup")


if __name__ == "__main__":
    sys.exit(main())
