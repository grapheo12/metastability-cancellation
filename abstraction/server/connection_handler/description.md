# Connection Handler — Description

Leaf component. Owns one accepted HTTP/2 connection for its whole life. It has two concurrent
activities:

- **FrameReader** reads HTTP/2 frames. It turns each fully received request into a **StreamTask**
  and appends it to a FIFO queue. When it sees `RST_STREAM`, it sets that stream's cancel flag.
- **SerialStreamWorker** takes StreamTasks off the queue one at a time, in arrival order, routes
  them to the Ping Handler (`POST /ping`) or to `handle_cancel` (`DELETE /cancel`), and writes the
  response.

The two run in parallel so that cancellations are seen **while** the worker is burning CPU. The
worker also owns the connection's **ConnectionCancelState**: its bound client id, its last completed
RequestId, and its local pending set of `/cancel` ids.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
