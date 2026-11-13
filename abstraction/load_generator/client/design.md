# Client — Design

`now()` means `monotonic_now_ns() - run_start`. `D = config.duration_ns`, `SLO = config.slo_ns`,
`λ = config.per_client_rate`, `W = config.n_waiting_requests`, `RETRY = config.retry_timeout_ns`,
`R = config.max_retries`, `STALL = config.initial_stall_ns`.

## SemiOpenLoopClient

`W = config.n_waiting_requests`. A client has two activities that share one record `C`:

```
C = { lock, slot_free: condition variable, outstanding: integer = 0,       # requests sent, not yet resolved
      events: FIFO of Event (unbounded, multi-producer, single consumer),  # to the receiver
      records: list of RequestRecord }                                     # written by the receiver only

function run_client(client_id, conn, config, rng, run_start, run_start_unix_ns):
    spawn receiver(C, client_id)
    sender(C, client_id, conn, config, rng)          # runs on the calling activity
    wait until the receiver has returned
    sort C.records by seq
    return C.records

procedure sender(C, client_id, conn, config, rng):
    seq = 0
    t_sched = sampling.exponential_ns(rng, λ)        # first arrival: one gap after 0
    while t_sched < D:
        sleep_until(t_sched)                         # no-op if t_sched is already past
        with C.lock:
            while C.outstanding == W: C.slot_free.wait()     # GatedArrival
            sendable = now() < D
            if sendable: C.outstanding = C.outstanding + 1
        if sendable:
            start_request(C, client_id, seq, t_sched, conn, config, run_start_unix_ns)   # never waits
        else:
            C.events.push(NotSent(seq, t_sched))
        if seq == 2^32 - 1: break
        seq = seq + 1
        t_sched = t_sched + sampling.exponential_ns(rng, λ)
    C.events.push(SenderDone)

procedure receiver(C, client_id):
    sender_done = false
    loop:
        ev = C.events.pop_blocking()
        match ev:
            NotSent(seq, t_sched):
                C.records.append(not_sent_record(client_id, seq, t_sched))
            Resolved(record):
                C.records.append(record)
                with C.lock:
                    C.outstanding = C.outstanding - 1
                    C.slot_free.notify_one()
            SenderDone:
                sender_done = true
        if sender_done:
            with C.lock: if C.outstanding == 0: return
```

- **Schedule.** Arrivals are at the running sums of Exp(λ) gaps. The `j`-th gap is always the `j`-th sample of
  this client's `Rng`, so the arrival schedule depends only on `seed`, `client_id` and λ, not on how the server
  behaves or on `W`.
- **seq** is the index of the scheduled arrival (starting at 0), and every scheduled arrival uses one. After
  `seq = 2^32 - 1` the client stops scheduling.
- **No arrival at or after `D`.** An arrival at `t_sched < D` that becomes sendable only at `now() >= D` is
  `not_sent`.
- **Up to `W` outstanding.** The sender alone increments `outstanding`, and only while it is below `W`; the
  receiver alone decrements it. So `0 <= outstanding <= W` at all times.
- **`W = 1` is the old closed loop.** The next arrival is sent only after the previous request resolved, exactly
  the behavior before this parameter existed.
- **Up to `W` outstanding** counts logical requests, not RequestCopies: a request holds one slot from its first
  send until it resolves, however many copies it sent.
- **Ends** when the sender is done and every sent request has resolved. Each one resolves within `SLO` of its
  first send, so the client returns by about `D + SLO`.

## GatedArrival

An arrival whose time has come while `W` requests are outstanding waits until one of them resolves, then is
sent at once. Its end-to-end latency includes that wait (`send_ns - scheduled_ns`). The schedule does **not**
move: if several arrivals pass during the wait, they are sent back-to-back as slots free up. With `W` large
enough that the cap is never reached, the client is open loop.

Worked example, `W = 2`, arrivals at 0, 1, 2 and 3 ms, time at the server 5, 1.5, 1 and 1 ms (no SLO hit):

| ms | Event | `outstanding` after | Record (`seq`: scheduled → sent → resolved) |
|---|---|---|---|
| 0 | send 0 | 1 | |
| 1 | send 1 | 2 | |
| 2 | arrival 2 due, `outstanding == W` | 2 | gated |
| 2.5 | 1 resolves; send 2 | 2 | 1: 1 → 1 → 2.5 |
| 3 | arrival 3 due, `outstanding == W` | 2 | gated |
| 3.5 | 2 resolves; send 3 | 2 | 2: 2 → 2.5 → 3.5 (waited 0.5) |
| 4.5 | 3 resolves | 1 | 3: 3 → 3.5 → 4.5 (waited 0.5) |
| 5 | 0 resolves | 0 | 0: 0 → 0 → 5 |

With `W = 1` the same arrivals give sends at 0, 5, 6.5 and 7.5 ms.

## start_request

`start_request` sends copy 0 of a request and returns; the request's first deciding event becomes one `Resolved`
event for the receiver.

```
procedure start_request(C, client_id, seq, t_sched, conn, config, run_start_unix_ns):
    id   = protocol.make_request_id(seq, client_id)
    t_send = now()                                    # first send: SLO and record clocks start here
    deadline_unix_ns = run_start_unix_ns + t_send + SLO      # RequestDeadline, fixed for the request
    body = protocol.build_request_body(id, deadline_unix_ns, config.request_bytes)   # every copy reuses it
    req = { id, seq, t_sched, t_send, client_id,
            in_stall_window: boolean = (t_send < STALL),   # StallWindowRequest: never retries
            lock,                                     # guards current, streams, reset_all
            current: integer = 0,                     # index of the newest RequestCopy
            streams: list of stream = [],             # every copy opened so far
            reset_all: boolean = false,               # set once SLOCancellation has reset the streams
            settled: atomic boolean = false }
    at timer t_send + SLO:                            # SLOCancellation
        if settle(C, req, TimerFired):
            with req.lock:
                req.reset_all = true
                open_streams = copy of req.streams
            for s in open_streams: s.reset(RST_CODE_CANCEL)
    send_copy(C, req, 0, conn, config, body)

function settle(C, req, result):                      # first event wins
    if not req.settled.compare_and_swap(false, true): return false
    t_end = now()
    with req.lock: copies = req.current + 1
    match result:
        Response(status, resp_body): outcome = classify(status, resp_body, req.id)  # see Outcome classification
        TimerFired:                  outcome = cancelled; status = none
        Dropped:                     outcome = dropped;   status = none
        StreamError or ConnectionError: outcome = error; status = none
    C.events.push(Resolved(RequestRecord(req.id, req.client_id, req.seq, outcome, status,
                                         req.t_sched, req.t_send, t_end, copies,
                                         req.in_stall_window)))
    return true
```

The callbacks may run on any activity (the HTTP/2 layer's reader, a timer). They never block: they hold
`req.lock` only for a few field reads and writes, and `settle` does one compare-and-swap, one short locked read
and one push. Opening and resetting streams happen outside `req.lock`.

## RequestDeadline

Every request carries a **RequestDeadline** in its PingRequestBody
([../../protocol/design.md#pingrequestbody](../../protocol/design.md#pingrequestbody)): the Unix wall-clock time by
which its SLO ends,

```
deadline_unix_ns = run_start_unix_ns + t_send + SLO        # unsigned 64-bit integer ns
```

- `run_start_unix_ns` is the Run Controller's wall-clock reading of `run_start`
  ([../design.md#time-base](../design.md#time-base)); `t_send` is the first copy's send on the monotonic time base.
  So the deadline equals the moment the SLO timer fires, expressed in Unix time, and all clients of one load
  generator use one origin.
- It is computed once, in `start_request`, and the body is built once. Every RequestCopy sends that same body
  ([RetryTimer](#retrytimer)), so **a retry has the same deadline as the original request**.
- A StallWindowRequest carries a deadline like any other request.

Worked example, `run_start_unix_ns = 1_794_533_185_000_000_000`, `SLO = 300 ms`, `RETRY = 150 ms`, `R = 1`:

| Copy | Sent at `t_send` (ms) | `deadline_unix_ns` | Check |
|---|---|---|---|
| 0 | 2.5 | 1_794_533_185_302_500_000 | 1_794_533_185_000_000_000 + 2_500_000 + 300_000_000 |
| 1 (retry at 152.5 ms) | — | 1_794_533_185_302_500_000 | same body as copy 0, so the same deadline |

## RetryTimer

Every **RequestCopy** of a request is the same RequestId and the same body, sent on a new stream of the client's
connection. Each copy `k` has its own **RetryTimer**, `RETRY` after that copy's send. Only the newest copy
(`k == req.current`) can decide the request; the responses and errors of earlier copies are ignored, and their
streams are left open (no reset) until the server answers them or SLOCancellation resets them.

```
procedure send_copy(C, req, k, conn, config, body):   # req.current == k already; called without req.lock
    t_copy = now()
    stream = conn.open_stream(headers = protocol request headers, body = body, end_stream = true)
    if open_stream failed: settle(C, req, ConnectionError); return
    with req.lock:
        req.streams.append(stream)
        late = req.reset_all
    if late: stream.reset(RST_CODE_CANCEL); return    # SLOCancellation already ran
    on stream.full_response(status, resp_body):       # HEADERS + DATA to END_STREAM
        with req.lock: newest = (k == req.current)
        if newest: settle(C, req, Response(status, resp_body))
    on stream.error():
        with req.lock: newest = (k == req.current)
        if newest: settle(C, req, StreamError)
    if req.in_stall_window: return                    # StallWindow: no RetryTimer
    at timer t_copy + RETRY:                          # RetryTimer of copy k
        with req.lock:
            if req.settled or k != req.current: return
            last = (k == config.max_retries)
            if not last: req.current = k + 1
        if last: settle(C, req, Dropped)              # no reset: the stream is abandoned
        else:    send_copy(C, req, k + 1, conn, config, body)
```

- **Copies.** A request sends at most `1 + R` copies. `copies` in its RequestRecord is `req.current + 1` when it
  settles: every copy the client started, including one whose `open_stream` failed.
- **Which answer counts.** Once copy `k+1` is started, the client waits only for copy `k+1`. A response of copy
  `k` that arrives later is ignored even if it is `ok`.
- **Dropped.** When the RetryTimer of copy `R` fires before any deciding event, the request resolves as `dropped`.
  Its streams are not reset; the server may still answer them, and those answers are ignored.
- **`R = 0`** gives one copy whose RetryTimer drops it after `RETRY`. With `R = 0` and `RETRY >= SLO`,
  SLOCancellation always fires first, which is the behavior before retries existed.
- **Races.** Every event checks `req.current` and `req.settled`, and `settle` decides by compare-and-swap, so
  whichever deciding event is observed first wins and the others are ignored. A RetryTimer and a response of the
  same copy at about the same moment: if the timer takes `req.lock` first, the copy is superseded and its response
  is ignored.
- **Open streams.** At most `W × (1 + R)` streams belong to unresolved requests at any time. Validation keeps this
  within `MAX_WAITING_REQUESTS`, which every server and Ingress advertises as `SETTINGS_MAX_CONCURRENT_STREAMS`
  ([../../api.md](../../api.md#load-generator-parameters)). Ignored copies of resolved requests may add to it
  until the server answers them; the HTTP/2 layer then holds back new streams.

Worked examples, `RETRY = 150 ms`, `SLO = 300 ms`, `R = 1`, a request first sent at 0 ms (times in ms):

| Case | Events | Record (`outcome`, `copies`, `end_ns - send_ns`) |
|---|---|---|
| Fast answer | copy 0 answers at 100 | `ok`, 1, 100 |
| Retry answered | RetryTimer of copy 0 at 150 sends copy 1; copy 1 answers at 250 | `ok`, 2, 250 |
| Late original | copy 1 sent at 150; copy 0 answers at 200 (ignored); copy 1 answers at 260 | `ok`, 2, 260 |
| No answer | copy 1 sent at 150; SLO timer at 300 settles first and resets copies 0 and 1; copy 1's RetryTimer (due at 300 or later) then does nothing | `cancelled`, 2, 300 |
| Dropped | `RETRY = 100`: copy 1 sent at 100; copy 1's RetryTimer at 200 | `dropped`, 2, 200 |
| `R = 0` | `RETRY = 100`: copy 0's RetryTimer at 100 | `dropped`, 1, 100 |
| `R = 0`, `RETRY = 300` | SLO timer and RetryTimer both due at 300; whichever settles first wins | `cancelled` or `dropped`, 1, 300 |

With the defaults (`RETRY = 150`, `R = 1`) and `slo_ms = 300`, copy 1 is sent at 150 ms or later, so its
RetryTimer is due at 300 ms or later. SLOCancellation fires first, or ties and then the first `settle` wins, so
those runs record `cancelled` and almost never `dropped`. `dropped` is reliable only when `(1 + R) × RETRY < SLO`.

## StallWindow

The **StallWindow** is `[0, STALL)` of the client's time base, `STALL = initial_stall_ns`. It mirrors the servers'
StallGate ([../../server/worker_pool/design.md](../../server/worker_pool/design.md#stallgate)), during which no
`/ping` runs, so every request sent then is slow by construction. A request whose first send `t_send` is in the
window (`t_send < STALL`) is a **StallWindowRequest**:

- It sends only copy 0. `send_copy` returns before starting a RetryTimer, so it never retries and never ends
  `dropped`; `copies` is always 1.
- SLOCancellation still applies: it ends `ok`, `rejected`, `cancelled` or `error` as before retries existed.
- It holds a GatedArrival slot like any other request.
- Its RequestRecord has `in_stall_window = true`. Metrics leaves it out of every count, rate and latency statistic
  ([../metrics/design.md#collection](../metrics/design.md#collection)), and Availability skips it.

The window is approximate. A server's stall starts at its first *admitted* `/ping`, a little after `run_start`
(network and Ingress delay), and its backlog drains only after the stall. So requests sent just after `STALL` can
still be slow; they are counted normally. `STALL = 0` disables the window: no request is a StallWindowRequest.

Worked examples, `STALL = 1000 ms`, `RETRY = 150 ms`, `SLO = 300 ms`, `R = 1`:

| First send | `t_send < STALL` | Behavior |
|---|---|---|
| 0 ms | yes | copy 0 only; SLO at 300 ms → `cancelled` if unanswered; excluded from metrics |
| 999 ms | yes | copy 0 only, although its SLO runs to 1299 ms |
| 1000 ms | no | normal request: RetryTimer at 1150 ms, SLO at 1300 ms |
| any, with `STALL = 0` | no | normal request |

## SLOCancellation

- The SLO timer starts at `t_send`, the first copy's send, taken just before copy 0's stream is opened. RetryTimers
  never restart it.
- "Answered" means the **full** response (END_STREAM) of the newest copy has arrived. Headers alone do not count.
- If the response completes and the timer fires at about the same moment, whichever calls `settle` first wins
  (the compare-and-swap). The other event is ignored.
- When it wins, the client resets **every** copy it opened, including ignored earlier copies. A copy whose stream
  opens after that is reset at once (`reset_all`).
- After `reset`, the client never reads that stream again. Frames that arrive later are discarded by
  the HTTP/2 layer.
- A connection error makes **every** outstanding request of the client `error` (its newest copy's stream fails).
  Later arrivals and RequestCopies on a broken connection each fail at once as `error` (the client does not
  reconnect).
- Each request has its own timers. Several requests of one client can retry, time out, and be reset,
  independently.

## Outcome classification

```
function classify(status, resp_body, id):
    parsed = protocol.parse_body(resp_body)
    if parsed is ParseError or parsed != id: return error
    if status == 202: return ok
    if status == 503: return rejected
    return error
```

## State machine

Per scheduled arrival:

```mermaid
stateDiagram-v2
  [*] --> Waiting
  Waiting --> Gated: t_sched reached, outstanding == W
  Waiting --> Ready: t_sched reached, outstanding < W
  Gated --> Ready: a request resolves (slot free)
  Ready --> NotSent: now >= D
  Ready --> InFlight: now < D (outstanding += 1, open copy 0)
  InFlight --> InFlight: RetryTimer of newest copy k < R (open copy k+1)
  InFlight --> Resolved: full response of newest copy (ok / rejected / error)
  InFlight --> Resolved: RetryTimer of copy R (dropped, no reset)
  InFlight --> Resolved: SLO timer (RST_STREAM on every copy, cancelled)
  InFlight --> Resolved: stream/conn error on newest copy (error)
  Resolved --> [*]: receiver records it, outstanding -= 1
  NotSent --> [*]: receiver records it
```

Events of copies older than the newest one never change the state. A StallWindowRequest never takes the two RetryTimer
transitions.
