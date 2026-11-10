# Cancel Tracker — Design

All integer arithmetic is on unsigned 64-bit. `cap = CANCEL_REGISTRY_CAPACITY`. `R = RECENT_FINISHED` (`4096`).

## CancelRegistry

A fixed-capacity open-addressing hash table with linear probing. Finding and creating slots takes no lock, the
table never resizes, and slots are never deleted.

```
keys     : array[cap] of atomic u64, all EMPTY_KEY (0)     # stored key = client_id + 1
heads    : array[cap] of atomic pointer to Node, all null  # CancelInbox per slot
finished : array[cap] of atomic u64, all 0                 # FinishedWatermark per slot: id + 1, 0 = none
state    : array[cap] of ClientCancelState, all initial    # see ClientCancelState
Node     : { id: u64, next: pointer to Node }              # immutable once published

function registry_new(capacity):
    assert capacity is a power of two
    return CancelRegistry(keys, heads, finished, state)

function find_slot(registry, client_id, insert):         # -> slot index | none | Full
    key = client_id + 1
    i = mix64(client_id) & (cap - 1)
    repeat cap times:
        k = keys[i].load(acquire)
        if k == key: return i
        if k == EMPTY_KEY:
            if not insert: return none
            if keys[i].compare_and_swap(EMPTY_KEY, key, acq_rel) succeeds: return i
            if keys[i].load(acquire) == key: return i     # another thread inserted the same key
            # another key took this slot: keep probing
        i = (i + 1) & (cap - 1)
    return Full if insert else none

function registry_client(registry, client_id):           # -> ClientSlot
    i = find_slot(registry, client_id, insert = true)
    return none if i is Full else i
```

`mix64` is from [../../sampling/design.md#splitmix64](../../sampling/design.md#splitmix64). Because
slots are never deleted, a probe that reaches an `EMPTY_KEY` proves the key is absent. A `ClientSlot` stays valid
for the life of the process.

## ClientCancelState

```
ClientCancelState: { lock: mutual-exclusion lock,
                     pending:    map u64 -> u64 = empty,     # drained /cancel id -> finish_count when drained
                     unfinished: set of u64 = empty,         # ids a worker has started and not finished
                     recent:     ring of (id: u64, completed: boolean), capacity R, oldest overwritten,
                     recent_index: map u64 -> boolean,       # the ids now in `recent`, to their `completed`
                     finish_count: u64 = 0 }                 # requests of this client finished so far
```

Any worker can run any client's request ([../worker_pool/design.md#workerpool](../worker_pool/design.md#workerpool)),
and a client can have many requests queued and running at once, in any order
([Out-of-order requests](#out-of-order-requests)). So the matching state lives in the client's registry slot and
tracks requests **by id**, never by order. Every read or write of these fields, and every `registry_drain` and
`finished` store, happens with the slot's `lock` held:

| Operation | Takes `lock` | Held for |
|---|---|---|
| `registry_start` | yes | one set insert |
| `should_cancel` (CancelMatching) | yes | one drain, resolving each drained id, one lookup |
| `registry_publish_finished` | yes | set removal, ring push, at most one pending lookup, two max-updates; an aging sweep every `R` finishes |
| `registry_push` (`/cancel`) | no | lock-free Treiber push |
| `registry_finished` (`/cancel` reply) | no | one atomic load |
| `registry_client` | no | lock-free probe |

The lock is never held across a burn or across another lock. A caller that finds it taken waits for at most one
such operation of another worker or connection of the same client.

**Bounded memory.** `unfinished` holds only requests started and not yet finished, so it is bounded by
`n_worker_threads`. `recent` holds at most `R` entries. `pending` loses every id older than `R` finishes
of its client (see the aging sweep in [FinishedWatermark](#finishedwatermark)). The ring is allocated on first use.

The connection's binding (`ConnectionCancelState.client_id`) is not part of this state. It belongs to the
connection and is used only when the Connection Handler admits a `/ping`
([../ping_handler/design.md#client-binding](../ping_handler/design.md#client-binding)).

```
procedure registry_start(registry, slot, id):        # Ping Handler, when a worker starts the request
    if slot is none: return
    with state[slot].lock:
        state[slot].unfinished.insert(id)
```

## CancelInbox

A Treiber stack per slot with multi-producer push and a "take all" by whoever holds the slot's lock.

```
function registry_push(registry, client_id, id):
    i = find_slot(registry, client_id, insert = true)
    if i is Full: return Full
    node = new Node(id, next = null)
    loop:
        h = heads[i].load(acquire)
        node.next = h
        if heads[i].compare_and_swap(h, node, release) succeeds: return ok

function registry_drain(registry, slot):                  # caller holds state[slot].lock
    h = heads[slot].exchange(null, acq_rel)
    ids = []
    while h != null:
        ids.append(h.id)
        n = h.next
        free(h)
        h = n
    return ids
```

Why this is safe without a lock on the push side:
- Pushers never dereference any node except the one they create. They only copy the head pointer
  into `node.next`. So there is no ABA problem and no use-after-free.
- There is one consumer at a time: whoever holds the slot's lock. It detaches the whole list in
  one atomic `exchange`. After that the detached nodes are reachable only by that worker, which
  may free them.
- The `release` on a successful push CAS paired with the `acq_rel` exchange makes each node's `id`
  and `next` visible to the drainer.

## FinishedWatermark

The **FinishedWatermark** of a client is the highest RequestId of that client whose `/ping` a worker has
finished, with any outcome (`202`, `503` or stopped by RST). The Ping Handler publishes it when a request
finishes, and `handle_cancel` reports it in every CancelReplyBody
([../../protocol/design.md#cancelreplybody](../../protocol/design.md#cancelreplybody)). The same call records the
request's outcome for CancelMatching.

```
function registry_publish_finished(registry, slot, id, completed, counters):   # any worker
    if slot is none: return                               # registry full: nothing published; replies say null
    with state[slot].lock:
        st = state[slot]
        st.unfinished.remove(id)
        if st.recent is full:
            (old, _) = st.recent.oldest(); st.recent_index.remove(old)
        st.recent.push((id, completed)); st.recent_index[id] = completed
        st.finish_count = st.finish_count + 1
        if id in st.pending:                              # the cancel came after the last CancelCheckPoint
            st.pending.remove(id)
            if completed: counters.completed_before_cancel.fetch_add(1, relaxed)
        if st.finish_count mod R == 0:                    # aging sweep
            for each (p, drained_at) in st.pending:
                if p not in st.unfinished and st.finish_count - drained_at > R: st.pending.remove(p)   # stale
        if id + 1 > finished[slot].load(relaxed):
            finished[slot].store(id + 1, release)

function registry_finished(registry, client_id):           # any thread, no lock
    i = find_slot(registry, client_id, insert = false)
    if i is none: return none
    v = finished[i].load(acquire)
    return none if v == 0 else v - 1
```

- **Max, not last.** Requests of one client finish in any order. The watermark only grows: a finish with a
  smaller id than the current value changes nothing.
- **One writer at a time.** All stores happen under the slot's lock, so a plain load-compare-store is enough
  (no compare-and-swap). Readers do not take the lock and see either the old or the new value.
- **No overflow.** `client_id <= 2^32 - 2` because `N <= 2^32 - 1`, so `id <= 2^64 - 2^32 - 1` and `id + 1` cannot
  wrap to the "none" value `0`.
- **Readers race with workers.** A reply carries whatever value the load sees. A request that finishes right
  after the load is reported by the next reply.

| Events for client 3 (ids are seqs; RequestId = `(3 << 32) \| seq`) | `finished` slot value | `registry_finished` |
|---|---|---|
| nothing yet (no slot) | — | none |
| seq 0 → `202` | 1 + id(0) | id(0) |
| seq 1 → `503` (matched `/cancel`) | 1 + id(1) | id(1) |
| seq 2 → stopped by RST | 1 + id(2) | id(2) |
| seq 3 → `400` (client id mismatch: never admitted, never run) | unchanged | id(2) |
| seq 5 → `202` on worker 1, while seq 4 still runs on worker 0 | 1 + id(5) | id(5) |
| seq 4 → `202` on worker 0, after seq 5 | unchanged: `id(4) + 1 <= id(5) + 1` | id(5) |

## CancelMatching

Runs on a worker at each CancelCheckPoint for the request `target_id` that is about to run or is running. It
matches by id only, so it works for any number of concurrent requests of a client and any admission order.

```
function resolve(st, counters, id):                       # lock held; id was just drained
    if id in st.unfinished: return keep                   # started, not finished: its own check will match
    if id in st.recent_index:                             # already finished
        if st.recent_index[id]: counters.completed_before_cancel.fetch_add(1, relaxed)
        return drop                                       # 503 or RST: nothing to count
    return keep                                           # not started yet: queued or future (or stale,
                                                          # which the aging sweep drops later)

function should_cancel(registry, slot, counters, target_id):
    if slot is none: return false
    with state[slot].lock:
        st = state[slot]
        for id in registry_drain(registry, slot):
            if id in st.pending: continue                 # a duplicate is stored once
            if resolve(st, counters, id) == keep:
                st.pending[id] = st.finish_count
        if target_id in st.pending:
            st.pending.remove(target_id)
            return true                                   # the caller counts before/during
        return false
```

Every pending id ends in exactly one of these ways:

| Way out | When | Counter |
|---|---|---|
| Its own request's CancelCheckPoint finds it | the request is started and reaches check point 1 or 2 after the drain | caller: `cancelled_before_processing` or `cancelled_during_processing` |
| Its request finishes first | `registry_publish_finished(id)` while still pending | `completed_before_cancel += 1` if `202`, else none |
| Drained after its request finished | `resolve` finds it in `recent` | `completed_before_cancel += 1` if `202`, else none |
| Aging sweep | not started, not found, and more than `R` finishes of its client since the drain | none (stale: never received, rejected, or finished too long ago) |
| Shutdown | still in an inbox or `pending` | none (not counted) |

Worked examples, client 0, ids are seqs. Before each row: `recent = [(3, completed), (5, RST)]`,
`unfinished = {6, 7}`, `pending = {}`, unless the row says otherwise.

| Inbox drained | `target_id` | Result | `pending` after | Counter change | Why |
|---|---|---|---|---|---|
| {3} | 6 | `false` | {} | `completed_before_cancel +1` | 3 finished with `202` |
| {5} | 6 | `false` | {} | none | 5 was stopped by RST |
| {7} | 6 | `false` | {7} | none | 7 is running elsewhere; its own check will match |
| {} (pending {7}) | 7 | `true` | {} | caller: before or during | its own check |
| {9} | 6 | `false` | {9} | none | 9 not started yet: queued or future |
| {6} | 6 | `true` | {} | caller | the usual case |
| {} | 0 (no slot: registry full) | `false` | — | none | `slot is none` |

Counting is lazy. An id is counted only when one of the ways out above happens. Ids still in an inbox or in
`pending` when the server shuts down are not counted.

## Out-of-order requests

A client keeps up to `n_waiting_requests` requests outstanding
([../../load_generator/client/design.md#semiopenloopclient](../../load_generator/client/design.md#semiopenloopclient)),
so the server sees several requests of one client at once. Their admission order can differ from their id order
(streams of one connection may complete in any order), and with `n_worker_threads > 1` several of them run at
the same time and finish in any order. CancelMatching never compares ids for order, so none of this matters:

| Situation | What happens | Counter |
|---|---|---|
| 10 and 11 run on two workers; `/cancel 10` is drained by 11's check | 10 is in `unfinished`, so it is kept; 10's next CancelCheckPoint returns `true` | `cancelled_during_processing +1` |
| 11 started before 10; `/cancel 10` drained by 11's check | 10 not yet started: kept; 10 starts, its check point 1 matches | `cancelled_before_processing +1` |
| `/cancel 12` arrives after 12's last check point, before it finishes `202` | `registry_publish_finished(12)` finds it pending | `completed_before_cancel +1` |
| `/cancel 13` for a request rejected with `400` (never queued) | kept until `R` more finishes of the client, then swept | none |

**The aging sweep never drops a live request's cancel.** A queued request starts after at most the client's
other outstanding requests, at most `MAX_WAITING_REQUESTS` (1000) of them, have started, and so after fewer than
`R` = 4096 finishes of that client. A request not yet sent is equally close. Only ids whose request never arrives
(a `400`, a backend error, or a cancel for an id the client never sent) age out.

In `ignore` mode `should_cancel` is never called and nothing is pushed, so `pending` stays empty; starting and
finishing still keep `unfinished`, `recent` and the watermark up to date.

## CancelCounters

```
counters: { cancelled_before_processing, cancelled_during_processing, completed_before_cancel }
          all atomic u64, starting at 0, incremented with fetch_add(1, relaxed)
```

- `cancelled_before_processing` is incremented by the Ping Handler when CancelCheckPoint 1 returns `true`.
- `cancelled_during_processing` is incremented by the Ping Handler when the burn returns `CANCELLED_ID`.
- `completed_before_cancel` is incremented inside `should_cancel` (via `resolve`) and inside
  `registry_publish_finished`, once per `/cancel` id whose request finished with `202` before a CancelCheckPoint
  saw it ([CancelMatching](#cancelmatching)).

Requests stopped by RST are never counted. `counters_snapshot` reads each value with an acquire
load after every worker has returned, so the values are final.

## handle_cancel

```
function handle_cancel(body, config, registry):
    id = protocol.parse_body(body)
    if id is ParseError: return Response(400, empty)
    (seq, client_id) = protocol.split_request_id(id)
    if config.cancel_mode == cooperative:                               # ignore: accepted, discarded
        if registry_push(registry, client_id, id) is Full: return Response(503, empty)
    reply = protocol.build_cancel_reply(registry_finished(registry, client_id))   # FinishedWatermark
    return Response(202, reply)                                         # content-type: application/json
```

`handle_cancel` runs on the Connection Handler of the connection the `/cancel` arrived on, never on a worker,
and takes no lock. It does not check that `client_id < N`, because the server does not know `N`. An id
for a client that never connects sits in its inbox and is never drained, and its replies say `null`.

The watermark is read **after** the push, so a reply never reports a value older than the push. In `ignore` mode
nothing is pushed, but the reply still carries the watermark: workers publish it in both modes.

| Mode | Body parses | Registry | Response |
|---|---|---|---|
| any | no | any | `400`, empty |
| `cooperative` | yes | full, client new | `503`, empty |
| `cooperative` | yes | has room or client known | `202`, CancelReplyBody |
| `ignore` | yes | any | `202`, CancelReplyBody (nothing pushed) |
