# Cancel Tracker — Design

All integer arithmetic is on unsigned 64-bit. `cap = CANCEL_REGISTRY_CAPACITY`.

## CancelRegistry

A fixed-capacity open-addressing hash table with linear probing. It has no locks and no resizing,
and slots are never deleted.

```
keys  : array[cap] of atomic u64, all EMPTY_KEY (0)     # stored key = client_id + 1
heads : array[cap] of atomic pointer to Node, all null  # CancelInbox per slot
Node  : { id: u64, next: pointer to Node }              # immutable once published

function registry_new(capacity):
    assert capacity is a power of two
    return CancelRegistry(keys, heads)

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
```

`mix64` is from [../../sampling/design.md#splitmix64](../../sampling/design.md#splitmix64). Because
slots are never deleted, a probe that reaches an `EMPTY_KEY` proves the key is absent.

## CancelInbox

A Treiber stack per slot with multi-producer push and a single-consumer "take all".

```
function registry_push(registry, client_id, id):
    i = find_slot(registry, client_id, insert = true)
    if i is Full: return Full
    node = new Node(id, next = null)
    loop:
        h = heads[i].load(acquire)
        node.next = h
        if heads[i].compare_and_swap(h, node, release) succeeds: return ok

function registry_drain(registry, state):
    if state.slot is none:
        state.slot = find_slot(registry, state.client_id, insert = false)
        if state.slot is none: return []
    h = heads[state.slot].exchange(null, acq_rel)
    ids = []
    while h != null:
        ids.append(h.id)
        n = h.next
        free(h)
        h = n
    return ids
```

Why this is safe without locks:
- Pushers never dereference any node except the one they create. They only copy the head pointer
  into `node.next`. So there is no ABA problem and no use-after-free.
- The only consumer is the single worker bound to `client_id`, and it detaches the whole list in
  one atomic `exchange`. After that the detached nodes are reachable only by that worker, which
  may free them.
- The `release` on a successful push CAS paired with the `acq_rel` exchange makes each node's `id`
  and `next` visible to the drainer.

## CancelMatching

Runs on the worker at each CancelCheckPoint for the request `target_id` that is about to run or is
running. It relies on the ids of one client reaching its worker in increasing order
([../connection_handler/design.md#serialstreamworker](../connection_handler/design.md#serialstreamworker)).

```
function should_cancel(state, registry, counters, target_id):
    if state.client_id is unbound: return false
    for id in registry_drain(registry, state):
        state.pending.insert(id)                 # set: a duplicate is stored once
    while state.pending is not empty and state.pending.min() <= target_id:
        id = state.pending.pop_min()
        if state.last_completed_id is not none and id <= state.last_completed_id:
            counters.completed_before_cancel.fetch_add(1, relaxed)   # already done: nothing to cancel
        else if id == target_id:
            return true                           # the caller counts before/during
        else:
            pass   # stale: an earlier request that was stopped, rejected or never received
    return false                                  # ids > target_id stay pending
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

Counting is lazy. An id is counted only when a CancelCheckPoint pops it. Ids still in an inbox
or in `pending` when the server shuts down are not counted.

## CancelCounters

```
counters: { cancelled_before_processing, cancelled_during_processing, completed_before_cancel }
          all atomic u64, starting at 0, incremented with fetch_add(1, relaxed)
```

- `cancelled_before_processing` is incremented by the Ping Handler when CancelCheckPoint 1 returns `true`.
- `cancelled_during_processing` is incremented by the Ping Handler when the burn returns `CANCELLED_ID`.
- `completed_before_cancel` is incremented inside `should_cancel` as shown above.

Requests stopped by RST are never counted. `counters_snapshot` reads each value with an acquire
load after every Connection Handler has returned, so the values are final.

## handle_cancel

```
function handle_cancel(body, config, registry):
    id = protocol.parse_body(body)
    if id is ParseError: return Response(400, empty)
    if config.cancel_mode == ignore: return Response(202, empty)       # accepted, discarded
    (seq, client_id) = protocol.split_request_id(id)
    if registry_push(registry, client_id, id) is Full: return Response(503, empty)
    return Response(202, empty)
```

`handle_cancel` does not check that `client_id < N`, because the server does not know `N`. An id
for a client that never connects sits in its inbox and is never drained.

## Design specific to Go

Implementation: `implementation/go` (module `testbed`, Go 1.21, `golang.org/x/net v0.17.0`).
Build and test with `CGO_ENABLED=0 go build ./...` / `CGO_ENABLED=0 go test ./...`. On recent macOS the dynamic
loader rejects cgo-linked Go 1.21 binaries (missing `LC_UUID`), so cgo is disabled.

### Go choices

- File `internal/server/canceltracker.go`.
- `keys` is `[]atomic.Uint64` and `heads` is `[]atomic.Pointer[inboxNode]`. Go atomics are sequentially consistent, which is stronger than the acquire/release the design requires.
- The garbage collector frees drained nodes, so there is no explicit `free`. There is still no ABA problem, because nodes are never reused while reachable.
- `pending` is a `container/heap` min-heap plus a membership `map` for set semantics. `last_completed_id: none` is the flag `hasLastCompleted`. `slot: none` is `-1`.
- Tests: `canceltracker_test.go` checks the worked examples from the CancelMatching table, concurrent push then drain, a full registry, and `HandleCancel`. `server_test.go` runs end-to-end tests for before, during and RST.

### Traceability

Generated from the `// Design:` tags in the code. Each link points at the first code line after the tag.

<!-- trace:begin -->
| Design subsection | Implementation (file:line) |
|---|---|
| [CancelRegistry](#cancelregistry) | [canceltracker.go:16](../../../implementation/go/internal/server/canceltracker.go#L16), [canceltracker.go:35](../../../implementation/go/internal/server/canceltracker.go#L35), [canceltracker.go:44](../../../implementation/go/internal/server/canceltracker.go#L44), [canceltracker.go:59](../../../implementation/go/internal/server/canceltracker.go#L59), [canceltracker_test.go:100](../../../implementation/go/internal/server/canceltracker_test.go#L100) |
| [CancelInbox](#cancelinbox) | [canceltracker.go:24](../../../implementation/go/internal/server/canceltracker.go#L24), [canceltracker.go:86](../../../implementation/go/internal/server/canceltracker.go#L86), [canceltracker.go:106](../../../implementation/go/internal/server/canceltracker.go#L106), [canceltracker_test.go:77](../../../implementation/go/internal/server/canceltracker_test.go#L77) |
| [CancelMatching](#cancelmatching) | [canceltracker.go:157](../../../implementation/go/internal/server/canceltracker.go#L157), [canceltracker.go:195](../../../implementation/go/internal/server/canceltracker.go#L195), [canceltracker_test.go:28](../../../implementation/go/internal/server/canceltracker_test.go#L28), [canceltracker_test.go:68](../../../implementation/go/internal/server/canceltracker_test.go#L68) |
| [CancelCounters](#cancelcounters) | [canceltracker.go:124](../../../implementation/go/internal/server/canceltracker.go#L124), [canceltracker.go:133](../../../implementation/go/internal/server/canceltracker.go#L133) |
| [handle_cancel](#handle_cancel) | [canceltracker.go:217](../../../implementation/go/internal/server/canceltracker.go#L217), [canceltracker_test.go:119](../../../implementation/go/internal/server/canceltracker_test.go#L119) |
<!-- trace:end -->
