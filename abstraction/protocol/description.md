# Protocol — Description

Leaf component, shared. It defines everything about the `/ping` and `/cancel` exchanges that the
server, the load generator and the load canceller must agree on, byte for byte:

- **RequestId**: how a 64-bit request id is built from a client id and a per-client sequence number,
  and how it is written in JSON.
- **PingRequestBody**: the exact JSON serialization of a `/ping` request body, carrying the RequestId and the
  **RequestDeadline**, padded to an exact byte length.
- **PaddedJsonBody**: the exact JSON serialization of `/ping` response bodies, padded the same way, carrying the
  server's **ExecReport** (ExecutionTime and StartSlack).
- **CancelBody**: the exact JSON serialization of a `/cancel` request body.
- **HeartbeatLine**: one line of a **HeartbeatStream**, the never-ending body a server streams on `GET /heartbeat`.
- **Status codes** and headers for `/ping`, `/cancel` and `/heartbeat`.

It contains no networking. Callers use it to build and parse bodies and to classify statuses.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
