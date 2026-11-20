# Heartbeat — Description

Leaf component. It answers `GET /heartbeat` with a **HeartbeatStream**: one **HeartbeatLine** every
`heartbeat_interval_ms`, on the stream's own coroutine, for as long as the stream stays open
([../../protocol/design.md#heartbeatline](../../protocol/design.md#heartbeatline)). This is the server's
**Heartbeat**: the Ingress's Heartbeat Monitor reads it to tell live backends from dead ones.

- **HeartbeatSender**: the loop that writes the lines. Before each line it passes the WorkQueue's `freeze_wait`, so
  a server whose WorkerFreeze has started stops beating: the heartbeat says that the server can still run work, not
  only that its process is up.
- A heartbeat never goes through the WorkQueue and never waits for a worker, so a server that is merely busy (a long
  queue) keeps beating on time.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
