# In-Flight Map — API

## Types

### `InFlightEntry`

Immutable once published.

| Field | Type | Meaning |
|---|---|---|
| `request_id` | u64 | The RequestId being forwarded (its high 32 bits are the client id) |
| `server_id` | u32 | The backend it was forwarded to (index into `backends`) |

### `InFlightMap`

A lock-free map whose valid keys are `0 ..= num_clients - 1`, with at most one InFlightEntry per key. Safe for any
number of concurrent callers. The representation need not allocate one slot per possible key. See
[design.md#inflightmap](design.md#inflightmap).

## Functions

### `inflight_new(num_clients: integer) -> InFlightMap`
The map is empty. `num_clients` sets the exclusive upper bound for client ids and may be used as a capacity hint;
construction need not allocate storage for every client. See [design.md#inflightmap](design.md#inflightmap).

### `publish(map, client_id: u32, entry: InFlightEntry) -> nothing`
Precondition: `client_id < num_clients`. Called by the Frontend before it forwards. Replaces any entry already
mapped to the client id. See [design.md#publish-and-clear](design.md#publish-and-clear).

### `clear(map, client_id: u32, entry: InFlightEntry) -> boolean`
Removes the mapping only if it still holds exactly `entry` (the same published record, not just equal fields).
Returns whether it did. Called by the Frontend when the forward ends. See
[design.md#publish-and-clear](design.md#publish-and-clear).

### `snapshot(map) -> list of (client_id: u32, InFlightEntry)`
At most one observed entry per client, in increasing `client_id` order. Lock-free; not an atomic picture of the map
at one instant, so it may include an entry concurrently cleared or omit one concurrently published. See
[design.md#snapshot](design.md#snapshot).
