#!/usr/bin/env python3
"""impl/envoy LoadGenerator: runs the vendored Nighthawk (build/nighthawk_client) with the Go load generator's
command line (abstraction/load_generator/api.md#program-parameters) and writes its outputs (requests.csv,
summary.json, abstraction/load_generator/metrics/api.md) from Nighthawk's per-request logs.

Mapping:
  num_clients        -> Nighthawk workers (--concurrency), one HTTP/2 connection each; worker k is client k
                        (the ping RequestSource assigns client ids in worker-creation order)
  global_rate        -> Poisson arrivals at global_rate / num_clients per worker (NH_POISSON_RATE)
  request_bytes, slo_ms -> the ping RequestSource's PingRequestBody and RequestDeadline
  n_waiting_requests -> --max-active-requests and --max-pending-requests per worker
  duration_s         -> --duration (whole seconds)
  drain_timeout_ms   -> --timeout: Nighthawk's bound on draining in-flight requests at the end (DrainDeadline)
  arrival_rng        -> native only: per-worker mt19937_64 seeded seed + k (NH_POISSON_SEED)
Rejected as not supported (exit 2): arrival_rng sampling, max_retries other than 0 (no RequestCopies), slo_cancel
true (no SLOCancellation), a duration_s that is not a whole number of seconds.
"""
import argparse
import json
import math
import os
import subprocess
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, "..", "wrappers"))
import convert  # noqa: E402

NIGHTHAWK = os.path.join(HERE, "..", "build", "nighthawk_client")


def parse_bool(s):
    if s in ("true", "false"):
        return s == "true"
    raise argparse.ArgumentTypeError("must be true or false")


def fail(message):
    """A one-line validation error and exit status 2, before any network activity."""
    print(message, file=sys.stderr)
    sys.exit(2)


def parse(argv):
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    a = p.add_argument
    a("--target", default="127.0.0.1:8080")
    a("--num_clients", required=True, type=int)
    a("--global_rate", required=True, type=float)
    a("--request_bytes", required=True, type=int)
    a("--slo_ms", required=True, type=float)
    a("--duration_s", required=True, type=float)
    a("--seed", default="0")
    a("--n_waiting_requests", default=100, type=int)
    a("--retry_timeout_ms", default="150")
    a("--max_retries", default=1, type=int)
    a("--initial_stall_ms", default=1000, type=float)
    a("--slo_cancel", default=True, type=parse_bool)
    a("--drain_timeout_ms", default=10000, type=float)
    a("--output_path", default="results")
    a("--arrival_rng", default="sampling")
    args = p.parse_args(argv)
    if args.request_bytes < 76:
        fail("request_bytes must be >= 76")
    if args.arrival_rng not in ("sampling", "native"):
        fail("arrival_rng must be sampling or native")
    if args.arrival_rng == "sampling":
        fail("arrival_rng sampling is not supported by this implementation")
    if args.max_retries != 0:
        fail(f"max_retries {args.max_retries} is not supported by this implementation")
    if args.slo_cancel:
        fail("slo_cancel true is not supported by this implementation")
    if args.duration_s != int(args.duration_s) or args.duration_s < 1:
        fail(f"duration_s {args.duration_s:g} is not supported by this implementation")
    return args


def main():
    args = parse(sys.argv[1:])
    if not os.access(NIGHTHAWK, os.X_OK):
        sys.exit(f"loadgen: {NIGHTHAWK} missing: build it with implementation/envoy/build.sh")
    out = args.output_path
    prefix = out + ".nh"
    for stale in [f for f in os.listdir(os.path.dirname(os.path.abspath(prefix)) or ".")
                  if f.startswith(os.path.basename(prefix) + ".") and f.endswith(".csv")]:
        os.remove(os.path.join(os.path.dirname(os.path.abspath(prefix)), stale))
    n = args.num_clients
    source = {"name": "nighthawk.request_source.ping", "typed_config": {
        "@type": "type.googleapis.com/nighthawk.PingRequestSourcePluginConfig",
        "client_id_base": 0, "request_bytes": args.request_bytes, "slo_ms": int(round(args.slo_ms)), "path": "/ping"}}
    cmd = [NIGHTHAWK, f"http://{args.target}/ping", "--protocol", "http2", "--concurrency", str(n), "--connections", "1",
           "--rps", str(max(1, math.ceil(args.global_rate / n))),  # unused with NH_POISSON_RATE; must be valid
           "--duration", str(int(args.duration_s)),
           "--max-active-requests", str(args.n_waiting_requests),
           "--max-pending-requests", str(args.n_waiting_requests),
           "--timeout", str(max(1, math.ceil(args.drain_timeout_ms / 1000))),
           "--request-source-plugin-config", json.dumps(source),
           # The Go load generator never stops early: drop Nighthawk's default failure predicates (they end the run
           # at the first 4xx/5xx, e.g. a DeadlineFilter drop's 503, or at the first pool failure or overflow).
           "--no-default-failure-predicates",
           "--output-format", "json", "--verbosity", "warn"]
    env = dict(os.environ, NH_REQUEST_LOG_PREFIX=prefix, NH_POISSON_RATE=repr(args.global_rate / n),
               NH_POISSON_SEED=str(args.seed))
    started = time.monotonic()
    with open(out + ".nighthawk.json", "w") as stdout, open(out + ".nighthawk.log", "w") as stderr:
        rc = subprocess.run(cmd, env=env, stdout=stdout, stderr=stderr).returncode
    run_end_s = time.monotonic() - started
    rows = convert.read_request_logs(prefix)
    records, run_start = convert.requests_records(rows, args.initial_stall_ms)
    convert.write_requests(records, out + ".requests.csv")
    config = {k: getattr(args, k) for k in vars(args)}
    summary = convert.loadgen_summary(records, config, run_start, run_end_s)
    convert.write_json(summary, out + ".summary.json")
    c = summary["counts"]
    print(f"loadgen: nighthawk exit {rc}; {len(records)} requests: ok {c['ok']}, rejected {c['rejected']}, "
          f"cancelled {c['cancelled']}, error {c['error']}", flush=True)
    sys.exit(0 if rc == 0 and records else 1)


if __name__ == "__main__":
    main()
