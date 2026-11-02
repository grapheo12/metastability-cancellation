# Load Canceller — Design

`now()` means `monotonic_now_ns() - run_start`, where `run_start` is the Ingress's time base (the first `/ping`,
[../design.md#time-base](../design.md#time-base)). `D = config.duration_ns`. Integer arithmetic is on unsigned
64-bit unless stated otherwise. `B` is the number of backends.

## Startup

```
function canceller_start(config, inflight, run_start):       # called once, on the first /ping, if cancel_rate > 0
    rng = sampling.rng_new(sampling.derive_seed(config.seed, STREAM_CANCELLER, 0))
    canceller = { records: [], outstanding: atomic u64 = 0, stop: atomic boolean = false,
                  conns: array[B] of connection or none }
    spawn sender(canceller, config, inflight, rng)           # the only sender; the caller does not wait
    return canceller

procedure sender(canceller, config, inflight, rng):
    for s in 0 ..= B-1:                                      # one connection per backend, /cancel only
        canceller.conns[s] = open_h2c_connection(config.backends[s], timeout = UPSTREAM_DIAL_TIMEOUT_MS)
                             or none if that fails
    run_schedule(canceller, config, inflight, rng)           # CancelSchedule
```

The canceller's connections never carry a `/ping`, so the server never binds them to a client
([../../server/ping_handler/design.md#client-binding](../../server/ping_handler/design.md#client-binding)), and a
`/cancel` for any client may travel on them. Dialing happens on the sender, before the first arrival is due; if it
takes longer than the first gap, the first arrivals are late, which shows as `send_ns - scheduled_ns`.

## CancelSchedule

A single sender activity runs an open-loop Poisson process with rate `cancel_rate`.

```
function run_schedule(canceller, config, inflight, rng):
    arrival = 0
    t = sampling.exponential_ns(rng, config.cancel_rate)
    while t < D and not canceller.stop.load():
        sleep_until(t)                                        # woken early if stop is set
        if canceller.stop.load(): break
        victim = choose_victim(rng, inflight)                 # InFlightVictimChoice
        if victim is none:
            canceller.records.append(CancelRecord(arrival, outcome = skipped, scheduled_ns = t))
        else:
            (client_id, entry) = victim
            rec = CancelRecord(arrival, entry.request_id, client_id, entry.server_id,
                               target_seq = low 32 bits of entry.request_id, state = PENDING, scheduled_ns = t)
            canceller.records.append(rec)
            rec.send_ns = now()
            conn = canceller.conns[entry.server_id]
            if conn is none:
                rec.end_ns = now(); rec.outcome = error; rec.state = DONE      # backend unreachable
            else:
                canceller.outstanding.fetch_add(1)
                conn.send_async(method = "DELETE", path = "/cancel",
                                body = protocol.build_cancel_body(entry.request_id),
                                on_done = (result) -> complete(rec, result, canceller.outstanding))
        arrival = arrival + 1
        t = t + sampling.exponential_ns(rng, config.cancel_rate)
```

- **Draw order** (for reproducibility): the first gap is drawn before the loop. Each arrival then draws the victim
  index (only when the snapshot is non-empty, in `choose_victim`), then the next gap. The arrival times depend only
  on `seed` and `cancel_rate`; the victims depend on what is in flight, so they differ between runs.
- **Open loop:** `send_async` opens a stream, sends HEADERS and DATA with END_STREAM, and returns without waiting
  for the response. The sender is late only when `sleep_until` wakes late or the HTTP/2 layer blocks opening a
  stream. Lateness shows as `send_ns - scheduled_ns`.
- No arrival is scheduled at or after `D`, and none after `stop` is set.

## InFlightVictimChoice

```
function choose_victim(rng, inflight):
    snap = inflight_map.snapshot(inflight)                # list of (client_id, InFlightEntry), client-id order
    k = len(snap)
    if k == 0: return none                                # nothing in flight: skipped, no draw
    i = sampling.uniform_int(rng, 0, k - 1)
    return snap[i]
```

- Every request in flight at the snapshot is equally likely to be chosen, whichever client or backend it belongs
  to.
- The victim may finish between the snapshot and the moment its `/cancel` reaches the backend. The server then
  counts it `completed_before_cancel`; nothing else happens. The shorter the requests, the more often this occurs.
- Two arrivals close together may pick the same request. The server stores a duplicate id once
  ([../../server/cancel_tracker/design.md#cancelmatching](../../server/cancel_tracker/design.md#cancelmatching)).

Worked example, 4 clients, 2 backends:

| Snapshot (client: request seq @ server) | `k` | Draw `i` | Victim | `/cancel` sent to |
|---|---|---|---|---|
| 0: 17 @ 1, 2: 9 @ 0, 3: 30 @ 1 | 3 | 1 | client 2, seq 9 | server 0 |
| 1: 4 @ 0 | 1 | 0 | client 1, seq 4 | server 0 |
| (empty) | 0 | (no draw) | none | — (`skipped`) |

## Response collection

The HTTP/2 layer calls `on_done` on whatever activity reads the connection. That is not the sender, so the sender is
never blocked by responses.

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

From [../../protocol/design.md#status-classification-load-canceller](../../protocol/design.md#status-classification-load-canceller):

| Result | `outcome` |
|---|---|
| `202` | `accepted` |
| `503` | `rejected` (server's CancelRegistry full) |
| `400` | `bad_request` |
| other status | `error` |
| stream or connection error, or no connection to that backend | `error` |
| no result before the drain timeout | `timeout` |
| nothing in flight at the arrival | `skipped` |

The response body is not read for anything: the server's CancelReplyBody is ignored, because the victim comes from
the InFlightMap, not from a guess.

## CancellerDrain

```
function canceller_stop(canceller):
    canceller.stop.store(true); wake the sender; wait for the sender to return
    deadline = now() + CANCELLER_DRAIN_TIMEOUT_MS * 1e6
    wait until canceller.outstanding.load(acquire) == 0 or now() >= deadline
    for rec in canceller.records with state PENDING:
        if rec.state.compare_and_swap(PENDING, TIMED_OUT): rec.outcome = timeout
    send GOAWAY on and close every connection in canceller.conns   # errors ignored
    return canceller.records
```

The compare-and-swap on `state` makes sure every record is resolved by exactly one of `complete` and the drain.
After `canceller_stop` returns, no `on_done` can change a record, so writing the outputs needs no locking. The sender
normally ends on its own at `D`; the Ingress is stopped after the load generator, so `stop` usually finds it done.

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
    return { counts, rates_per_s: rates, clients_targeted }
```

With the LoadCanceller disabled (`cancel_rate = 0`) every count, rate and `clients_targeted` is `0`.

Whether an accepted cancel actually stopped work is visible only in the servers' CancelCounters
([../../server/api.md#cancelcounters-output](../../server/api.md#cancelcounters-output)) and in the load
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
| [Startup](#startup) |  |
| [CancelSchedule](#cancelschedule) |  |
| [InFlightVictimChoice](#inflightvictimchoice) |  |
| [Response collection](#response-collection) |  |
| [Outcome classification](#outcome-classification) |  |
| [CancellerDrain](#cancellerdrain) |  |
| [Summary](#summary) |  |
<!-- trace:end -->
