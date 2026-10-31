# Load Canceller — Design

`now()` means `monotonic_now_ns() - run_start`. `D = config.duration_ns`. Integer arithmetic is on
unsigned 64-bit unless stated otherwise.

## Configuration validation

```
function validate(params):
    1 <= num_clients <= 2^32 - 1             else error
    cancel_rate > 0                          else error
    duration_s > 0                           else error
    seed parses as u64                       else error
    target parses as host:port               else error
    return CancellerConfig(params, duration_ns = round(duration_s * 1e9))
```

## Startup

```
function main(params):
    config = validate(params)                          # exit non-zero on error
    conn = open_h2c_connection(config.target)          # exit non-zero on failure
    rng = sampling.rng_new(sampling.derive_seed(config.seed, STREAM_CANCELLER, 0))
    run_start = monotonic_now_ns()
    records = run_schedule(conn, config, rng)          # CancelSchedule (the only sender)
    drain(conn, records)                               # CancellerDrain
    run_end = now()
    write_cancels_csv(config.output_path + ".cancels.csv", records)
    write_summary_json(config.output_path + ".summary.json", summarize(records, config, run_end))
    exit 0
```

## CancelSchedule

A single sender activity runs an open-loop Poisson process with rate `cancel_rate`.

```
function run_schedule(conn, config, rng):
    records = []
    outstanding = atomic u64 = 0
    arrival = 0
    t = sampling.exponential_ns(rng, config.cancel_rate)
    while t < D:
        sleep_until(t)
        (client_id, seq_or_none) = choose_target(rng, config)        # CancelTargetChoice
        if seq_or_none is none:
            records.append(CancelRecord(arrival, client_id, outcome = skipped, scheduled_ns = t))
        else:
            id = protocol.make_request_id(seq_or_none, client_id)
            rec = CancelRecord(arrival, id, client_id, seq_or_none, state = PENDING, scheduled_ns = t)
            records.append(rec)
            outstanding.fetch_add(1)
            rec.send_ns = now()
            conn.send_async(method = "DELETE", path = "/cancel",
                            body = protocol.build_cancel_body(id),
                            on_done = (result) -> complete(rec, result, outstanding))
        arrival = arrival + 1
        t = t + sampling.exponential_ns(rng, config.cancel_rate)
    return records
```

- **Draw order per arrival** (for reproducibility): the first gap is drawn before the loop. Each
  arrival then draws the client, then the step (both in `choose_target`), then the next gap. So
  the stream reads `gap0, client0, step0, gap1, client1, step1, …`. The schedule and the targets
  depend only on `seed`, `N` and `cancel_rate`, not on the server.
- **Open loop:** `send_async` opens a stream, sends HEADERS and DATA with END_STREAM, and returns
  without waiting for the response. The sender is late only when `sleep_until` wakes late or when
  the HTTP/2 layer blocks opening a stream at the peer's `MAX_CONCURRENT_STREAMS`. Lateness shows
  as `send_ns - scheduled_ns`.
- No arrival is scheduled at or after `D`.

## CancelTargetChoice

```
last : map u32 -> u64, initially empty               # last counter sent per client

function choose_target(rng, config):
    client_id = sampling.uniform_int(rng, 0, config.num_clients - 1)
    step      = sampling.uniform_int(rng, 1, INCR_RAND_MAX)
    if client_id in last: s = last[client_id] + step
    else:                 s = step - 1                # first target in [0, INCR_RAND_MAX - 1]
    if s > 2^32 - 1:
        return (client_id, none)                      # counter exhausted: skipped, last unchanged
    last[client_id] = s
    return (client_id, s)
```

Worked example with `INCR_RAND_MAX = 4`:

| arrival | client drawn | step drawn | `last` before | target seq | RequestId |
|---|---|---|---|---|---|
| 0 | 3 | 2 | — | 1 | `(3 << 32) \| 1 = 12884901889` |
| 1 | 0 | 4 | — | 3 | `3` |
| 2 | 3 | 1 | 1 | 2 | `12884901890` |
| 3 | 3 | 3 | 2 | 5 | `12884901893` |

`last` holds one entry per client that has been chosen, at most `min(N, arrivals)` entries.

## Response collection

The HTTP/2 layer calls `on_done` on whatever activity reads the connection. That is not the
sender, so the sender is never blocked by responses.

```
procedure complete(rec, result, outstanding):
    if not rec.state.compare_and_swap(PENDING, DONE): return     # drain already timed it out
    rec.end_ns = now()
    match result:
        Response(status, body): rec.http_status = status; rec.outcome = classify(status)
        StreamError or ConnectionError: rec.outcome = error
    outstanding.fetch_sub(1, release)
```

## Outcome classification

From [../protocol/design.md#status-classification-load-canceller](../protocol/design.md#status-classification-load-canceller):

| Result | `outcome` |
|---|---|
| `202` | `accepted` |
| `503` | `rejected` (server's CancelRegistry full) |
| `400` | `bad_request` |
| other status | `error` |
| stream or connection error | `error` |
| no result before the drain timeout | `timeout` |
| counter exhausted | `skipped` |

The response body is ignored. `/cancel` responses are always empty.

## CancellerDrain

```
procedure drain(conn, records):
    deadline = now() + CANCELLER_DRAIN_TIMEOUT_MS * 1e6
    wait until outstanding.load(acquire) == 0 or now() >= deadline
    for rec in records with state PENDING:
        if rec.state.compare_and_swap(PENDING, TIMED_OUT): rec.outcome = timeout
    send GOAWAY and close conn                       # errors ignored
```

The compare-and-swap on `state` makes sure every record is resolved by exactly one of `complete`
and `drain`. After `drain` returns, no `on_done` can change a record, so writing the outputs needs
no locking.

## Summary

```
function summarize(records, config, run_end):
    counts.scheduled = len(records)
    counts[o] = number of records with outcome o, for each outcome o
    counts.sent = counts.scheduled - counts.skipped
    rates.offered  = counts.scheduled / config.duration_s
    rates.sent     = counts.sent      / config.duration_s
    rates.accepted = counts.accepted  / config.duration_s
    clients_targeted = number of distinct client_id among records with outcome != skipped
    return { config, run_end_s: run_end / 1e9, counts, rates_per_s: rates, clients_targeted }
```

Whether an accepted cancel actually stopped work is visible only in the server's CancelCounters
([../server/api.md#cancelcounters-output](../server/api.md#cancelcounters-output)) and in the load
generator's `rejected` count.

## Design specific to Go

Implementation: `implementation/go` (module `testbed`, Go 1.21, `golang.org/x/net v0.17.0`).
Build and test with `CGO_ENABLED=0 go build ./...` / `CGO_ENABLED=0 go test ./...`. On recent macOS the dynamic
loader rejects cgo-linked Go 1.21 binaries (missing `LC_UUID`), so cgo is disabled.

### Go choices

- Package `internal/canceller`, file `canceller.go`. Flags are parsed in `cmd/canceller/main.go`.
- `send_async` calls `RoundTrip` on a new goroutine per cancel, because `RoundTrip` blocks until response headers. The sender loop never waits. That goroutine also plays the role of `on_done`.
- The record state machine has an extra state: `PENDING → COMPLETING → DONE` (instead of `PENDING → DONE`). `drain` spins (`runtime.Gosched`) on `COMPLETING`, so it never reads a record that a response goroutine is still writing. Exactly one of `complete` and `drain` still resolves each record.
- The drain loop polls `outstanding` every 1 ms until it reaches 0 or the deadline passes.
- `last` is a `map[uint32]uint64`.

### Traceability

Generated from the `// Design:` tags in the code by `implementation/tracegen/tracegen.py` (see `AGENTS.md`). Each link points at the first code line after the tag.
Regenerate from the repository root with `python3 implementation/tracegen/tracegen.py --lang Go --ext .go --comment // --write`.

<!-- trace:begin -->
| Design subsection | Implementation (file:line) |
|---|---|
| [Configuration validation](#configuration-validation) | [canceller.go:64](../../implementation/go/internal/canceller/canceller.go#L64) |
| [Startup](#startup) | [canceller.go:157](../../implementation/go/internal/canceller/canceller.go#L157), [h2client.go:28](../../implementation/go/internal/h2client/h2client.go#L28) |
| [CancelSchedule](#cancelschedule) | [canceller.go:182](../../implementation/go/internal/canceller/canceller.go#L182), [canceller.go:212](../../implementation/go/internal/canceller/canceller.go#L212) |
| [CancelTargetChoice](#canceltargetchoice) | [canceller.go:232](../../implementation/go/internal/canceller/canceller.go#L232), [canceller.go:243](../../implementation/go/internal/canceller/canceller.go#L243), [canceller_test.go:14](../../implementation/go/internal/canceller/canceller_test.go#L14) |
| [Response collection](#response-collection) | [canceller.go:260](../../implementation/go/internal/canceller/canceller.go#L260) |
| [Outcome classification](#outcome-classification) | [canceller.go:103](../../implementation/go/internal/canceller/canceller.go#L103), [canceller.go:280](../../implementation/go/internal/canceller/canceller.go#L280), [canceller_test.go:40](../../implementation/go/internal/canceller/canceller_test.go#L40) |
| [CancellerDrain](#cancellerdrain) | [canceller.go:296](../../implementation/go/internal/canceller/canceller.go#L296), [h2client.go:52](../../implementation/go/internal/h2client/h2client.go#L52) |
| [Summary](#summary) | [canceller.go:351](../../implementation/go/internal/canceller/canceller.go#L351) |
<!-- trace:end -->
