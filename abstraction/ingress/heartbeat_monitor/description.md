# Heartbeat Monitor — Description

Leaf component. It tells the Load Balancer which backends are **live**, from the servers' Heartbeats
([../../server/heartbeat/description.md](../../server/heartbeat/description.md)):

- **HeartbeatReader**: one per backend. It keeps a `GET /heartbeat` stream open on its own connection to that
  backend and records when the last HeartbeatLine arrived. It redials when the stream ends.
- **BackendLiveness**: a backend is live while its last HeartbeatLine is less than `heartbeat_timeout_ms` old. One
  checker updates a live flag per backend on a short tick and logs every change in the **LivenessLog**.

With `heartbeat_timeout_ms = 0` (the default) it does not run, and every backend counts as live, so the Ingress
behaves as it did before heartbeats.

It exists so the Ingress stops sending new requests to a server that can no longer run them (for example one whose
WorkerFreeze has started), instead of letting them wait there until the client gives up.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
