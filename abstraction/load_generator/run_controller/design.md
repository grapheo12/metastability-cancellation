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
