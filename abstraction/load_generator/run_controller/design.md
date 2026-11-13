# Run Controller — Design

## Configuration validation

```
function validate(params):
    1 <= num_clients <= 2^32 - 1             else error
    global_rate > 0                          else error
    protocol.validate_body_size(request_bytes)
    slo_ms > 0                               else error
    duration_s > 0                           else error
    seed parses as u64                       else error
    target parses as host:port               else error
    1 <= n_waiting_requests <= MAX_WAITING_REQUESTS   else error     # MAX_WAITING_REQUESTS = 1000
    retry_timeout_ms > 0                     else error             # default 150
    max_retries is an integer >= 0           else error             # default 1
    n_waiting_requests * (1 + max_retries) <= MAX_WAITING_REQUESTS  else error   # exact integer arithmetic
    return LoadGenConfig(params,
                         per_client_rate = global_rate / num_clients,
                         slo_ns = round(slo_ms * 1e6),
                         retry_timeout_ns = round(retry_timeout_ms * 1e6),
                         duration_ns = round(duration_s * 1e9))
```

The last retry check keeps every client's streams of unresolved requests within the `SETTINGS_MAX_CONCURRENT_STREAMS`
that servers and the Ingress advertise ([../client/design.md#retrytimer](../client/design.md#retrytimer)). Examples:
`n_waiting_requests = 100`, `max_retries = 1` gives 200 (valid); `500` and `1` gives 1000 (valid, the maximum);
`1000` and `1` gives 2000 (error); `1` and `999` gives 1000 (valid).

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
    run_start_unix_ns = wall_clock_unix_ns()  # reported only, in the summary; never used for timing
    tasks = []
    for i in 0 .. N-1:
        rng_i = sampling.rng_new(sampling.derive_seed(config.seed, STREAM_CLIENT_ARRIVALS, i))
        tasks.append(spawn run_client(i, conns[i], config, rng_i, run_start))
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

No extra mechanism is needed. Each client's sender stops sending once `now() >= duration_ns`. Its receiver then
waits for the up to `n_waiting_requests` requests still outstanding, and each one is resolved by its response or by
its own SLO timer within `slo_ns` of being sent. Joining every client therefore ends by about
`duration_ns + slo_ns` (plus scheduling jitter), whatever `n_waiting_requests` is.

`MAX_WAITING_REQUESTS = 1000` is the stream count every peer must accept on one connection
([../../api.md#wire-contract-ping](../../api.md#wire-contract-ping)), so a client never opens more streams than the
Ingress or the server allows. Requests that were scheduled but not
sent are recorded as `not_sent` by the client.

## Shutdown ordering

1. All clients have returned (no stream is in use any more).
2. Send `GOAWAY` and close each connection. Errors here are ignored.
3. Aggregate and write the outputs. A write failure exits non-zero.

## Design specific to Go

Implementation: `implementation/go` (module `testbed`, Go 1.21, `golang.org/x/net v0.17.0`).
Build and test with `CGO_ENABLED=0 go build ./...` / `CGO_ENABLED=0 go test ./...`. On recent macOS the dynamic
loader rejects cgo-linked Go 1.21 binaries (missing `LC_UUID`), so cgo is disabled.

### Go choices

- File `internal/loadgen/runcontroller.go`: `Validate` and `Run` (the `main` of the design, minus flag parsing). `MAX_WAITING_REQUESTS` is `MaxWaitingRequests` in `config.go`. `TestValidateNWaitingRequests` covers the range and the default.
- `open_h2c_connection` is `h2client.Dial`. It does TCP dial, `Transport{AllowHTTP: true}.NewClientConn`, then a `Ping` round trip, so the server has accepted the connection and SETTINGS are exchanged before the next client dials. This keeps the server's accept order equal to client-id order.
- `join` is a `sync.WaitGroup`. `GOAWAY + close` is `ClientConn.Shutdown` with a 10 ms bound, then `Close`.

### Traceability

Generated from the `// Design:` tags in the code by `implementation/spec_tools/tracegen.py` (see `AGENTS.md`). Each link points at the first code line after the tag.
Regenerate from the repository root with `python3 implementation/spec_tools/tracegen.py --lang Go --ext .go --comment // --write`.

<!-- trace:begin -->
| Design subsection | Implementation (file:line) |
|---|---|
| [Configuration validation](#configuration-validation) | [client_test.go:91](../../../implementation/go/internal/loadgen/client_test.go#L91), [runcontroller.go:20](../../../implementation/go/internal/loadgen/runcontroller.go#L20) |
| [Startup](#startup) | [h2client.go:31](../../../implementation/go/internal/h2client/h2client.go#L31), [runcontroller.go:83](../../../implementation/go/internal/loadgen/runcontroller.go#L83), [runcontroller.go:121](../../../implementation/go/internal/loadgen/runcontroller.go#L121) |
| [DrainPhase](#drainphase) | [runcontroller.go:109](../../../implementation/go/internal/loadgen/runcontroller.go#L109) |
| [Shutdown ordering](#shutdown-ordering) | [h2client.go:55](../../../implementation/go/internal/h2client/h2client.go#L55), [runcontroller.go:113](../../../implementation/go/internal/loadgen/runcontroller.go#L113) |
<!-- trace:end -->
