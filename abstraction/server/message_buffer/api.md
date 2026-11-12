# Message Buffer — API

## Parameters

From the server parameters ([../api.md#program-parameters](../api.md#program-parameters)); they must match it.

| Parameter | Type / unit / constraint | Default | Meaning |
|---|---|---|---|
| `window_size` (`W`) | integer, `1 ..= 2^32-1` | `200000` | Number of slots in the window |
| `message_bytes` | integer, bytes, `>= 1` | `1024` | Length of every message |
| `n_messages_per_request` | integer, `>= 1` | `50` | Messages each `/ping` creates (used by the Ping Handler) |

## Constants

| Name | Value | Meaning |
|---|---|---|
| `MESSAGE_CURSOR_STRIDE` | `7919` | Spreads the first slot of each client's MessageCursor ([design.md#messagecursor](design.md#messagecursor)) |

## Types

### `Message`

`bytes` of length `message_bytes`. See [design.md#make_message](design.md#make_message).

### `MessageBuffer`

The window, the MessageCursors and the lock that guards both. One per process, created only when
`workload = message_buffer`. Safe for concurrent callers. See [design.md#messagebuffer](design.md#messagebuffer).

## Functions

### `make_message(n: u64, message_bytes: integer >= 1) -> Message`
A new message whose every byte is `n mod 256`. Allocates; takes no lock. See
[design.md#make_message](design.md#make_message).

### `buffer_new(window_size: integer >= 1, message_bytes: integer >= 1) -> MessageBuffer`
Fills every slot, in slot order, with a new message. Called by the server's Listener before it binds. See
[design.md#messagebuffer](design.md#messagebuffer).

### `push_messages(buf, client_id: u32, msgs: list of Message) -> nothing`
Writes `msgs` in order into consecutive slots starting at the client's MessageCursor, wrapping at `W`, and moves the
cursor past them, all under the lock. The messages must already be created. Called by the Ping Handler. See
[design.md#messagecursor](design.md#messagecursor).
