# In-Flight Map — API

## Types

### `InFlightEntry`

Immutable once published.

| Field | Type | Meaning |
|---|---|---|
| `request_id` | u64 | The RequestId being forwarded (its high 32 bits are the client id) |
| `server_id` | u32 | The backend it was forwarded to (index into `backends`) |

### `InFlightMap`

`num_clients` slots, each empty or holding one InFlightEntry. Safe for any number of concurrent callers. See
[design.md#inflightmap](design.md#inflightmap).

## Functions

### `inflight_new(num_clients: integer) -> InFlightMap`
All slots empty. See [design.md#inflightmap](design.md#inflightmap).

### `publish(map, client_id: u32, entry: InFlightEntry) -> nothing`
Precondition: `client_id < num_clients`. Called by the Frontend before it forwards. Replaces any entry left in the
slot. See [design.md#publish-and-clear](design.md#publish-and-clear).

### `clear(map, client_id: u32, entry: InFlightEntry) -> boolean`
Empties the slot only if it still holds exactly `entry` (the same published record, not just equal fields). Returns
whether it did. Called by the Frontend when the forward ends. See
[design.md#publish-and-clear](design.md#publish-and-clear).

### `snapshot(map) -> list of (client_id: u32, InFlightEntry)`
Every non-empty slot, in increasing `client_id` order. Lock-free; not an atomic picture of all slots at one instant.
See [design.md#snapshot](design.md#snapshot).
