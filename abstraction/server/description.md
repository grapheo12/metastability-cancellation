# Server — Description

Subcomponents: Connection Handler, Worker Pool, Ping Handler, CPU Burner, Message Buffer, Cancel Tracker.

An HTTP/2 (h2c, prior knowledge) server with two endpoints, `POST /ping` and `DELETE /cancel`. It
accepts connections and gives each one its own **Connection Handler**, which runs as its own
coroutine or thread. A connection is bound to a client id by its first `/ping` (`id >> 32`).

- **`/ping`** does the work of the **ServerWorkload** named by `workload`. With `cpu_burn` (the default) it
  costs a random amount of pure CPU time (the ServiceTime, `Exp(response_rate)`). With `message_buffer` it
  creates `n_messages_per_request` messages, each a random binary tree of `n_gc_binary_tree_size` nodes, and writes
  them into a **MessageBuffer** filled at startup, to stress the garbage collector. The Connection Handler (for `cpu_burn`, after drawing the ServiceTime) puts the
  request on one process-wide, unbounded **WorkQueue**. A **WorkerPool** of `n_worker_threads` workers takes
  requests off it in arrival order and does the work. The server then answers `202` with a PaddedJsonBody of `response_bytes`, or `503` with the
  same kind of body if a `/cancel` stopped it.
- **`/cancel`** is never queued. The Connection Handler records the RequestId in the global
  **CancelRegistry** under its client id and answers `202` straight away.

How the server reacts to cancellation is set by `cancel_mode`:

- `ignore`: the server never cancels work. It burns the full ServiceTime for every request, writes
  responses that the HTTP/2 layer drops for reset streams, and accepts `/cancel` but throws the id
  away.
- `cooperative`: two cancellation sources share the same **CancelCheckPoints**: the stream's RST
  flag, and **CancelMatching** against `/cancel` ids. The worker running the request makes the checks
  before the request starts and periodically during its burn. RST stops the request with no response.
  A `/cancel` match stops it with `503`.

Three server-wide **CancelCounters** count the `/cancel` outcomes and are printed at shutdown.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
