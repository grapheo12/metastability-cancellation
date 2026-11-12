# Message Buffer — Description

Leaf component. The **MessageBuffer** is the shared state of the `message_buffer` **ServerWorkload**, a
garbage-collector stress test modeled on the Pusher GC-latency benchmark
(<https://github.com/WillSewell/gc-latency-experiment>):

- A window of `window_size` messages of `message_bytes` bytes each, filled when the server starts, before it
  listens. It stays allocated for the life of the process, so it is a large live heap.
- Each `/ping` creates `n_messages_per_request` new messages and overwrites as many window slots, so the
  messages they replace become garbage after a medium lifetime.
- Each client has its own **MessageCursor**: the next slot its messages overwrite. Cursors of different clients
  start at different slots.
- The window is shared by every worker, behind one lock. Messages are created before the lock is taken, so the
  lock only guards the slot writes.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
