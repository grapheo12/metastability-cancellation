# Connection Handler — Description

Leaf component. Owns one accepted HTTP/2 connection for its whole life. It has two concurrent
activities:

- **FrameReader** reads HTTP/2 frames. It turns each fully received request into a **StreamTask**
  and appends it to a FIFO queue. When it sees `RST_STREAM`, it sets that stream's cancel flag.
- **SerialStreamWorker** takes StreamTasks off the queue one at a time, in arrival order, routes
  them, runs the Ping Handler, and writes the response.

The two run in parallel so that cancellations are seen **while** the worker is burning CPU.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
