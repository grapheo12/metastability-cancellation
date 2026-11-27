#!/usr/bin/env python3
# Source: abstraction/ingress/design.md (ingress v0.19.1)
# Source: abstraction/ingress/load_balancer/design.md (load_balancer v0.24.0)
# Source: abstraction/ingress/frontend/design.md (frontend v0.11.0)
# Source: abstraction/ingress/retry_policy/design.md (retry_policy v0.8.0)
# Source: abstraction/ingress/upstream_pool/design.md (upstream_pool v0.1.2)
# Source: abstraction/protocol/design.md (protocol v0.8.0)
"""impl/envoy Ingress: runs the vendored Envoy (build/envoy-static) as the testbed's Ingress, with the Go Ingress's
command line (abstraction/ingress/api.md#program-parameters), and writes its outputs (forwards.csv, summary.json)
from Envoy's access log when interrupted (SIGINT/SIGTERM), as the BenchmarkRunner expects.

Realized (abstraction/ingress, Design specific to Envoy): a static cluster of --backends;
lb_policy peak_ewma (Envoy's contrib Peak EWMA: PeakEwmaRtt, PeakEwmaCost) with lb_filter none, or with
deadline_provisional_rtt (the testbed's peak_ewma_deadline policy: ProvisionalCurrentRtt <= time left over every
backend, lowest PeakEwmaCost, per-worker wait queue; RequestDeadline from the x-request-deadline header), or with
token_bucket (the same policy with its TokenBucketFilter: a per-backend token bucket, the testbed_token_bucket
upstream filter reporting each attempt's latency for the TokenRate AIMD);
forward_mode hedge only (HedgedForwarding: the first answer of any attempt wins, the others are reset);
lb_retry_policy none, timeout (TimeoutHedge: Envoy hedging on per_try_timeout) or anxious (the testbed_anxious
upstream filter and the router's 10 ms retry hook, RetryElsewhere through previous_hosts).
forwards.csv has one row per attempt, from the router's upstream log.
Every other value is rejected as not supported (exit 2): lb_policy other than peak_ewma, the deadline_mean* and
deadline_mmk strategies, forward_mode relay, heartbeat_timeout_ms > 0 (no Heartbeat Monitor) and cancel_rate > 0
(no LoadCanceller).
"""
import argparse
import json
import os
import signal
import socket
import subprocess
import sys
import time
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, "..", "wrappers"))
import convert  # noqa: E402

ENVOY = os.path.join(HERE, "..", "build", "envoy-static")
LB_POLICIES = ("round_robin", "least_outstanding", "power_of_two", "ewma", "peak_ewma")
LB_FILTERS = ("none", "deadline_mean", "deadline_mean_per_worker", "deadline_mean_queue_plus_share", "deadline_mmk",
              "deadline_provisional_rtt", "token_bucket")
RETRY_POLICIES = ("none", "timeout", "anxious")
# (flag, valid, error) of the TokenBucketFilter parameters, in the spec's validation order
# (abstraction/ingress/design.md#configuration-validation).
TOKEN_PARAMS = (
    ("lb_tb_slo_ms", lambda v: v >= 0, "lb_tb_slo_ms must be a finite number > 0"),
    ("lb_tb_init_rate", lambda v: v > 0, "lb_tb_init_rate must be a finite number > 0"),
    ("lb_tb_max_tokens", lambda v: v >= 1, "lb_tb_max_tokens must be a finite number >= 1"),
    ("lb_tb_mult_dec", lambda v: v > 1, "lb_tb_mult_dec must be a finite number > 1"),
    ("lb_tb_add_inc_limit", lambda v: v >= 1, "lb_tb_add_inc_limit must be a finite number >= 1"),
    ("lb_tb_add_inc_frac", lambda v: v >= 0, "lb_tb_add_inc_frac must be a finite number >= 0"),
    ("lb_tb_min_rate_frac", lambda v: 0 < v <= 1, "lb_tb_min_rate_frac must be a number in (0, 1]"),
)
UNLIMITED = 1_000_000  # circuit-breaker limits: the Go Ingress has none


def fail(message):
    """A one-line validation error and exit status 2, before any network activity."""
    print(message, file=sys.stderr)
    sys.exit(2)


# Design: abstraction/ingress/design.md#configuration-validation
def validate(args):
    """The Ingress's configuration validation, in the spec's order, then what this implementation does not realize
    rejected as not supported."""
    if args.lb_policy not in LB_POLICIES:
        fail("lb_policy must be " + ", ".join(LB_POLICIES[:-1]) + " or " + LB_POLICIES[-1])
    if args.lb_filter not in LB_FILTERS:
        fail("lb_filter must be " + ", ".join(LB_FILTERS[:-1]) + " or " + LB_FILTERS[-1])
    if args.lb_filter in ("deadline_provisional_rtt", "token_bucket") and args.lb_policy != "peak_ewma":
        fail(f"lb_filter {args.lb_filter} needs lb_policy peak_ewma")
    if args.lb_policy == "peak_ewma" and args.lb_filter not in ("none", "deadline_provisional_rtt", "token_bucket"):
        fail("lb_policy peak_ewma needs lb_filter none, deadline_provisional_rtt or token_bucket")
    if args.lb_policy != "peak_ewma":
        fail(f"lb_policy {args.lb_policy} is not supported by this implementation")
    try:
        margin = float(args.lb_filter_margin)
    except ValueError:
        margin = float("nan")
    if not (margin >= 1) or margin == float("inf"):
        fail("lb_filter_margin must be a finite number >= 1")
    args.lb_filter_margin = margin
    try:
        tau = float(args.lb_probe_tau_ms)
    except ValueError:
        tau = float("nan")
    if not (tau >= 0) or tau == float("inf"):
        fail("lb_probe_tau_ms must be a finite number >= 0")
    args.lb_probe_tau_ms = tau
    # TokenBucketFilter parameters: validated always, used only by token_bucket.
    for name, ok, message in TOKEN_PARAMS:
        try:
            v = float(getattr(args, name))
        except ValueError:
            v = float("nan")
        if v != v or v in (float("inf"), float("-inf")) or not ok(v):
            fail(message)
        setattr(args, name, v)
    if args.lb_filter == "token_bucket" and not args.lb_tb_slo_ms > 0:
        fail("lb_tb_slo_ms is required with lb_filter token_bucket")
    if args.lb_retry_policy not in RETRY_POLICIES:
        fail("lb_retry_policy must be one of none, timeout, anxious")
    if args.forward_mode not in ("relay", "hedge"):
        fail("forward_mode must be relay or hedge")
    if args.forward_mode == "relay":
        fail("forward_mode relay is not supported by this implementation")
    if args.heartbeat_timeout_ms > 0:
        fail(f"heartbeat_timeout_ms {args.heartbeat_timeout_ms} is not supported by this implementation")
    if args.cancel_rate > 0:
        fail(f"cancel_rate {args.cancel_rate:g} is not supported by this implementation")


def parse(argv):
    p = argparse.ArgumentParser(description=__doc__)
    a = p.add_argument
    a("--listen_addr", default="0.0.0.0:9090")
    a("--backends", required=True)
    a("--num_clients", required=True, type=int)
    a("--lb_policy", default="least_outstanding")
    a("--lb_filter", default="none")
    a("--ewma_beta", default="0.7")
    a("--cancel_rate", default="0", type=float)
    a("--duration_s", default="0")
    a("--seed", default="0")
    a("--output_path", default="ingress")
    a("--initial_stall_ms", default="1000")
    a("--heartbeat_timeout_ms", default="0", type=int)
    a("--lb_retry_policy", default="none")
    a("--lb_max_retries", default="0", type=int)
    a("--lb_retry_timeout_ms", default="150")
    a("--lb_retry_anxiety_ratio", default="0.3")
    a("--lb_retry_late_factor", default="10")
    a("--forward_mode", default="relay")
    a("--lb_filter_margin", default="1")
    a("--lb_probe_tau_ms", default="0")
    a("--lb_tb_slo_ms", default="0")
    a("--lb_tb_init_rate", default="50")
    a("--lb_tb_max_tokens", default="10")
    a("--lb_tb_mult_dec", default="2")
    a("--lb_tb_add_inc_limit", default="2")
    a("--lb_tb_add_inc_frac", default="0.1")
    a("--lb_tb_min_rate_frac", default="0.1")
    args = p.parse_args(argv)
    validate(args)
    return args


def hedging(args):
    """True when the flags ask for timeout LbRetries (Envoy hedging on per-try timeout)."""
    return args.lb_retry_policy == "timeout" and args.lb_max_retries > 0


def anxious(args):
    """True when the flags ask for anxious LbRetries (the testbed_anxious upstream filter + router retry hook)."""
    return args.lb_retry_policy == "anxious" and args.lb_max_retries > 0


# Design: abstraction/ingress/load_balancer/design.md#lbpolicy
def cluster_policy(args):
    """The Envoy load-balancing policy for the validated flags (lb_policy is peak_ewma)."""
    if args.lb_filter in ("deadline_provisional_rtt", "token_bucket"):
        return "PEAK_EWMA_DEADLINE"
    return "PEAK_EWMA"


def upstream_filters(args, backends):
    """The cluster's upstream HTTP filters (per upstream attempt), or [] for none: the TokenBucketFilter's latency
    reports, the AnxiousTracker's observations, then the codec."""
    filters = []
    if args.lb_filter == "token_bucket":
        # Design: abstraction/ingress/load_balancer/design.md#tokenbucketfilter
        filters.append({"name": "envoy.filters.http.testbed_token_bucket", "typed_config": {
            "@type": "type.googleapis.com/envoy.extensions.filters.http.testbed_token_bucket.v3alpha."
                     "TestbedTokenBucket"}})
    if anxious(args):
        filters.append({"name": "envoy.filters.http.testbed_anxious", "typed_config": {
            "@type": "type.googleapis.com/envoy.extensions.filters.http.testbed_anxious.v3alpha.TestbedAnxious",
            "alpha": float(args.lb_retry_anxiety_ratio), "beta": float(args.lb_retry_late_factor),
            "ewma_beta": float(args.ewma_beta), "backends": backends}})
    if not filters:
        return []
    return filters + [{"name": "envoy.filters.http.upstream_codec", "typed_config": {
        "@type": "type.googleapis.com/envoy.extensions.filters.http.upstream_codec.v3.UpstreamCodec"}}]


def token_bucket_config(args):
    """The peak_ewma_deadline token_bucket block (TokenBucketFilter), or {} without lb_filter token_bucket."""
    if args.lb_filter != "token_bucket":
        return {}
    # Design: abstraction/ingress/load_balancer/design.md#tokenbucketfilter
    return {"token_bucket": {"slo": f"{args.lb_tb_slo_ms / 1000:.6f}s", "init_rate": args.lb_tb_init_rate,
                             "max_tokens": args.lb_tb_max_tokens, "mult_dec": args.lb_tb_mult_dec,
                             "add_inc_limit": args.lb_tb_add_inc_limit, "add_inc_frac": args.lb_tb_add_inc_frac,
                             "min_rate_frac": args.lb_tb_min_rate_frac}}


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


# Design: abstraction/ingress/design.md#structure
def envoy_config(args, backends, admin_port, access_log, upstream_log=None):
    host, port = args.listen_addr.rsplit(":", 1)
    policy = cluster_policy(args)
    cluster = {
        "name": "servers", "type": "STATIC",
        "connect_timeout": "5s",
        # One upstream connection per client connection and backend, so a server sees one client per connection
        # (its client binding, abstraction/server/ping_handler/design.md#client-binding).
        # Design: abstraction/ingress/design.md#why-one-upstream-connection-per-client-and-backend
        # Design: abstraction/ingress/upstream_pool/design.md#upstreampool
        # Design: abstraction/ingress/upstream_pool/design.md#get
        "connection_pool_per_downstream_connection": True,
        "typed_extension_protocol_options": {"envoy.extensions.upstreams.http.v3.HttpProtocolOptions": {
            "@type": "type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions",
            "explicit_http_config": {"http2_protocol_options": {}},
            **({"http_filters": upstream_filters(args, backends)} if upstream_filters(args, backends) else {})}},
        "circuit_breakers": {"thresholds": [{"max_connections": UNLIMITED, "max_pending_requests": UNLIMITED,
                                             "max_requests": UNLIMITED, "max_retries": UNLIMITED}]},
        "load_assignment": {"cluster_name": "servers", "endpoints": [{"lb_endpoints": [
            {"endpoint": {"address": {"socket_address": {"address": h, "port_value": int(pt)}}}}
            for h, pt in (b.rsplit(":", 1) for b in backends)]}]},
    }
    if policy == "PEAK_EWMA":  # contrib extension, built into envoy-static by build.sh
        cluster["load_balancing_policy"] = {"policies": [{"typed_extension_config": {
            "name": "envoy.load_balancing_policies.peak_ewma",
            "typed_config": {"@type": "type.googleapis.com/envoy.extensions.load_balancing_policies.peak_ewma.v3alpha."
                                      "PeakEwma"}}}]}
    elif policy == "PEAK_EWMA_DEADLINE":  # the testbed's contrib extension, same binary (deadline_lb.h)
        cluster["load_balancing_policy"] = {"policies": [{"typed_extension_config": {
            "name": "envoy.load_balancing_policies.peak_ewma_deadline",
            "typed_config": {"@type": "type.googleapis.com/envoy.extensions.load_balancing_policies."
                                      "peak_ewma_deadline.v3alpha.PeakEwmaDeadline",
                             "filter_margin": args.lb_filter_margin,  # FilterMargin gamma
                             # Design: abstraction/ingress/load_balancer/design.md#filterprobe
                             **({"probe_tau": f"{args.lb_probe_tau_ms / 1000:.6f}s"} if args.lb_probe_tau_ms > 0 else {}),
                             **token_bucket_config(args)}}}]}
    router = {"name": "envoy.filters.http.router", "typed_config": {
        "@type": "type.googleapis.com/envoy.extensions.filters.http.router.v3.Router"}}
    if upstream_log is not None:  # one line per upstream attempt (forwards.csv)
        # Design: abstraction/ingress/load_balancer/design.md#forwardlog
        # Design: abstraction/ingress/frontend/design.md#forwardattempt
        router["typed_config"]["upstream_log"] = [{"name": "envoy.access_loggers.file", "typed_config": {
            "@type": "type.googleapis.com/envoy.extensions.access_loggers.file.v3.FileAccessLog",
            "path": upstream_log, "log_format": {"text_format_source": {
                "inline_string": convert.UPSTREAM_LOG_FORMAT}}}}]
    # Design: abstraction/ingress/frontend/design.md#forwardtask
    route = {"cluster": "servers", "timeout": "0s"}
    # Design: abstraction/ingress/frontend/design.md#hedgedforwarding
    # Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
    if hedging(args):
        # The timeout RetryPolicy as Envoy hedging: after lb_retry_timeout_ms without an answer, one more attempt
        # through the load balancer, the slow one kept; never on errors (retriable-status-codes with no codes
        # matches no response and no reset); back-off at most 1 ms (Go consults the policy every 10 ms).
        route["retry_policy"] = {
            "retry_on": "retriable-status-codes", "retriable_status_codes": [],
            "num_retries": args.lb_max_retries,
            "per_try_timeout": f"{float(args.lb_retry_timeout_ms) / 1000:.6f}s",
            "retry_back_off": {"base_interval": "0.001s", "max_interval": "0.001s"}}
        route["hedge_policy"] = {"hedge_on_per_try_timeout": True}
    # Design: abstraction/ingress/frontend/design.md#hedgedforwarding
    # Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
    if anxious(args):
        # AnxiousRetry: the router asks the testbed_anxious hook every 10 ms (no per-try timer); a retry is a hedge
        # that avoids the backends this request already tried (RetryElsewhere); never on errors, as Go.
        route["retry_policy"] = {
            "retry_on": "retriable-status-codes", "retriable_status_codes": [],
            "num_retries": args.lb_max_retries,
            "retry_back_off": {"base_interval": "0.001s", "max_interval": "0.001s"},
            "retry_host_predicate": [{"name": "envoy.retry_host_predicates.previous_hosts", "typed_config": {
                "@type": "type.googleapis.com/envoy.extensions.retry.host.previous_hosts.v3.PreviousHostsPredicate"}}],
            "host_selection_retry_max_attempts": 5}
        route["hedge_policy"] = {"hedge_on_per_try_timeout": True}
    # Peak EWMA learns RTTs only through its HTTP filter, which must run before the router.
    # Design: abstraction/ingress/load_balancer/design.md#peakewmartt
    http_filters = ([{"name": "envoy.filters.http.peak_ewma", "typed_config": {
        "@type": "type.googleapis.com/envoy.extensions.filters.http.peak_ewma.v3alpha.PeakEwmaConfig"}}]
                    if policy in ("PEAK_EWMA", "PEAK_EWMA_DEADLINE") else []) + [router]
    hcm = {
        "@type": "type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3."
                 "HttpConnectionManager",
        "stat_prefix": "ingress", "codec_type": "AUTO",
        "stream_idle_timeout": "0s", "request_timeout": "0s",
        "http2_protocol_options": {"max_concurrent_streams": 100000},
        "route_config": {"name": "ping", "virtual_hosts": [{"name": "all", "domains": ["*"], "routes": [
            # WorkerCount (x-worker-threads) is read by an Ingress, never relayed to the client: removed on the
            # route entry (envoy.config.route.v3.Route, not its RouteAction).
            # Design: abstraction/protocol/design.md#workercount
            {"match": {"prefix": "/"}, "route": route, "response_headers_to_remove": ["x-worker-threads"]}]}]},
        # Design: abstraction/ingress/frontend/design.md#client-connections
        "http_filters": http_filters,
        "access_log": [{"name": "envoy.access_loggers.file", "typed_config": {
            "@type": "type.googleapis.com/envoy.extensions.access_loggers.file.v3.FileAccessLog",
            "path": access_log, "log_format": {"text_format_source": {
                "inline_string": convert.ACCESS_LOG_FORMAT}}}}],
    }
    return {
        "admin": {"address": {"socket_address": {"address": "127.0.0.1", "port_value": admin_port}}},
        "static_resources": {
            "listeners": [{"name": "ingress",
                           "address": {"socket_address": {"address": host, "port_value": int(port)}},
                           "filter_chains": [{"filters": [{"name": "envoy.filters.network.http_connection_manager",
                                                           "typed_config": hcm}]}]}],
            "clusters": [cluster]},
    }


def main():
    args = parse(sys.argv[1:])
    if not os.access(ENVOY, os.X_OK):
        sys.exit(f"ingress: {ENVOY} missing: build it with implementation/envoy/build.sh")
    backends = [b for b in args.backends.split(",") if b]
    out = args.output_path
    access_log = out + ".access.raw"
    upstream_log = out + ".upstream.raw"
    admin_port = free_port()
    cfg_path = out + ".envoy.json"
    convert.write_json(envoy_config(args, backends, admin_port, access_log, upstream_log), cfg_path)
    # Design: abstraction/ingress/design.md#concurrency-model
    concurrency = os.environ.get("GOMAXPROCS") or str(os.cpu_count() or 1)
    env = dict(os.environ)
    trace_prefix = out + ".peak_ewma"
    if cluster_policy(args) in ("PEAK_EWMA", "PEAK_EWMA_DEADLINE") or anxious(args):  # trace (convert.merge_peak_ewma)
        env["PEAK_EWMA_TRACE_PREFIX"] = trace_prefix
    envoy = subprocess.Popen([ENVOY, "--config-path", cfg_path, "--concurrency", concurrency,
                              "--use-dynamic-base-id", "-l", "warning", "--log-path", out + ".envoy.log"], env=env)
    stop = {"now": False}
    # Design: abstraction/ingress/design.md#shutdown
    for sig in (signal.SIGINT, signal.SIGTERM):
        signal.signal(sig, lambda *_: stop.update(now=True))
    deadline = time.monotonic() + 60
    while True:  # ready: admin says LIVE and the listener accepts
        if envoy.poll() is not None:
            sys.exit(f"ingress: envoy exited during startup ({envoy.returncode}); see {out}.envoy.log")
        try:
            if urllib.request.urlopen(f"http://127.0.0.1:{admin_port}/ready", timeout=1).status == 200:
                break
        except OSError:
            pass
        if time.monotonic() > deadline:
            envoy.kill()
            sys.exit("ingress: envoy not ready after 60 s")
        time.sleep(0.1)
    print(f"listening on {args.listen_addr}", flush=True)  # the BenchmarkRunner's READY_LINE
    started = time.monotonic()
    while not stop["now"] and envoy.poll() is None:
        time.sleep(0.2)
    try:  # graceful stop: Envoy flushes its access log on exit
        urllib.request.urlopen(urllib.request.Request(f"http://127.0.0.1:{admin_port}/quitquitquit", method="POST"),
                               timeout=5)
    except OSError:
        envoy.terminate()
    try:
        envoy.wait(timeout=30)
    except subprocess.TimeoutExpired:
        envoy.kill()
        envoy.wait()
    rows = convert.parse_access_log(access_log) if os.path.exists(access_log) else []
    attempts = convert.parse_upstream_log(upstream_log) if os.path.exists(upstream_log) else rows
    run_start = min((r[0] for r in rows), default=0)  # the first request's arrival (as before)
    convert.write_forwards(attempts, backends, out + ".forwards.csv", float(args.ewma_beta), run_start)
    trace = convert.merge_peak_ewma(trace_prefix, backends, run_start, out)
    if any(trace.values()):
        print("ingress: Peak EWMA trace: " + ", ".join(f"{len(v)} {k}" for k, v in trace.items() if v), flush=True)
    counters = convert.filter_counts(trace["queue"]) if cluster_policy(args) == "PEAK_EWMA_DEADLINE" else {}
    counters["lb_retries"] = convert.lb_retries(attempts)
    counters.update(convert.retry_counts(trace["queue"]))
    exec_values = convert.exec_ewmas(trace["exec"], len(backends))  # AnxiousTracker's EWMAs, last values
    config = {k: getattr(args, k) for k in vars(args)}
    config["backends"] = backends
    summary = convert.ingress_summary(attempts, backends, config, run_start, time.monotonic() - started, counters)
    for b, (ewma_exec, ewma_anti) in enumerate(exec_values):
        summary["backends"][b]["ewma_exec_ns"] = ewma_exec
        summary["backends"][b]["ewma_anti_exec_ns"] = ewma_anti
    convert.write_json(summary, out + ".summary.json")
    for name in ("cancels", "liveness", "picks"):  # outputs of components not implemented here: header only
        header = {"cancels": "arrival,cancel_id,client_id,server_id,target_seq,outcome,http_status,scheduled_ns,send_ns,end_ns",
                  "liveness": "t_ns,server_id,live",
                  "picks": "t_ns,server_id,outstanding,ewma_exec_ns,worker_count"}[name]
        with open(f"{out}.{name}.csv", "w") as f:
            f.write(header + "\n")
    print(f"ingress: {len(rows)} requests, {len(attempts)} upstream attempts, {counters['lb_retries']} LB retries; "
          "outputs written", flush=True)


if __name__ == "__main__":
    main()
