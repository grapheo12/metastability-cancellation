# Upstream Pool — API

## Constants

| Name | Value | Meaning |
|---|---|---|
| `UPSTREAM_DIAL_TIMEOUT_MS` | `5000` | Longest time to open one upstream connection (TCP connect plus the HTTP/2 PING round trip) |

## Types

### `UpstreamPool`

A map from `(client_id, server_id)` to a slot that holds at most one open connection. Safe for concurrent callers.
See [design.md#upstreampool](design.md#upstreampool).

### `DialError`

A one-line reason the connection could not be opened. The Frontend answers `502`.

## Functions

### `pool_new(backends: list of host:port) -> UpstreamPool`
Empty; nothing is dialed. See [design.md#upstreampool](design.md#upstreampool).

### `get(pool, client_id: u32, server_id: u32) -> Connection | DialError`
Returns the pair's open connection, opening it first if there is none. Concurrent first calls for one pair wait on a
single dial and share its result. See [design.md#get](design.md#get).

### `discard(pool, client_id: u32, server_id: u32, conn: Connection) -> nothing`
Called when a request on `conn` fails with a connection error. Removes `conn` from the pair's slot if it is still
there, and closes it. See [design.md#get](design.md#get).

### `close_all(pool) -> nothing`
Sends GOAWAY on and closes every connection, ignoring errors. Used at shutdown. See
[design.md#upstreampool](design.md#upstreampool).

## Uses

- [../../load_generator/run_controller/design.md#startup](../../load_generator/run_controller/design.md#startup): the
  `open_h2c_connection` primitive (TCP connect, HTTP/2 prior-knowledge preface, PING round trip)
