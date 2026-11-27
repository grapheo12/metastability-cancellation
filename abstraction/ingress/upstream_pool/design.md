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
- A client keeps up to `n_waiting_requests` requests outstanding, so a pair's connection may carry that many
  concurrent streams. The server accepts at least `MAX_WAITING_REQUESTS` per connection
  ([../../api.md#wire-contract-ping](../../api.md#wire-contract-ping)).

| Event for pair (2, 1) | `slot.conn` before | Result | `slot.conn` after |
|---|---|---|---|
| first `get`, dial succeeds | none | connection X | X |
| `get` | X | X | X |
| request on X fails with a connection error → `discard(2, 1, X)` | X | — | none |
| `get`, dial fails | none | DialError (→ `502`) | none |
| `get`, dial succeeds | none | connection Y | Y |
| late `discard(2, 1, X)` | Y | — | Y |

## Design specific to Envoy

Implementation: `implementation/envoy`: vendored Envoy (`envoy/`) and Nighthawk (`nighthawk/`) with the testbed's
changes, recorded in [VENDORED.md](../../../implementation/envoy/VENDORED.md) ([AGENTS.md](../../../AGENTS.md#vendored-code)),
built in Docker with Bazel by `implementation/envoy/build.sh` (C++ tests: `bazel test` on the targets VENDORED.md
names); Python 3 wrappers `bin/ingress.py`, `bin/loadgen.py` and `wrappers/convert.py` (tests: `cd
implementation/envoy/wrappers && python3 -B -m unittest test_convert`). Only code written or changed for the testbed
carries `Design:` tags; unchanged upstream code does not.

### Envoy choices

- UpstreamPool and Get: Envoy's HTTP/2 connection pool with `connection_pool_per_downstream_connection`, so each
  client connection gets its own upstream connection to each backend, created on first use (`bin/ingress.py`).

### Traceability

Generated from the `Design:` tags in the testbed's code under `implementation/envoy` by `implementation/spec_tools/tracegen.py` (see `AGENTS.md`). Each link points at the first code line after the tag.
Regenerate from the repository root with `python3 implementation/spec_tools/tracegen.py --lang Envoy --impl implementation/envoy --ext .cc --ext .h --comment // --ext .py:# --write`.

<!-- trace:begin -->
| Design subsection | Implementation (file:line) |
|---|---|
| [UpstreamPool](#upstreampool) | [ingress.py:140](../../../implementation/envoy/bin/ingress.py#L140) |
| [Get](#get) | [ingress.py:140](../../../implementation/envoy/bin/ingress.py#L140) |
<!-- trace:end -->
