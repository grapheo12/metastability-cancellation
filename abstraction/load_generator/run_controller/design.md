# Run Controller — Design

## Configuration validation

```
function validate(params):
    1 <= num_clients <= 2^32 - 1             else error
    global_rate > 0                          else error
    protocol.validate_request_body_size(request_bytes)   # >= 76 (PingRequestBody)
    slo_ms > 0                               else error
    slo_cancel is exactly "true" or "false"  else error             # default true
    drain_timeout_ms is an integer > 0       else error             # default 10000; checked even when unused
    duration_s > 0                           else error
    seed parses as u64                       else error
    arrival_rng in {sampling, native}        else error             # default sampling
    arrival_rng is realized here             else error "arrival_rng <value> is not supported by this implementation"   # OptionalValue
    rate_steps = parse_rate_schedule(rate_schedule, num_clients)   # RateSchedule; none if empty (default)
    target parses as host:port               else error
    1 <= n_waiting_requests <= MAX_WAITING_REQUESTS   else error     # MAX_WAITING_REQUESTS = 1000
    retry_timeout_ms > 0                     else error             # default 150
    max_retries is an integer >= 0           else error             # default 1
    n_waiting_requests * (1 + max_retries) <= MAX_WAITING_REQUESTS  else error   # exact integer arithmetic
    initial_stall_ms is an integer >= 0      else error             # default 1000
    initial_stall_ms < duration_s * 1000     else error             # measured_s > 0
    return LoadGenConfig(params,
                         per_client_rate = global_rate / num_clients,
                         rate_steps = rate_steps,
                         slo_ns = round(slo_ms * 1e6),
                         retry_timeout_ns = round(retry_timeout_ms * 1e6),
                         initial_stall_ns = initial_stall_ms * 1_000_000,
                         duration_ns = round(duration_s * 1e9),
                         drain_timeout_ns = drain_timeout_ms * 1_000_000)
```

```
function parse_rate_schedule(s, n):              # -> none | list of (start_ns, λ)
    if s is empty: return none
    E = "rate_schedule must be start_s:rate pairs separated by commas, the first starting at 0, starts increasing, rates > 0"
    steps = []
    for item in s split on ",":
        (a, b) = item split on ":" into exactly two parts      else error E
        start_s = a as a finite float >= 0; rate = b as a finite float > 0   else error E
        steps.append((round(start_s * 1e9), rate / n))
    steps[0].start_ns == 0                                     else error E
    every steps[k + 1].start_ns > steps[k].start_ns           else error E
    return steps
```

Examples: `"0:200,30:120"` gives `[(0, 200/N), (30_000_000_000, 120/N)]`; `"0:200"` is one step (a constant rate,
drawn by the time change); `"5:200"`, `"0:200,30:0"`, `"0:200,30:120,30:100"`, `"0:200;30:120"` and `"0:200,x:1"`
are errors.

The last retry check keeps every client's streams of unresolved requests within the `SETTINGS_MAX_CONCURRENT_STREAMS`
that servers and the Ingress advertise ([../client/design.md#retrytimer](../client/design.md#retrytimer)). Examples:
`n_waiting_requests = 100`, `max_retries = 1` gives 200 (valid); `500` and `1` gives 1000 (valid, the maximum);
`1000` and `1` gives 2000 (error); `1` and `999` gives 1000 (valid).

`initial_stall_ms` must leave part of the active phase to measure: with `duration_s = 60`, `59999` is valid and
`60000` is an error; with `duration_s = 0.5`, the default `1000` is an error, so a short run must set it lower (or to
`0`).

## Startup

```
function main(params):
    config = validate(params)                 # exit non-zero on error
    conns = []
    for i in 0 .. N-1:
        c = open_h2c_connection(config.target) # prior knowledge, SETTINGS exchanged
        if c failed: close all in conns; exit non-zero
        conns.append(c)
    run_start = monotonic_now_ns()
    run_start_unix_ns = wall_clock_unix_ns()  # reported in the summary and the origin of every RequestDeadline;
                                              # never used for timing
    tasks = []
    for i in 0 .. N-1:
        rng_i = sampling.rng_new(sampling.derive_seed(config.seed, STREAM_CLIENT_ARRIVALS, i))   # arrival_rng = sampling
                if config.arrival_rng == sampling
                else native_rng(config.seed + i)        # arrival_rng = native: the implementation's generator, i = client index
        tasks.append(spawn run_client(i, conns[i], config, rng_i, run_start, run_start_unix_ns))
    all_records = []
    for i in 0 .. N-1:
        all_records.extend(join(tasks[i]))     # see DrainPhase
    run_end = monotonic_now_ns() - run_start
    shutdown(conns)
    report = metrics.aggregate(all_records, config, run_end, run_start_unix_ns)
    metrics.write_requests_csv(config.output_path + ".requests.csv", all_records)
    metrics.write_summary_json(config.output_path + ".summary.json", report)
    exit 0
```

Connections are opened one after another, in client-id order, so the server's accept order (and
so its per-connection seeds) matches the client ids.

## DrainPhase

Each client's sender stops sending once `now() >= duration_ns`. Its receiver then waits for the up to
`n_waiting_requests` requests still outstanding. How long that takes depends on `slo_cancel`:

| `slo_cancel` | What ends an outstanding request | Clients have all returned by about |
|---|---|---|
| `true` (default) | its response, an error, or its own SLO timer within `slo_ns` of being sent ([../client/design.md#slocancellation](../client/design.md#slocancellation)) | `duration_ns + slo_ns` |
| `false` | its response, an error, or the client's **DrainDeadline** at `duration_ns + drain_timeout_ns`, which cancels every request still outstanding (defined with SLOCancellation in [../client/design.md#slocancellation](../client/design.md#slocancellation)) | `duration_ns + drain_timeout_ns` |

Either way the end is bounded, whatever `n_waiting_requests` is (plus scheduling jitter). With `slo_cancel = false`
a request stuck on a server that never answers (a WorkerFreeze, for example) holds its client's slot until the
DrainDeadline.

`MAX_WAITING_REQUESTS = 1000` is the stream count every peer must accept on one connection
([../../api.md#wire-contract-ping](../../api.md#wire-contract-ping)), so a client never opens more streams than the
Ingress or the server allows. Requests that were scheduled but not
sent are recorded as `not_sent` by the client.

## Shutdown ordering

1. All clients have returned (no stream is in use any more).
2. Send `GOAWAY` and close each connection. Errors here are ignored.
3. Aggregate and write the outputs. A write failure exits non-zero.

## Design specific to Envoy

Implementation: `implementation/envoy`: vendored Envoy (`envoy/`) and Nighthawk (`nighthawk/`) with the testbed's
changes, recorded in [VENDORED.md](../../../implementation/envoy/VENDORED.md) ([AGENTS.md](../../../AGENTS.md#vendored-code)),
built in Docker with Bazel by `implementation/envoy/build.sh` (C++ tests: `bazel test` on the targets VENDORED.md
names); Python 3 wrappers `bin/ingress.py`, `bin/loadgen.py` and `wrappers/convert.py` (tests: `cd
implementation/envoy/wrappers && python3 -B -m unittest test_convert`). Only code written or changed for the testbed
carries `Design:` tags; unchanged upstream code does not.

### Envoy choices

- Configuration validation: `bin/loadgen.py` `parse`, then the values Envoy's load generator does not realize are
  rejected with `<parameter> <value> is not supported by this implementation` (exit 2).
- Startup, DrainPhase and Shutdown ordering: the wrapper runs Nighthawk to completion (`--duration`, then
  `--timeout`), then reads the per-request logs and writes the outputs; it exits non-zero if Nighthawk failed or no
  request was recorded.

### Traceability

Generated from the `Design:` tags in the testbed's code under `implementation/envoy` by `implementation/spec_tools/tracegen.py` (see `AGENTS.md`). Each link points at the first code line after the tag.
Regenerate from the repository root with `python3 implementation/spec_tools/tracegen.py --lang Envoy --impl implementation/envoy --ext .cc --ext .h --comment // --ext .py:# --write`.

<!-- trace:begin -->
| Design subsection | Implementation (file:line) |
|---|---|
| [Configuration validation](#configuration-validation) | [loadgen.py:49](../../../implementation/envoy/bin/loadgen.py#L49), [loadgen.py:77](../../../implementation/envoy/bin/loadgen.py#L77), [test_loadgen.py:30](../../../implementation/envoy/wrappers/test_loadgen.py#L30) |
| [Startup](#startup) | [loadgen.py:115](../../../implementation/envoy/bin/loadgen.py#L115) |
| [DrainPhase](#drainphase) | [loadgen.py:139](../../../implementation/envoy/bin/loadgen.py#L139) |
| [Shutdown ordering](#shutdown-ordering) | [loadgen.py:156](../../../implementation/envoy/bin/loadgen.py#L156) |
<!-- trace:end -->
