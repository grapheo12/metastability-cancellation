# Protocol — Description

Shared leaf component. It defines everything about the `/ping` exchange that both the server and the
load generator must agree on, byte for byte:

- **RequestId**: how a 64-bit request id is built from a client id and a per-client sequence number,
  and how it is written in JSON.
- **PaddedJsonBody**: the exact JSON serialization of request and response bodies, padded to an
  exact byte length.
- **Status codes** and headers for `/ping`.

It contains no networking. Callers use it to build and parse bodies and to classify statuses.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
