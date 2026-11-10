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
