# Message Buffer — API

## Parameters

From the server parameters ([../api.md#program-parameters](../api.md#program-parameters)); they must match it.

| Parameter | Type / unit / constraint | Default | Meaning |
|---|---|---|---|
| `window_size` (`W`) | integer, `1 ..= 2^32-1` | `200000` | Number of slots in the window |
| `n_gc_binary_tree_size` (`n`) | integer, `1 ..= 65536` | `15` | Nodes in every MessageTree |
| `n_messages_per_request` | integer, `>= 1` | `50` | Messages each `/ping` creates (used by the Ping Handler) |

## Constants

| Name | Value | Meaning |
|---|---|---|
| `MESSAGE_CURSOR_STRIDE` | `7919` | Spreads the first slot of each client's MessageCursor ([design.md#messagecursor](design.md#messagecursor)) |

## Types

### `TreeNode`

| Field | Type | Meaning |
|---|---|---|
| `value` | u64 | A random value |
| `left`, `right` | TreeNode or none | The children |

### `Message`

A **MessageTree**: the root `TreeNode` of a tree of exactly `n_gc_binary_tree_size` nodes. See
[design.md#messagetree](design.md#messagetree).

### `MessageBuffer`

The window, the MessageCursors and the lock that guards both. One per process, created only when
`workload = message_buffer`. Safe for concurrent callers. See [design.md#messagebuffer](design.md#messagebuffer).

## Functions

### `make_tree(rng: Rng, n: integer >= 0) -> TreeNode | none`
A new random tree of exactly `n` nodes (`none` for `0`), drawn from `rng` in a fixed order. Allocates; takes no
lock. See [design.md#messagetree](design.md#messagetree).

### `buffer_new(window_size: integer >= 1, n_gc_binary_tree_size: integer >= 1, seed: u64) -> MessageBuffer`
Fills every slot, in slot order, with a new MessageTree from the fill stream
`rng_new(derive_seed(seed, STREAM_SERVER_PREFILL, 0))`. Called by the server's Listener before it binds. See
[design.md#messagebuffer](design.md#messagebuffer).

### `push_messages(buf, client_id: u32, msgs: list of Message) -> nothing`
Writes `msgs` in order into consecutive slots starting at the client's MessageCursor, wrapping at `W`, and moves the
cursor past them, all under the lock. The messages must already be created. Called by the Ping Handler. See
[design.md#messagecursor](design.md#messagecursor).

## Uses

- [../../sampling/api.md](../../sampling/api.md): `Rng`, `rng_new`, `derive_seed`, `next_u64`, `uniform_int`, `STREAM_SERVER_PREFILL`
