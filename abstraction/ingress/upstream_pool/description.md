# Upstream Pool — Description

Leaf component. The **UpstreamPool** holds the Ingress's HTTP/2 (h2c) connections to the backends: **one connection
per (client id, server id) pair**, opened the first time that client's request is sent to that backend.

- The server binds each connection to one client and serves it serially, so keeping clients on separate upstream
  connections makes the backends behave exactly as if the client had connected directly.
- A connection is opened once per pair, even when two requests race to use it first.
- A connection that fails is dropped from the pool, so the next request for that pair opens a fresh one.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
