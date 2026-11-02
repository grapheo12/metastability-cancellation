#!/usr/bin/env python3
# Tooling language: Python 3.11+ (shared tool implementation/scripts)
# Source: abstraction/benchmark_runner/design.md (benchmark_runner v0.1.5)
"""bench: the BenchmarkRunner.

Usage (from anywhere inside an impl/<lang> checkout):
    python3 implementation/scripts/bench.py run   <config.toml>   # setup, every SweepPoint, plots
    python3 implementation/scripts/bench.py setup <config.toml>   # setup both machines only
    python3 implementation/scripts/bench.py plot  <output_dir>    # rebuild table and plots

Exit status: 0 all good, 1 some SweepPoint failed, 2 config/pin/setup/usage error.
Plotting needs matplotlib: python3 -m pip install -r implementation/scripts/requirements.txt
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
from benchrunner import executor, plotter, remote_setup, transport

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


# Design: abstraction/benchmark_runner/design.md#run-flow
def warn_unpinned(config, server, client):
    for role, ready, sections in (("server", server, ["server"]), ("client", client, ["load_gen", "canceller"])):
        if any(config.placement[s]["cpu_set"] is not None for s in sections) and not ready.cpu_pinning:
            print(f"warning: cpu_set ignored on {role}: taskset not found", file=sys.stderr, flush=True)


def require_matplotlib():
    try:
        import matplotlib  # noqa: F401
    except ImportError:
        raise cfgmod.ConfigError("matplotlib is missing: python3 -m pip install -r "
                                 "implementation/scripts/requirements.txt") from None


# Design: abstraction/benchmark_runner/design.md#structure
# Design: abstraction/benchmark_runner/design.md#run-flow
def bench_run(config_path, setup_only):
    try:
        config = cfgmod.load_config(config_path, RUNNER_DIR)
        points = cfgmod.expand_sweep(config)
        if not setup_only:
            require_matplotlib()
        pin = remote_setup.source_pin(config.repo_url, repo_root())
        log(f"pinned {pin.branch} @ {pin.commit[:12]} ({len(points)} sweep points)")
        check_placement_support(config, pin)
        if not setup_only:
            prepare_output_dir(config, config_path, pin, len(points))
        server_t = transport.open_transport(config.machines["server"])
        client_t = transport.open_transport(config.machines["client"])
        # Design: abstraction/benchmark_runner/design.md#why-setup-happens-once
        log("setting up both machines (clone, checkout, tool check, build)")
        server, client = remote_setup.setup_machines(server_t, client_t, config, pin)
    except (cfgmod.ConfigError, remote_setup.PinError, remote_setup.SetupError, transport.TransportError) as e:
        print(f"error: {e}", file=sys.stderr)
        return 2
    log("setup done")
    warn_unpinned(config, server, client)
    if setup_only:
        return 0

    run_id = os.path.basename(config.run.output_dir.rstrip("/"))
    any_failed = False
    try:
        for i, point in enumerate(points):
            log(f"point {point.id}/{len(points) - 1} {json.dumps(point.swept)}")
            result = executor.run_point(point, config, server, client, run_id)
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
        print("error: matplotlib is missing: python3 -m pip install -r implementation/scripts/requirements.txt",
              file=sys.stderr)
        return 2
    return 0


def main(argv=None):
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = p.add_subparsers(dest="command", required=True)
    for name in ("run", "setup"):
        sub.add_parser(name).add_argument("config")
    sub.add_parser("plot").add_argument("output_dir")
    args = p.parse_args(argv)
    if args.command == "plot":
        if not os.path.isdir(args.output_dir):
            print(f"error: no such directory {args.output_dir}", file=sys.stderr)
            return 2
        return bench_plot(args.output_dir)
    return bench_run(args.config, setup_only=args.command == "setup")


if __name__ == "__main__":
    sys.exit(main())
