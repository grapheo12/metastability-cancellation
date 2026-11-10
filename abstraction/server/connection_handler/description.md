# Connection Handler — Description

Leaf component. Owns one accepted HTTP/2 connection for its whole life:

- **FrameReader** reads HTTP/2 frames. It turns each fully received request into a **StreamTask** and routes it
  at once. When it sees `RST_STREAM`, it sets that stream's cancel flag (the RST flag).
- **Admission**: a `POST /ping` is admitted through the Ping Handler (parse, bind the connection to its client id,
  draw the ServiceTime) and pushed as a PingJob onto the server-wide **WorkQueue**. The handler never runs a burn
  itself.
- **Inline answers**: `DELETE /cancel` goes straight to `handle_cancel` and is answered without queuing. `400`,
  `404` and `405` are answered the same way.
- **Response writing**: when a worker hands back a `/ping` result, the handler writes it on the stream, unless the
  stream was reset.

The handler owns the connection's Rng and its **ConnectionCancelState** (the client binding). Admission happens
one request at a time, in the order the requests were fully received, so the connection's random sequence and
binding do not depend on the workers.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
