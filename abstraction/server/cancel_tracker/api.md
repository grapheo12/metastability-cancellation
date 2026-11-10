# Cancel Tracker — API

## Constants

| Name | Value | Meaning |
|---|---|---|
| `CANCEL_REGISTRY_CAPACITY` | `65536` | Slot count of the CancelRegistry (same as [../api.md#constants](../api.md#constants)) |
| `EMPTY_KEY` | `0` | Marks an unused registry slot. Stored keys are `client_id + 1` |
| `RECENT_FINISHED` | `4096` | Per client: how many finished requests CancelMatching remembers, and how many finishes a never-matched `/cancel` id is kept for |

The first two are used by [design.md#cancelregistry](design.md#cancelregistry), the third by
[design.md#clientcancelstate](design.md#clientcancelstate).

## Types

### `CancelRegistry`
Process-wide. It is safe for any number of concurrent callers of every function below: inbox pushes and
watermark reads are lock-free, and everything that reads or changes a client's ClientCancelState takes that
client's lock. See [design.md#cancelregistry](design.md#cancelregistry).

### `ClientSlot`
A handle to one client's registry slot (its index), or `none` when the registry is full and the client has no
slot. Returned by `registry_client`. See [design.md#cancelregistry](design.md#cancelregistry).

### `ClientCancelState`

One per registry slot, shared by every worker and connection that handles a request of that client. Every field
is read and written only with `lock` held.

| Field | Type | Initial | Meaning |
|---|---|---|---|
| `lock` | mutual-exclusion lock | unlocked | Serializes starts, CancelMatching passes and finishes of this client |
| `pending` | map u64 → u64 | empty | Drained `/cancel` ids not yet resolved, each with `finish_count` at its drain |
| `unfinished` | set of u64 | empty | Ids a worker has started and not yet finished |
| `recent` | ring of (u64, boolean), capacity `RECENT_FINISHED` | empty | The last finished ids and whether each completed (`202`) |
| `recent_index` | map u64 → boolean | empty | The ids now in `recent` |
| `finish_count` | u64 | `0` | Requests of this client finished so far |

See [design.md#clientcancelstate](design.md#clientcancelstate).

### `CancelCounters`

| Field | Type | Meaning |
|---|---|---|
| `cancelled_before_processing` | atomic u64 | Requests stopped by `/cancel` at the CancelCheckPoint before their burn |
| `cancelled_during_processing` | atomic u64 | Requests stopped by `/cancel` at a CancelCheckPoint during their burn |
| `completed_before_cancel` | atomic u64 | `/cancel` ids found at a CancelCheckPoint to target a request that had already completed |

See [design.md#cancelcounters](design.md#cancelcounters).

### `ConnectionCancelState`

The cancel-related state of one connection: only its client binding. Owned by one Connection Handler and touched
only when it admits a `/ping`, which it does one request at a time, so it needs no further synchronization.

| Field | Type | Initial | Meaning |
|---|---|---|---|
| `client_id` | u32 or `unbound` | `unbound` | Set by the connection's first valid `/ping` |

See [design.md#clientcancelstate](design.md#clientcancelstate).

## Functions

### `registry_new(capacity: integer) -> CancelRegistry`
Precondition: `capacity` is a power of two. See [design.md#cancelregistry](design.md#cancelregistry).

### `registry_client(registry, client_id: u32) -> ClientSlot`
Finds the client's slot, creating it if needed. Returns `none` if the client is new and no slot is free.
Lock-free. Called by a worker before it runs a request. See [design.md#cancelregistry](design.md#cancelregistry).

### `registry_push(registry, client_id: u32, id: u64) -> ok | Full`
Adds `id` to the client's inbox, creating the entry if needed. Returns `Full` if the client is new
and no slot is free. Lock-free, never takes a client's lock. See [design.md#cancelinbox](design.md#cancelinbox).

### `registry_start(registry, slot: ClientSlot, id: u64) -> nothing`
Records `id` as started and not finished, under the slot's lock. Called by the Ping Handler when a worker starts
the request. Does nothing for slot `none`. See [design.md#clientcancelstate](design.md#clientcancelstate).

### `registry_drain(registry, slot: ClientSlot) -> list of u64`
Removes and returns every id in the slot's inbox, in no particular order. The caller holds the slot's lock. Used
only inside `should_cancel`. See [design.md#cancelinbox](design.md#cancelinbox).

### `registry_publish_finished(registry, slot: ClientSlot, id: u64, completed: boolean, counters) -> nothing`
Called by a worker when a request of that client finishes with any outcome; `completed` is `true` for `202`.
Under the slot's lock it moves `id` from `unfinished` into `recent`, resolves a pending `/cancel` for `id` (counting
`completed_before_cancel` if `completed`), ages out stale pending ids, and raises the client's FinishedWatermark to
at least `id`. Does nothing for slot `none`. See
[design.md#finishedwatermark](design.md#finishedwatermark).

### `registry_finished(registry, client_id: u32) -> u64 | none`
The client's FinishedWatermark, or none if the client has no slot or has finished nothing. Lock-free, callable
from any thread. See [design.md#finishedwatermark](design.md#finishedwatermark).

### `should_cancel(registry, slot: ClientSlot, counters, target_id: u64) -> boolean`
Runs CancelMatching for the request `target_id` under the slot's lock. Returns `true` if a `/cancel` for exactly
`target_id` is pending, and `false` for slot `none`. Makes no assumption about the order of a client's requests.
It may increment `completed_before_cancel`. See
[design.md#cancelmatching](design.md#cancelmatching).

### `handle_cancel(body: bytes, config: ServerConfig, registry) -> Response`
Handles a routed `DELETE /cancel`. Responses: `202` with a CancelReplyBody holding the target client's
FinishedWatermark, or `400` / `503` with an empty body. Never takes a client's lock. See
[design.md#handle_cancel](design.md#handle_cancel).

### `counters_new() -> CancelCounters`
All fields start at 0. See [design.md#cancelcounters](design.md#cancelcounters).

### `counters_snapshot(counters) -> (u64, u64, u64)`
Reads the three values in the order of the CancelCounters table. Used for the shutdown line. See
[design.md#cancelcounters](design.md#cancelcounters).

## Uses

- [../../protocol/api.md](../../protocol/api.md): `parse_body`, `split_request_id`, `build_cancel_reply`, status constants
- [../../sampling/api.md](../../sampling/api.md): `mix64` for slot hashing
