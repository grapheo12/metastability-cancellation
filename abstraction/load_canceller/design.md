# Load Canceller — Design

`now()` means `monotonic_now_ns() - run_start`. `D = config.duration_ns`. Integer arithmetic is on
unsigned 64-bit unless stated otherwise.

## Configuration validation

```
function validate(params):
    1 <= num_clients <= 2^32 - 1             else error
    cancel_rate > 0                          else error
    1 <= incr_rand_max <= 2^32 - 1           else error
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
  the stream reads `gap0, client0, step0, gap1, client1, step1, …`. The arrival times, clients and
  steps depend only on `seed`, `N`, `cancel_rate` and `incr_rand_max`. The **targets** also depend on
  the FinishedWatermarks the server has reported so far, so they can differ between runs with the
  same seed.
- **Open loop:** `send_async` opens a stream, sends HEADERS and DATA with END_STREAM, and returns
  without waiting for the response. The sender is late only when `sleep_until` wakes late or when
  the HTTP/2 layer blocks opening a stream at the peer's `MAX_CONCURRENT_STREAMS`. Lateness shows
  as `send_ns - scheduled_ns`.
- No arrival is scheduled at or after `D`.

## CancelTargetChoice

Each target is a small random step past the latest request the server reports as finished for that
client (its FinishedWatermark, [../protocol/design.md#cancelreplybody](../protocol/design.md#cancelreplybody)).

```
finished : map u32 -> u32, initially empty    # largest reported finished seq per client
lock     : mutex guarding finished            # choose_target (sender) reads, note_finished (reader) writes

function choose_target(rng, config):
    client_id = sampling.uniform_int(rng, 0, config.num_clients - 1)
    step      = sampling.uniform_int(rng, 1, config.incr_rand_max)
    with lock: base = finished.get(client_id)          # seq or none
    if base is none: s = step - 1                      # no reply yet: target in [0, incr_rand_max - 1]
    else:            s = base + step                   # u64 add: base <= 2^32-1, step <= 2^32-1, no wrap
    if s > 2^32 - 1:
        return (client_id, none)                       # past the last possible seq: skipped
    return (client_id, s)

procedure note_finished(client_id, f):                 # f: RequestId from a 202 reply
    (fseq, fclient) = protocol.split_request_id(f)
    if fclient != client_id: return                    # reply about another client: ignored
    with lock:
        if client_id not in finished or fseq > finished[client_id]:
            finished[client_id] = fseq                 # max: replies can be read out of order
```

- **What a step means.** The client keeps at most one request outstanding, so with base `b` the
  request in flight is usually `b + 1`. Step `1` aims at it (cancelled before or during
  processing), steps `2 ..= incr_rand_max` aim at upcoming requests, which wait in the server's
  pending set until they arrive.
- **Before the first reply** for a client the target is `step - 1`, as if `b = -1`.
- **Repeats are allowed.** Two arrivals for the same client before a newer reply can name the same
  id. The server stores a duplicate once ([../server/cancel_tracker/design.md#cancelmatching](../server/cancel_tracker/design.md#cancelmatching)).
- `finished` holds one entry per client that has had a `202` reply with a number, at most `N`.

Worked example with `incr_rand_max = 4` (seqs shown; RequestId = `(client << 32) | seq`):

| Event | client | step | `finished[client]` before | target seq | Note |
|---|---|---|---|---|---|
| arrival 0 | 3 | 2 | none | 1 | `step - 1` |
| reply to arrival 0 | 3 | — | none | — | `{"finished":"<id(3, 4)>"}`, so `finished[3] = 4` |
| arrival 1 | 3 | 1 | 4 | 5 | `4 + 1` |
| arrival 2 | 0 | 4 | none | 3 | `step - 1` |
| arrival 3 | 3 | 3 | 4 | 7 | `4 + 3`, reply to arrival 1 not read yet |
| reply to arrival 3 | 3 | — | 4 | — | reports seq 6, so `finished[3] = 6` |
| reply to arrival 1 | 3 | — | 6 | — | reports seq 5 (read late), `5 < 6`: unchanged |
| reply to arrival 2 | 0 | — | none | — | `{"finished":null}`: unchanged |

Boundary: with `finished[c] = 2^32 - 2` and step `3`, `s = 2^32 + 1 > 2^32 - 1`, so the arrival is
`skipped`.

## Response collection

The HTTP/2 layer calls `on_done` on whatever activity reads the connection. That is not the
sender, so the sender is never blocked by responses.

```
procedure complete(rec, result, outstanding):
    if not rec.state.compare_and_swap(PENDING, DONE): return     # drain already timed it out
    rec.end_ns = now()
    match result:
        Response(status, body):
            rec.http_status = status; rec.outcome = classify(status)
            if status == 202:
                f = protocol.parse_cancel_reply(body)               # u64 | none | ParseError
                if f is a u64:
                    rec.reported_finished = f
                    note_finished(rec.client_id, f)                 # CancelTargetChoice
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

Only a `202` body is read, for the FinishedWatermark ([Response collection](#response-collection)).
It never changes the outcome: a `202` with a `null` or unparseable body is still `accepted`.

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
- `finished` and its lock are the struct `finishedTable` (`map[uint32]uint32` plus a `sync.Mutex`). `note_finished` is `finishedTable.note`, called from `complete` inside the `COMPLETING` window, so a timed-out record never updates it. The pure arithmetic of `choose_target` is `nextTarget(base, hasBase, step)`.
- The response goroutine reads the whole body (`io.ReadAll`) and passes it to `complete`, which parses it with `protocol.ParseCancelReply` only for a `202`. `reported_finished` is `CancelRecord.ReportedFinished` plus the flag `HasReported`.
- `incr_rand_max` is the `-incr_rand_max` flag. It is parsed with `strconv.ParseUint(..., 10, 32)`, which enforces `<= 2^32 - 1`, and appears in the summary's `config` after `cancel_rate`, in API order.
- Tests: `canceller_test.go` checks the CancelTargetChoice worked example (including out-of-order replies and a reply about another client), the skipped boundary, reply parsing in `complete` (watermark, `null`, junk), and `incr_rand_max` validation.

### Traceability

Generated from the `// Design:` tags in the code by `implementation/spec_tools/tracegen.py` (see `AGENTS.md`). Each link points at the first code line after the tag.
Regenerate from the repository root with `python3 implementation/spec_tools/tracegen.py --lang Go --ext .go --comment // --write`.

<!-- trace:begin -->
| Design subsection | Implementation (file:line) |
|---|---|
| [Configuration validation](#configuration-validation) | [canceller.go:64](../../implementation/go/internal/canceller/canceller.go#L64), [canceller_test.go:87](../../implementation/go/internal/canceller/canceller_test.go#L87) |
| [Startup](#startup) | [canceller.go:196](../../implementation/go/internal/canceller/canceller.go#L196), [h2client.go:28](../../implementation/go/internal/h2client/h2client.go#L28) |
| [CancelSchedule](#cancelschedule) | [canceller.go:221](../../implementation/go/internal/canceller/canceller.go#L221), [canceller.go:251](../../implementation/go/internal/canceller/canceller.go#L251) |
| [CancelTargetChoice](#canceltargetchoice) | [canceller.go:154](../../implementation/go/internal/canceller/canceller.go#L154), [canceller.go:169](../../implementation/go/internal/canceller/canceller.go#L169), [canceller.go:273](../../implementation/go/internal/canceller/canceller.go#L273), [canceller.go:285](../../implementation/go/internal/canceller/canceller.go#L285), [canceller_test.go:14](../../implementation/go/internal/canceller/canceller_test.go#L14), [canceller_test.go:49](../../implementation/go/internal/canceller/canceller_test.go#L49) |
| [Response collection](#response-collection) | [canceller.go:299](../../implementation/go/internal/canceller/canceller.go#L299), [canceller_test.go:62](../../implementation/go/internal/canceller/canceller_test.go#L62) |
| [Outcome classification](#outcome-classification) | [canceller.go:108](../../implementation/go/internal/canceller/canceller.go#L108), [canceller.go:325](../../implementation/go/internal/canceller/canceller.go#L325), [canceller_test.go:106](../../implementation/go/internal/canceller/canceller_test.go#L106) |
| [CancellerDrain](#cancellerdrain) | [canceller.go:341](../../implementation/go/internal/canceller/canceller.go#L341), [h2client.go:52](../../implementation/go/internal/h2client/h2client.go#L52) |
| [Summary](#summary) | [canceller.go:397](../../implementation/go/internal/canceller/canceller.go#L397) |
<!-- trace:end -->
