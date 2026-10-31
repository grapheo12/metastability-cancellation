# Cancel Tracker — API

## Constants

| Name | Value | Meaning |
|---|---|---|
| `CANCEL_REGISTRY_CAPACITY` | `65536` | Slot count of the CancelRegistry (same as [../api.md#constants](../api.md#constants)) |
| `EMPTY_KEY` | `0` | Marks an unused registry slot. Stored keys are `client_id + 1` |

## Types

### `CancelRegistry`
Process-wide. It is safe for any number of concurrent `registry_push` calls together with one
`registry_drain` caller per client id. See [design.md#cancelregistry](design.md#cancelregistry).

### `CancelCounters`

| Field | Type | Meaning |
|---|---|---|
| `cancelled_before_processing` | atomic u64 | Requests stopped by `/cancel` at the CancelCheckPoint before their burn |
| `cancelled_during_processing` | atomic u64 | Requests stopped by `/cancel` at a CancelCheckPoint during their burn |
| `completed_before_cancel` | atomic u64 | `/cancel` ids found at a CancelCheckPoint to target a request that had already completed |

See [design.md#cancelcounters](design.md#cancelcounters).

### `ConnectionCancelState`

Owned by one Connection Handler's worker only, so it needs no synchronization.

| Field | Type | Initial | Meaning |
|---|---|---|---|
| `client_id` | u32 or `unbound` | `unbound` | Set by the connection's first valid `/ping` |
| `slot` | registry slot or `none` | `none` | This client's registry slot, cached once found |
| `last_completed_id` | u64 or `none` | `none` | Id of the last request whose burn finished (`202`) |
| `pending` | ordered set of u64 | empty | The local set: drained ids not yet resolved |

## Functions

### `registry_new(capacity: integer) -> CancelRegistry`
Precondition: `capacity` is a power of two. See [design.md#cancelregistry](design.md#cancelregistry).

### `registry_push(registry, client_id: u32, id: u64) -> ok | Full`
Adds `id` to the client's inbox, creating the entry if needed. Returns `Full` if the client is new
and no slot is free. Lock-free. See [design.md#cancelinbox](design.md#cancelinbox).

### `registry_drain(registry, state: ConnectionCancelState) -> list of u64`
Removes and returns every id in `state.client_id`'s inbox, in no particular order, and caches the
slot. Returns an empty list if the client has no entry yet. See [design.md#cancelinbox](design.md#cancelinbox).

### `should_cancel(state, registry, counters, target_id: u64) -> boolean`
Runs CancelMatching for the request `target_id`. Returns `true` if a `/cancel` targets it. It may
increment `completed_before_cancel`. See [design.md#cancelmatching](design.md#cancelmatching).

### `handle_cancel(body: bytes, config: ServerConfig, registry) -> Response`
Handles a routed `DELETE /cancel`. Responses: `202`, `400` or `503`, always with an empty body. See
[design.md#handle_cancel](design.md#handle_cancel).

### `counters_new() -> CancelCounters`
All fields start at 0.

### `counters_snapshot(counters) -> (u64, u64, u64)`
Reads the three values in the order of the CancelCounters table. Used for the shutdown line.

## Uses

- [../../protocol/api.md](../../protocol/api.md): `parse_body`, `split_request_id`, status constants
- [../../sampling/api.md](../../sampling/api.md): `mix64` for slot hashing
