# In-Flight Map — Design

## InFlightMap

```
entries : lock-free concurrent map from u32 to immutable reference to InFlightEntry
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
function publish(map, client_id, entry):
    require client_id < map.num_clients
    map.entries.insert_or_replace(client_id, immutable_reference(entry))

function clear(map, client_id, entry):
    require client_id < map.num_clients
    handle = map.entries.get_handle(client_id)
    if handle is none: return false
    if not same_identity(handle.value, entry): return false
    return handle.remove_exact_entry()
```

- `insert_or_replace` is one linearizable operation: after it takes effect, a lookup for `client_id` observes the
  new entry or a still newer publication, never a partially initialized entry.
- **Identity, not equality.** `same_identity` distinguishes publications even if their fields are equal. If a
  client resets request A and publishes B before A's forward finishes, A's late `clear` must not remove B.
- `remove_exact_entry` removes the entry named by `handle`, not whichever entry currently has the same key. A
  library may instead provide one atomic conditional-remove operation with the same behavior.
- Entries are immutable after publication. The backing library or its adapter keeps an entry alive while any map
  handle or snapshot copy refers to it and reclaims it afterward.

| Mapping before | Call | Result | Mapping after |
|---|---|---|---|
| no key 3 | `publish(3, A)` | — | `3 → A` |
| `3 → A` | `clear(3, A)` | true | no key 3 |
| `3 → A` | `publish(3, B)` (client reset A, sent B) | — | `3 → B` |
| `3 → B` | `clear(3, A)` (A's forward ends late) | false | `3 → B` |
| `3 → B` | `clear(3, B)` | true | no key 3 |

## Snapshot

```
function snapshot(map):
    observed = empty local map from u32 to InFlightEntry
    for handle in map.entries.iterate_weakly_consistent():
        c = handle.key
        if c < map.num_clients:
            observed[c] = copy(handle.value)
    out = list of all pairs in observed
    sort out by client_id ascending
    return out
```

- Iteration is lock-free and weakly consistent. The result may mix entries observed at different instants, omit an
  entry published during traversal, or retain a copy of an entry cleared during traversal. It contains at most one
  entry per client because `observed` replaces any duplicate observation of the same key.
- An entry in the snapshot may have been cleared by the time it is used. That race is inherent and harmless: a
  `/cancel` for a request that has just finished is counted by the server as `completed_before_cancel`
  ([../../server/cancel_tracker/design.md#cancelmatching](../../server/cancel_tracker/design.md#cancelmatching)).
- Sorting makes the order increasing by client id even when the backing map's iterator is unordered, so a uniform
  draw over the snapshot's index is reproducible given the same observed set of in-flight requests.

Worked example, `num_clients = 4`:

| Entries observed during traversal | Local `observed` | `snapshot` after sorting |
|---|---|---|
| `(3, B), (0, A)` | `{3: B, 0: A}` | `[(0, A), (3, B)]` |
| none | `{}` | `[]` |

## Design specific to Go

Implementation: `implementation/go` (module `testbed`, Go 1.21, `golang.org/x/net v0.17.0`).
Build and test with `CGO_ENABLED=0 go build ./...` / `CGO_ENABLED=0 go test ./...`. On recent macOS the dynamic
loader rejects cgo-linked Go 1.21 binaries (missing `LC_UUID`), so cgo is disabled.

### Go choices

- File `internal/ingress/inflight.go`. `slots` is `[]atomic.Pointer[InFlightEntry]`. Go atomics are sequentially consistent, which subsumes release/acquire.
- `clear` is `CompareAndSwap(e, nil)` on the pointer, so identity (not field equality) decides. Entries are freed by the garbage collector once no slot or snapshot refers to them.
- `snapshot` returns `[]Victim{ClientID, Entry}`.
- Tests: `inflight_test.go` checks the publish/clear table and snapshot order, and churns 64 clients concurrently against snapshots (run with `-race`).

### Traceability

Generated from the `// Design:` tags in the code by `implementation/spec_tools/tracegen.py` (see `AGENTS.md`). Each link points at the first code line after the tag.
Regenerate from the repository root with `python3 implementation/spec_tools/tracegen.py --lang Go --ext .go --comment // --write`.

<!-- trace:begin -->
| Design subsection | Implementation (file:line) |
|---|---|
| [InFlightMap](#inflightmap) | [inflight.go:19](../../../implementation/go/internal/ingress/inflight.go#L19), [inflight.go:32](../../../implementation/go/internal/ingress/inflight.go#L32) |
| [Publish and clear](#publish-and-clear) | [inflight.go:39](../../../implementation/go/internal/ingress/inflight.go#L39), [inflight.go:46](../../../implementation/go/internal/ingress/inflight.go#L46), [inflight_test.go:12](../../../implementation/go/internal/ingress/inflight_test.go#L12) |
| [Snapshot](#snapshot) | [inflight.go:53](../../../implementation/go/internal/ingress/inflight.go#L53), [inflight_test.go:33](../../../implementation/go/internal/ingress/inflight_test.go#L33) |
<!-- trace:end -->
