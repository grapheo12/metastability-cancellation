# In-Flight Map — Design

## InFlightMap

```
entries : lock-free concurrent map from u64 (RequestId) to immutable reference to InFlightEntry
num_clients : integer

function inflight_new(num_clients):
    require 1 <= num_clients <= MAX_CLIENTS
    return InFlightMap(entries = empty lock-free concurrent map,
                       num_clients = num_clients)
```

The language-specific design chooses the backing map in this order:

1. A standard-library lock-free concurrent map, if one provides the required operations.
2. A maintained third-party lock-free concurrent map.
3. A component-specific data structure only when neither library category can satisfy the contract on the target;
   the language-specific choices must state which required operation is unavailable.

A library qualifies only when the operations used below are documented as lock-free: a caller may retry because
another caller made progress, but it never waits for a mutex or read-write lock held by another caller. A thin
adapter may provide identity handles, immutable value copies, or ordering, but it must not introduce a blocking
lock. The language-specific choices name and pin the selected library and explain any adapter or fallback.

## Publish and clear

```
function publish(map, entry):
    require entry.request_id >> 32 < map.num_clients
    map.entries.insert_or_replace(entry.request_id, immutable_reference(entry))

function clear(map, entry):
    require entry.request_id >> 32 < map.num_clients
    handle = map.entries.get_handle(entry.request_id)
    if handle is none: return false
    if not same_identity(handle.value, entry): return false
    return handle.remove_exact_entry()
```

- **One key per request.** A client's outstanding requests have distinct RequestIds, so they occupy distinct keys
  and never replace one another.
- `insert_or_replace` is one linearizable operation: after it takes effect, a lookup for the key observes the new
  entry or a still newer publication, never a partially initialized entry.
- **Identity, not equality.** `same_identity` distinguishes publications even if their fields are equal. A
  well-behaved client never reuses a RequestId, but if one did, an older forward's late `clear` must not remove the
  newer publication.
- `remove_exact_entry` removes the entry named by `handle`, not whichever entry currently has the same key. A
  library may instead provide one atomic conditional-remove operation with the same behavior.
- Entries are immutable after publication. The backing library or its adapter keeps an entry alive while any map
  handle or snapshot copy refers to it and reclaims it afterward.

| Mapping before (key: request `client.seq`) | Call | Result | Mapping after |
|---|---|---|---|
| empty | `publish(A = 3.7 @ server 0)` | — | `3.7 → A` |
| `3.7 → A` | `publish(B = 3.8 @ server 1)` (client 3's next request, A still in flight) | — | `3.7 → A`, `3.8 → B` |
| `3.7 → A`, `3.8 → B` | `clear(A)` | true | `3.8 → B` |
| `3.8 → B` | `publish(B' = 3.8 @ server 0)` (a reused id) | — | `3.8 → B'` |
| `3.8 → B'` | `clear(B)` (B's forward ends late) | false | `3.8 → B'` |
| `3.8 → B'` | `clear(B')` | true | empty |

## Snapshot

```
function snapshot(map):
    observed = empty local map from u64 to InFlightEntry
    for handle in map.entries.iterate_weakly_consistent():
        if handle.key >> 32 < map.num_clients:
            observed[handle.key] = copy(handle.value)
    out = [ (key >> 32, entry) for (key, entry) in observed ]
    sort out by entry.request_id ascending
    return out
```

- Iteration is lock-free and weakly consistent. The result may mix entries observed at different instants, omit an
  entry published during traversal, or retain a copy of an entry cleared during traversal. It contains at most one
  entry per RequestId because `observed` replaces any duplicate observation of the same key, and any number per
  client.
- An entry in the snapshot may have been cleared by the time it is used. That race is inherent and harmless: a
  `/cancel` for a request that has just finished is counted by the server as `completed_before_cancel`
  ([../../server/cancel_tracker/design.md#cancelmatching](../../server/cancel_tracker/design.md#cancelmatching)).
- Sorting makes the order increasing by RequestId (by client id, then seq) even when the backing map's iterator is
  unordered, so a uniform draw over the snapshot's index is reproducible given the same observed set of in-flight
  requests.

| In flight (client.seq @ server) | `snapshot` |
|---|---|
| 2.9 @ 0, 0.17 @ 1, 2.8 @ 1 | (0, 0.17 @ 1), (2, 2.8 @ 1), (2, 2.9 @ 0) |
| none | empty |

## Design specific to Go

Implementation: `implementation/go` (module `testbed`, Go 1.21, `golang.org/x/net v0.17.0`).
Build and test with `CGO_ENABLED=0 go build ./...` / `CGO_ENABLED=0 go test ./...`. On recent macOS the dynamic
loader rejects cgo-linked Go 1.21 binaries (missing `LC_UUID`), so cgo is disabled.

### Go choices

- File `internal/ingress/inflight.go`. Keep the Go 1.21 toolchain and use the documented component-specific fallback: an `atomic.Pointer` to an immutable state containing `map[uint32]*InFlightEntry`. `num_clients` is stored only as the key bound; construction starts with an empty map and does not allocate one slot per possible client.
- The standard-library `sync.Map` is not documented as lock-free and its Go 1.21 write paths may wait on a mutex. Maintained Go 1.21-compatible lock-free maps such as `github.com/cornelk/hashmap` v1.0.8 do not expose an atomic conditional remove or stable entry handle, while `github.com/Snawoot/lfmap` releases with transactions require Go 1.23 or newer. Those missing operations prevent identity-safe `clear` without changing the toolchain, so no third-party dependency is added.
- `publish` and `clear` load the immutable state, copy its built-in map, apply one replacement or identity-checked removal, and compare-and-swap the state pointer. A failed swap means another caller completed an update, so retrying is lock-free. Published maps are never mutated; garbage collection keeps old states and entries alive while a caller or snapshot still refers to them.
- `snapshot` reads one immutable state, builds `[]Victim{ClientID, Entry}`, and sorts it by `ClientID`. This consistent single-state view is stronger than the required weakly consistent traversal.
- Tests: `inflight_test.go` checks the publish/clear table, a concurrent publish-versus-stale-clear race, snapshot order, the maximum key-space bound without eager slot allocation, and concurrent churn against snapshots.

### Traceability

Generated from the `// Design:` tags in the code by `implementation/spec_tools/tracegen.py` (see `AGENTS.md`). Each link points at the first code line after the tag.
Regenerate from the repository root with `python3 implementation/spec_tools/tracegen.py --lang Go --ext .go --comment // --write`.

<!-- trace:begin -->
| Design subsection | Implementation (file:line) |
|---|---|
| [InFlightMap](#inflightmap) | [inflight.go:25](../../../implementation/go/internal/ingress/inflight.go#L25), [inflight.go:39](../../../implementation/go/internal/ingress/inflight.go#L39), [inflight_test.go:95](../../../implementation/go/internal/ingress/inflight_test.go#L95) |
| [Publish and clear](#publish-and-clear) | [inflight.go:51](../../../implementation/go/internal/ingress/inflight.go#L51), [inflight.go:66](../../../implementation/go/internal/ingress/inflight.go#L66), [inflight_test.go:12](../../../implementation/go/internal/ingress/inflight_test.go#L12), [inflight_test.go:33](../../../implementation/go/internal/ingress/inflight_test.go#L33) |
| [Snapshot](#snapshot) | [inflight.go:85](../../../implementation/go/internal/ingress/inflight.go#L85), [inflight_test.go:58](../../../implementation/go/internal/ingress/inflight_test.go#L58) |
<!-- trace:end -->
