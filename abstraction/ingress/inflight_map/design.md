# In-Flight Map — Design

## InFlightMap

```
slots : array[num_clients] of atomic pointer to InFlightEntry, all null

function inflight_new(num_clients):
    return InFlightMap(slots = array of num_clients null pointers)
```

A fixed array indexed by client id is a complete lock-free map for keys `0 ..= num_clients - 1`: there is no
hashing, no probing and no resizing, and every operation touches one slot. Memory is one pointer per client
(`MAX_CLIENTS = 2^20` gives 8 MiB with 64-bit pointers).

## Publish and clear

```
function publish(map, client_id, entry):
    slots[client_id].store(pointer to entry, release)

function clear(map, client_id, entry):
    return slots[client_id].compare_and_swap(pointer to entry, null, acq_rel)
```

- **Identity, not equality.** `clear` compares the pointer. If a client resets a request and sends its next one
  before the first forward has finished, the second `publish` replaces the slot. The late `clear` of the first
  forward then fails and leaves the newer entry in place.
- **Release/acquire.** The `release` store makes the entry's fields visible to any reader that loads the pointer
  with `acquire`.
- Entries are never modified after publishing, so readers need no further synchronization. An entry is freed only
  when no slot and no snapshot refers to it (garbage collection or reference counting; a reader may still hold a
  snapshot copy).

| Slot before | Call | Result | Slot after |
|---|---|---|---|
| null | `publish(3, A)` | — | A |
| A | `clear(3, A)` | true | null |
| A | `publish(3, B)` (client reset A, sent B) | — | B |
| B | `clear(3, A)` (A's forward ends late) | false | B |
| B | `clear(3, B)` | true | null |

## Snapshot

```
function snapshot(map):
    out = []
    for c in 0 ..= num_clients - 1:
        p = slots[c].load(acquire)
        if p is not null: out.append((c, *p))
    return out
```

- The cost is `O(num_clients)` loads. With the default experiment sizes (a handful of clients) it is negligible
  next to a network round trip.
- Each slot is read once, so the result may mix slots read at slightly different instants. An entry in the
  snapshot may have been cleared by the time it is used. That race is inherent and harmless: a `/cancel` for a
  request that has just finished is counted by the server as `completed_before_cancel`
  ([../../server/cancel_tracker/design.md#cancelmatching](../../server/cancel_tracker/design.md#cancelmatching)).
- The order is increasing client id, so a uniform draw over the snapshot's index is reproducible given the same set
  of in-flight requests.

Worked example, `num_clients = 4`:

| Slots 0..3 | `snapshot` |
|---|---|
| A, null, null, B | `[(0, A), (3, B)]` |
| null, null, null, null | `[]` |
