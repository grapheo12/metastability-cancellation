# In-Flight Map — API

## Types

### `InFlightEntry`

Immutable once published.

| Field | Type | Meaning |
|---|---|---|
| `request_id` | u64 | The RequestId being forwarded (its high 32 bits are the client id) |
| `server_id` | u32 | The backend it was forwarded to (index into `backends`) |

### `InFlightMap`

A lock-free map from RequestId to the InFlightEntry of that request's forward. Valid keys are RequestIds whose
client id (`request_id >> 32`) is below `num_clients`; a client may have any number of keys at once. Safe for any
number of concurrent callers. The representation need not allocate one slot per possible key. See
[design.md#inflightmap](design.md#inflightmap).

## Functions

### `inflight_new(num_clients: integer) -> InFlightMap`
The map is empty. `num_clients` sets the exclusive upper bound for the client ids of keys and may be used as a
capacity hint; construction need not allocate storage for every client. See [design.md#inflightmap](design.md#inflightmap).

### `publish(map, entry: InFlightEntry) -> nothing`
Precondition: `entry.request_id >> 32 < num_clients`. Called by the Frontend before it forwards. The key is
`entry.request_id`; any entry already mapped to it is replaced. See [design.md#publish-and-clear](design.md#publish-and-clear).

### `clear(map, entry: InFlightEntry) -> boolean`
Removes the mapping of `entry.request_id` only if it still holds exactly `entry` (the same published record, not just equal fields).
Returns whether it did. Called by the Frontend when the forward ends. See
[design.md#publish-and-clear](design.md#publish-and-clear).

### `snapshot(map) -> list of (client_id: u32, InFlightEntry)`
One pair per observed in-flight request (`client_id = entry.request_id >> 32`), any number per client, in
increasing `request_id` order (so by client, then by seq). Lock-free; not an atomic picture of the map
at one instant, so it may include an entry concurrently cleared or omit one concurrently published. See
[design.md#snapshot](design.md#snapshot).
