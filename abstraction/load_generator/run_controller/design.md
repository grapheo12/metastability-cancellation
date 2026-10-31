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
    return LoadGenConfig(params,
                         per_client_rate = global_rate / num_clients,
                         slo_ns = round(slo_ms * 1e6),
                         duration_ns = round(duration_s * 1e9))
```

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
    tasks = []
    for i in 0 .. N-1:
        rng_i = sampling.rng_new(sampling.derive_seed(config.seed, STREAM_CLIENT_ARRIVALS, i))
        tasks.append(spawn run_client(i, conns[i], config, rng_i, run_start))
    all_records = []
    for i in 0 .. N-1:
        all_records.extend(join(tasks[i]))     # see DrainPhase
    run_end = monotonic_now_ns() - run_start
    shutdown(conns)
    report = metrics.aggregate(all_records, config, run_end)
    metrics.write_requests_csv(config.output_path + ".requests.csv", all_records)
    metrics.write_summary_json(config.output_path + ".summary.json", report)
    exit 0
```

Connections are opened one after another, in client-id order, so the server's accept order (and
so its per-connection seeds) matches the client ids.

## DrainPhase

No extra mechanism is needed. Each client stops sending once `now() >= duration_ns`, and its one
in-flight request is resolved by its response or by its SLO timer. Joining every client therefore
ends by about `duration_ns + slo_ns` (plus scheduling jitter). Requests that were scheduled but not
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

- File `internal/loadgen/runcontroller.go`: `Validate` and `Run` (the `main` of the design, minus flag parsing).
- `open_h2c_connection` is `h2client.Dial`. It does TCP dial, `Transport{AllowHTTP: true}.NewClientConn`, then a `Ping` round trip, so the server has accepted the connection and SETTINGS are exchanged before the next client dials. This keeps the server's accept order equal to client-id order.
- `join` is a `sync.WaitGroup`. `GOAWAY + close` is `ClientConn.Shutdown` with a 10 ms bound, then `Close`.

### Traceability

Generated from the `// Design:` tags in the code. Each link points at the first code line after the tag.

<!-- trace:begin -->
| Design subsection | Implementation (file:line) |
|---|---|
| [Configuration validation](#configuration-validation) | [runcontroller.go:20](../../../implementation/go/internal/loadgen/runcontroller.go#L20) |
| [Startup](#startup) | [h2client.go:28](../../../implementation/go/internal/h2client/h2client.go#L28), [runcontroller.go:78](../../../implementation/go/internal/loadgen/runcontroller.go#L78) |
| [DrainPhase](#drainphase) | [runcontroller.go:104](../../../implementation/go/internal/loadgen/runcontroller.go#L104) |
| [Shutdown ordering](#shutdown-ordering) | [h2client.go:52](../../../implementation/go/internal/h2client/h2client.go#L52), [runcontroller.go:108](../../../implementation/go/internal/loadgen/runcontroller.go#L108) |
<!-- trace:end -->
