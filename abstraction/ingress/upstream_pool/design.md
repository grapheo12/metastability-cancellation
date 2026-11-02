# Upstream Pool — Design

## UpstreamPool

```
pool = { backends, slots: map (client_id, server_id) -> Slot, map_lock: mutex }
Slot = { lock: mutex, conn: Connection or none }

function slot_for(pool, c, s):                       # find or create the pair's Slot
    with pool.map_lock:
        if (c, s) not in pool.slots: pool.slots[(c, s)] = Slot(conn = none)
        return pool.slots[(c, s)]

function close_all(pool):
    with pool.map_lock: slots = all Slots
    for slot in slots:
        with slot.lock:
            if slot.conn is not none: send GOAWAY and close slot.conn (errors ignored); slot.conn = none
```

`map_lock` is held only to look up or insert a Slot, never during a dial or a request. Slots are never removed, so
the map has at most `num_clients × B` entries, created as pairs are first used.

## Get

```
function get(pool, c, s):
    slot = slot_for(pool, c, s)
    with slot.lock:                                  # serializes the first dial for this pair
        if slot.conn is not none: return slot.conn
        conn = open_h2c_connection(pool.backends[s], timeout = UPSTREAM_DIAL_TIMEOUT_MS)
        if conn failed: return DialError(reason)     # not cached: the next get dials again
        slot.conn = conn
        return conn

function discard(pool, c, s, conn):
    slot = slot_for(pool, c, s)
    with slot.lock:
        if slot.conn is conn: slot.conn = none        # identity: a newer connection is kept
    close conn (errors ignored)
```

- **One dial per pair.** The slot lock is held during the dial, so a second `get` for the same pair waits and then
  reuses the connection. Different pairs dial in parallel.
- **Lock held only briefly afterwards.** Once `conn` exists, `get` takes `slot.lock` only to read it. Requests run
  on the returned connection without any pool lock.
- **What counts as a connection error.** The HTTP/2 layer reports that the whole connection is unusable (it
  received GOAWAY, the TCP connection closed, or a protocol error). A stream reset or a stream error alone does not
  discard the connection.
- A client keeps at most one request outstanding, so in practice a pair's connection carries one stream at a time.

| Event for pair (2, 1) | `slot.conn` before | Result | `slot.conn` after |
|---|---|---|---|
| first `get`, dial succeeds | none | connection X | X |
| `get` | X | X | X |
| request on X fails with a connection error → `discard(2, 1, X)` | X | — | none |
| `get`, dial fails | none | DialError (→ `502`) | none |
| `get`, dial succeeds | none | connection Y | Y |
| late `discard(2, 1, X)` | Y | — | Y |
