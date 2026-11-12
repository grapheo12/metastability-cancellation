# Message Buffer — Design

`W` is `window_size`. Slot indices and cursors are u32 in `0 ..= W-1`. Cursor arithmetic is done in u64, so it
cannot overflow.

## MessageTree

```
TreeNode = { value: u64, left: TreeNode | none, right: TreeNode | none }

function make_tree(rng, n):                 # exactly n nodes
    if n == 0: return none
    node = new TreeNode(value = next_u64(rng))
    k = uniform_int(rng, 0, n - 1)          # nodes in the left subtree; one or more draws, one even when n == 1
    node.left  = make_tree(rng, k)
    node.right = make_tree(rng, n - 1 - k)
    return node
```

- **Exact size.** The left subtree gets `k` nodes and the right `n - 1 - k`, so the tree has exactly `n` nodes.
- **Random shape.** `k` is uniform over `0 ..= n-1` at every node, so any shape from a path to a balanced tree can
  occur.
- **Draw order.** Pre-order: a node's value, then its left size, then its whole left subtree, then its whole right
  subtree. The same Rng state always gives the same tree. An implementation may use an explicit stack instead of
  recursion, keeping this order; `n <= 65536` bounds the depth.
- **References.** Every node is a new allocation that refers to its children, so a tracing collector visits all
  `n` nodes of every live tree, unlike Pusher's byte messages, whose contents a collector never reads. Nothing reads
  the values back; they only make each node a full object.
- `n = 1` gives a single node with no children (its left-size draw is `uniform_int(rng, 0, 0)`, which still uses
  one draw).

## MessageBuffer

```
buf = { lock: mutex, slots: array[W] of Message (tree roots), cursors: map client_id -> u32 (empty) }

function buffer_new(W, n, seed):
    rng = rng_new(derive_seed(seed, STREAM_SERVER_PREFILL, 0))
    for i in 0 ..= W-1:
        buf.slots[i] = make_tree(rng, n)            # in slot order; the window is full before the server listens
    return buf
```

- The window is filled once, before the server binds its address
  ([../design.md#listener](../design.md#listener)), so every request finds a full window and a run's first
  requests do not pay for it.
- The window is never freed while the server runs. It is the live heap the collector has to trace on every cycle.
- A collector traces the `W` slot references and then every node of every tree: `W × n` objects per cycle.

## MessageCursor

```
function push_messages(buf, client_id, msgs):
    with buf.lock:
        c = buf.cursors.get(client_id, (u64(client_id) * MESSAGE_CURSOR_STRIDE) mod W)   # first push: spread start
        for m in msgs:                                     # in list order
            buf.slots[c] = m                               # the message in that slot becomes garbage
            c = (c + 1) mod W
        buf.cursors[client_id] = c                         # next slot to overwrite
```

- The stored cursor is the slot after the last one this client overwrote.
- **Short critical section.** The lock covers only the slot writes and the cursor update, never a message
  allocation. The caller creates every message first.
- **Clients share the window.** Cursors of different clients may reach the same slots; each write simply replaces
  whatever message is there. The live set stays exactly `W` messages.
- **Order.** Pushes of different clients, and of one client's concurrent requests, are applied in the order the
  lock is acquired, which depends on timing. Within one push, slots are written in list order.
- A push of more than `W` messages wraps and overwrites its own earlier messages; that is allowed.

## Worked examples

MessageTree, `n = 4`, with these left-size draws (values `a, b, c, d` are whatever `next_u64` returns):

| Draw (in order) | Node | Value | `k` = left size | Subtree sizes (left, right) |
|---|---|---|---|---|
| 1, 2 | A (root) | a | 1 | (1, 2) |
| 3, 4 | B (A's left) | b | 0 | (0, 0) |
| 5, 6 | C (A's right) | c | 1 | (1, 0) |
| 7, 8 | D (C's left) | d | 0 | (0, 0) |

The tree is `A(left = B, right = C(left = D))`: 4 nodes, each with one value draw and one left-size draw (here
none of the `uniform_int` calls rejected). `n = 1`: one node, one value draw, one left-size draw returning `0`.

Concrete trees, `seed = 0`, `n = 4`, nodes in pre-order as (value, left size), computed with the
[sampling](../../sampling/design.md#generator) generator:

| Stream | Seed of the stream | Nodes (value, left size) | Shape |
|---|---|---|---|
| Fill, slot 0: `derive_seed(0, STREAM_SERVER_PREFILL, 0)` | 3061075853855529836 | (3347217672396778586, 2), (11281592653131264720, 0), (9616498591242348923, 0), (1901490388733148195, 0) | `A(left = B(right = C), right = D)` |
| Request client 2, seq 5, message 0: `derive_seed(derive_seed(0, STREAM_SERVER_MESSAGES, 2), STREAM_SERVER_MESSAGES, 5)` | 7161477469582606741 | (4208604537885474736, 3), (206431563091969346, 2), (2947458949422951496, 1), (15453664076641952497, 0) | a path of left children `A → B → C → D` |


`W = 10`. First slot `(client_id × 7919) mod 10`:

| `client_id` | `client_id × 7919` | First slot |
|---|---|---|
| 0 | 0 | 0 |
| 1 | 7919 | 9 |
| 3 | 23757 | 7 |

Pushes in this order (messages written as letters):

| Push | Slots written | Cursor after |
|---|---|---|
| client 1: `a, b, c` | 9 ← a, 0 ← b, 1 ← c (wraps after 9) | 2 |
| client 0: `d, e` | 0 ← d (replaces b), 1 ← e (replaces c) | 2 |
| client 1: `f` | 2 ← f (continues from its cursor 2) | 3 |
| client 3: `g, h, i, j` | 7 ← g, 8 ← h, 9 ← i (replaces a), 0 ← j (replaces d) | 1 |

`W = 1`: every push writes slot 0 repeatedly and every cursor stays 0. A push of an empty list changes nothing
except that it records the client's start slot as its cursor.

## Failure and edge cases

- The fill at startup may take noticeable time and memory: `W × n` node allocations (3 million, each holding a
  u64 and two references, with the defaults).
  If memory runs out the process fails before it listens, which the runner reports as a server that never became
  ready.
- `client_id` is the id the connection is bound to, so it is always valid when `push_messages` is called.
