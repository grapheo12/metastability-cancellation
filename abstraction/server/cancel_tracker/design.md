# Cancel Tracker — Design

All integer arithmetic is on unsigned 64-bit. `cap = CANCEL_REGISTRY_CAPACITY`.

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
ClientCancelState: { lock: mutual-exclusion lock, last_completed_id: u64 or none = none,
                     pending: ordered set of u64 = empty }
```

Any worker can run any client's request ([../worker_pool/design.md#workerpool](../worker_pool/design.md#workerpool)),
so the matching state lives in the client's registry slot, not in a worker or a connection. Every read or write
of `last_completed_id` and `pending`, and every `registry_drain` and `finished` store, happens with the slot's
`lock` held:

| Operation | Takes `lock` | Held for |
|---|---|---|
| `should_cancel` (CancelMatching) | yes | one drain plus one pass over `pending` |
| `registry_publish_finished` | yes | two max-updates |
| `registry_push` (`/cancel`) | no | lock-free Treiber push |
| `registry_finished` (`/cancel` reply) | no | one atomic load |
| `registry_client` | no | lock-free probe |

The lock is never held across a burn or across another lock. A worker that finds it taken waits; the wait is
bounded by one CancelMatching pass of another worker on the same client, which only happens while two requests of
that client overlap ([Overlap and ordering](#overlap-and-ordering)).

The connection's binding (`ConnectionCancelState.client_id`) is not part of this state. It belongs to the
connection and is used only when the Connection Handler admits a `/ping`
([../ping_handler/design.md#client-binding](../ping_handler/design.md#client-binding)).

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
- There is one consumer at a time: the worker holding the slot's lock. It detaches the whole list in
  one atomic `exchange`. After that the detached nodes are reachable only by that worker, which
  may free them.
- The `release` on a successful push CAS paired with the `acq_rel` exchange makes each node's `id`
  and `next` visible to the drainer.

## FinishedWatermark

The **FinishedWatermark** of a client is the highest RequestId of that client whose `/ping` a worker has
finished, with any outcome (`202`, `503` or stopped by RST). The Ping Handler publishes it when a request
finishes, and `handle_cancel` reports it in every CancelReplyBody
([../../protocol/design.md#cancelreplybody](../../protocol/design.md#cancelreplybody)).

```
function registry_publish_finished(registry, slot, id, completed):    # any worker
    if slot is none: return                               # registry full: nothing published; replies say null
    with state[slot].lock:
        if completed:
            if state[slot].last_completed_id is none or id > state[slot].last_completed_id:
                state[slot].last_completed_id = id
        if id + 1 > finished[slot].load(relaxed):
            finished[slot].store(id + 1, release)

function registry_finished(registry, client_id):           # any thread, no lock
    i = find_slot(registry, client_id, insert = false)
    if i is none: return none
    v = finished[i].load(acquire)
    return none if v == 0 else v - 1
```

- **Max, not last.** Two requests of one client can run at once on different workers and finish in either order
  ([Overlap and ordering](#overlap-and-ordering)). Both values only grow: a finish with a smaller id than the
  current value changes nothing.
- **One writer at a time.** All stores happen under the slot's lock, so a plain load-compare-store is enough
  (no compare-and-swap). Readers do not take the lock and see either the old or the new value.
- **No overflow.** `client_id <= 2^32 - 2` because `N <= 2^32 - 1`, so `id <= 2^64 - 2^32 - 1` and `id + 1` cannot
  wrap to the "none" value `0`.
- **Separate from `last_completed_id`.** CancelMatching and the CancelCounters use the `202`-only
  `last_completed_id`. The FinishedWatermark exists only for the reply.
- **Readers race with workers.** A reply carries whatever value the load sees. A request that finishes right
  after the load is reported by the next reply.

| Events for client 3 (ids are seqs; RequestId = `(3 << 32) \| seq`) | `finished` slot value | `registry_finished` | `last_completed_id` |
|---|---|---|---|
| nothing yet (no slot) | — | none | none |
| seq 0 → `202` | 1 + id(0) | id(0) | id(0) |
| seq 1 → `503` (matched `/cancel`) | 1 + id(1) | id(1) | id(0) |
| seq 2 → stopped by RST | 1 + id(2) | id(2) | id(0) |
| seq 3 → `400` (client id mismatch: never admitted, never run) | unchanged | id(2) | id(0) |
| seq 5 → `202` on worker 1, while seq 4 (reset) still burns on worker 0 | 1 + id(5) | id(5) | id(5) |
| seq 4 → `202` on worker 0, after seq 5 (`ignore` mode: its write is dropped) | unchanged: `id(4) + 1 <= id(5) + 1` | id(5) | id(5): `id(4) < id(5)` |

## CancelMatching

Runs on a worker at each CancelCheckPoint for the request `target_id` that is about to run or is
running. It relies on each client's requests starting in increasing id order
([../worker_pool/design.md#ordering](../worker_pool/design.md#ordering)).

```
function should_cancel(registry, slot, counters, target_id):
    if slot is none: return false
    with state[slot].lock:
        st = state[slot]
        for id in registry_drain(registry, slot):
            st.pending.insert(id)                 # set: a duplicate is stored once
        while st.pending is not empty and st.pending.min() <= target_id:
            id = st.pending.pop_min()
            if st.last_completed_id is not none and id <= st.last_completed_id:
                counters.completed_before_cancel.fetch_add(1, relaxed)   # already done: nothing to cancel
            else if id == target_id:
                return true                       # the caller counts before/during
            else:
                pass   # stale: an earlier request that was stopped, rejected or never received
        return false                              # ids > target_id stay pending
```

| `id` relative to the request | Action | Counter |
|---|---|---|
| `id <= last_completed_id` | Drop, do not cancel | `completed_before_cancel += 1` |
| `last_completed_id < id < target_id` | Drop | none |
| `id == target_id` | Cancel the current request | caller: before or during |
| `id > target_id` | Keep in `pending` | none (yet) |

Worked examples:

| pending before | last_completed_id | target_id | Result | pending after | Counter change |
|---|---|---|---|---|---|
| {3, 5, 9} | 3 | 5 | `true` | {9} | `completed_before_cancel +1` (for 3) |
| {4} | 2 | 6 | `false` | {} | none (4 is stale) |
| {8} | 5 | 6 | `false` | {8} | none |
| {} plus inbox {6} | 5 | 6 | `true` | {} | none |
| {} | none | 0 | `false` | {} | none (empty) |

Counting is lazy. An id is counted only when a CancelCheckPoint of the same client pops it. Ids still in an inbox
or in `pending` when the server shuts down are not counted.

## Overlap and ordering

A client keeps at most one request outstanding. It sends the next one only after the previous one was answered
(`202`, `503`) or abandoned with `RST_STREAM`. Jobs start in push order. So two requests of one client run at the
same time **only when the earlier one was reset** and is still running: in `cooperative` mode until its next RST
check (at most `cancel_check_interval_us`), in `ignore` mode until its burn ends.

During such an overlap the `target_id`s passed to `should_cancel` for that client are no longer non-decreasing:
the reset request `a` can check after the newer request `b > a` has checked. That is safe:

- `b`'s check may pop a `/cancel a` and drop it as stale (it is not `<= last_completed_id` and not `b`). Request
  `a` was reset, and RST wins over `/cancel`, so `a` would not have been counted anyway.
- `a`'s check pops only ids `<= a`. Ids between `a` and `b`, and `b` itself, stay pending for `b`.
- `last_completed_id` and the FinishedWatermark are max-updates, so a late finish of `a` never lowers them.
- In `ignore` mode `should_cancel` is never called; only the max-updates matter.

Worked example, `cooperative`, client 0, seqs as ids, two workers. `/cancel 4` and `/cancel 5` are pushed while 4
(reset by the client) is still running:

| Step | Worker | Event | pending after | last_completed_id | Result |
|---|---|---|---|---|---|
| 0 | — | state before | {} | 3 | — |
| 1 | — | client resets 4, sends 5; inbox gets 4 and 5 | {} (inbox {4, 5}) | 3 | — |
| 2 | w1 | `should_cancel(5)` at CancelCheckPoint 1 | {} | 3 | drains {4, 5}; 4 is stale (3 < 4 < 5), 5 == 5 → `true`: `503`, `cancelled_before_processing +1` |
| 3 | w0 | RST check in 4's burn | {} | 3 | `Aborted`, no counter |
| 4 | w0 | `registry_publish_finished(4, completed = false)` | {} | 3 | watermark id(5) stays (published in step 2) |

## CancelCounters

```
counters: { cancelled_before_processing, cancelled_during_processing, completed_before_cancel }
          all atomic u64, starting at 0, incremented with fetch_add(1, relaxed)
```

- `cancelled_before_processing` is incremented by the Ping Handler when CancelCheckPoint 1 returns `true`.
- `cancelled_during_processing` is incremented by the Ping Handler when the burn returns `CANCELLED_ID`.
- `completed_before_cancel` is incremented inside `should_cancel` as shown above.

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

## Design specific to Go

Implementation: `implementation/go` (module `testbed`, Go 1.21, `golang.org/x/net v0.17.0`).
Build and test with `CGO_ENABLED=0 go build ./...` / `CGO_ENABLED=0 go test ./...`. On recent macOS the dynamic
loader rejects cgo-linked Go 1.21 binaries (missing `LC_UUID`), so cgo is disabled.

### Go choices

- File `internal/server/canceltracker.go`.
- `keys` is `[]atomic.Uint64` and `heads` is `[]atomic.Pointer[inboxNode]`. Go atomics are sequentially consistent, which is stronger than the acquire/release the design requires.
- The garbage collector frees drained nodes, so there is no explicit `free`. There is still no ABA problem, because nodes are never reused while reachable.
- `finished` is `[]atomic.Uint64` in the registry. `registry_publish_finished` is `(*CancelRegistry).PublishFinished(st, id)` and `registry_finished` is `(*CancelRegistry).Finished(clientID) (id, ok)`, where `ok == false` is none. `handle_cancel` builds its reply with `protocol.BuildCancelReply`.
- `pending` is a `container/heap` min-heap plus a membership `map` for set semantics. `last_completed_id: none` is the flag `hasLastCompleted`. `slot: none` is `-1`.
- Tests: `canceltracker_test.go` checks the worked examples from the CancelMatching table, concurrent push then drain, a full registry, `HandleCancel` (reply bodies in both modes), and the FinishedWatermark worked example, including id `0` and a full registry. `server_test.go` runs end-to-end tests for before, during and RST.

### Traceability

Generated from the `// Design:` tags in the code by `implementation/spec_tools/tracegen.py` (see `AGENTS.md`). Each link points at the first code line after the tag.
Regenerate from the repository root with `python3 implementation/spec_tools/tracegen.py --lang Go --ext .go --comment // --write`.

<!-- trace:begin -->
| Design subsection | Implementation (file:line) |
|---|---|
| [CancelRegistry](#cancelregistry) | [canceltracker.go:16](../../../implementation/go/internal/server/canceltracker.go#L16), [canceltracker.go:35](../../../implementation/go/internal/server/canceltracker.go#L35), [canceltracker.go:45](../../../implementation/go/internal/server/canceltracker.go#L45), [canceltracker.go:61](../../../implementation/go/internal/server/canceltracker.go#L61), [canceltracker_test.go:100](../../../implementation/go/internal/server/canceltracker_test.go#L100) |
| [CancelInbox](#cancelinbox) | [canceltracker.go:24](../../../implementation/go/internal/server/canceltracker.go#L24), [canceltracker.go:88](../../../implementation/go/internal/server/canceltracker.go#L88), [canceltracker.go:108](../../../implementation/go/internal/server/canceltracker.go#L108), [canceltracker_test.go:77](../../../implementation/go/internal/server/canceltracker_test.go#L77) |
| [FinishedWatermark](#finishedwatermark) | [canceltracker.go:128](../../../implementation/go/internal/server/canceltracker.go#L128), [canceltracker.go:142](../../../implementation/go/internal/server/canceltracker.go#L142), [canceltracker.go:263](../../../implementation/go/internal/server/canceltracker.go#L263), [canceltracker_test.go:142](../../../implementation/go/internal/server/canceltracker_test.go#L142), [canceltracker_test.go:175](../../../implementation/go/internal/server/canceltracker_test.go#L175) |
| [CancelMatching](#cancelmatching) | [canceltracker.go:190](../../../implementation/go/internal/server/canceltracker.go#L190), [canceltracker.go:228](../../../implementation/go/internal/server/canceltracker.go#L228), [canceltracker_test.go:28](../../../implementation/go/internal/server/canceltracker_test.go#L28), [canceltracker_test.go:68](../../../implementation/go/internal/server/canceltracker_test.go#L68) |
| [CancelCounters](#cancelcounters) | [canceltracker.go:157](../../../implementation/go/internal/server/canceltracker.go#L157), [canceltracker.go:166](../../../implementation/go/internal/server/canceltracker.go#L166) |
| [handle_cancel](#handle_cancel) | [canceltracker.go:251](../../../implementation/go/internal/server/canceltracker.go#L251), [canceltracker_test.go:119](../../../implementation/go/internal/server/canceltracker_test.go#L119) |
<!-- trace:end -->
