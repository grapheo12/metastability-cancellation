# Message Buffer — Design

`W` is `window_size`. Slot indices and cursors are u32 in `0 ..= W-1`. Cursor arithmetic is done in u64, so it
cannot overflow.

## make_message

```
function make_message(n, size):            # n: u64
    m = new bytes of length size
    for i in 0 ..= size-1: m[i] = n mod 256
    return m
```

This is Pusher's `mkMessage`. The content only has to be written, so that the pages are really touched; nothing
reads it back. Every message is a new allocation, never reused.

## MessageBuffer

```
buf = { lock: mutex, slots: array[W] of Message, cursors: map client_id -> u32 (empty) }

function buffer_new(W, size):
    for i in 0 ..= W-1:
        buf.slots[i] = make_message(i, size)       # in slot order; the window is full before the server listens
    return buf
```

- The window is filled once, before the server binds its address
  ([../design.md#listener](../design.md#listener)), so every request finds a full window and a run's first
  requests do not pay for it.
- The window is never freed while the server runs. It is the live heap the collector has to trace on every cycle.
- Messages hold no references. A collector traces the `W` slot references but never looks inside a message.

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

- The fill at startup may take noticeable time and memory (`W × message_bytes`, about 200 MB with the defaults).
  If memory runs out the process fails before it listens, which the runner reports as a server that never became
  ready.
- `client_id` is the id the connection is bound to, so it is always valid when `push_messages` is called.

## Design specific to Go

Implementation: `implementation/go` (module `testbed`, Go 1.21, `golang.org/x/net v0.17.0`).
Build and test with `CGO_ENABLED=0 go build ./...` / `CGO_ENABLED=0 go test ./...`. On recent macOS the dynamic
loader rejects cgo-linked Go 1.21 binaries (missing `LC_UUID`), so cgo is disabled.

### Go choices

- File `internal/server/messagebuffer.go` in the one `internal/server` package (see the server's Go choices).
- A message is a `[]byte` from `make`, filled with `byte(n)` (which is `n mod 256`). `[]byte` holds no pointers,
  so Go's collector allocates it as a no-scan object and never looks inside it, as in Pusher's Go version.
- `MessageBuffer` is `{mu sync.Mutex; slots [][]byte; cursors map[uint32]uint32}`. `buffer_new` is
  `NewMessageBuffer(windowSize uint32, messageBytes int)`, and `push_messages` is `(*MessageBuffer).Push(clientID, msgs)`,
  which locks with `defer Unlock`. Cursor arithmetic is in `uint64`.
- Tests: `messagebuffer_test.go` checks `makeMessage`, the fill in slot order, the cursor worked example, and
  `W = 1`.

### Traceability

Generated from the `// Design:` tags in the code by `implementation/spec_tools/tracegen.py` (see `AGENTS.md`). Each link points at the first code line after the tag.
Regenerate from the repository root with `python3 implementation/spec_tools/tracegen.py --lang Go --ext .go --comment // --write`.

<!-- trace:begin -->
| Design subsection | Implementation (file:line) |
|---|---|
| [make_message](#make_message) | [messagebuffer.go:14](../../../implementation/go/internal/server/messagebuffer.go#L14), [messagebuffer_test.go:11](../../../implementation/go/internal/server/messagebuffer_test.go#L11) |
| [MessageBuffer](#messagebuffer) | [messagebuffer.go:27](../../../implementation/go/internal/server/messagebuffer.go#L27), [messagebuffer.go:39](../../../implementation/go/internal/server/messagebuffer.go#L39), [messagebuffer_test.go:11](../../../implementation/go/internal/server/messagebuffer_test.go#L11) |
| [MessageCursor](#messagecursor) | [messagebuffer.go:51](../../../implementation/go/internal/server/messagebuffer.go#L51), [messagebuffer_test.go:25](../../../implementation/go/internal/server/messagebuffer_test.go#L25) |
| [Worked examples](#worked-examples) | [messagebuffer_test.go:25](../../../implementation/go/internal/server/messagebuffer_test.go#L25) |
| [Failure and edge cases](#failure-and-edge-cases) | [messagebuffer.go:39](../../../implementation/go/internal/server/messagebuffer.go#L39) |
<!-- trace:end -->
