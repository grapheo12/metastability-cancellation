# HTTP/2 Cancellation Testbed — Description

## Problem

We want to measure how **client-side request cancellation** affects tail latency and wasted work in a
CPU-bound HTTP/2 service. Clients have a latency target (SLO). When a request does not come back in
time, the client gives up on it and resets the stream. What happens next depends on the server: it can
keep burning CPU on the abandoned request, or it can notice the cancellation and stop. This testbed makes that
choice a single switch and records per-request latency so the two behaviors can be compared.

## The system in one paragraph

A **Server** accepts HTTP/2 connections and starts one handler (coroutine or thread) per connection.
It exposes one endpoint, `POST /ping`. For each request it busy-loops on the CPU for a random time drawn
from an exponential distribution, then answers with a JSON body of a fixed size. A **Load Generator**
simulates `N` clients, each on its own connection. Every client has its own Poisson arrival clock
(rate `global_rate / N`) but keeps at most one request outstanding (a gated closed loop). When a
request goes past its SLO, the client cancels it. The load generator runs for a fixed time, waits
for in-flight requests to finish, and writes per-request records plus a summary.

## Goals

- Show the effect of server-side cancellation handling (`ignore` vs `cooperative`) on latency.
- Make runs reproducible where possible (seeded randomness, exact body sizes, exact id format).
- Keep the server's per-request cost a pure, controllable CPU cost.

## Non-goals

- TLS, HTTP/1.1, or any endpoint other than `/ping`.
- Server-side metrics collection (v1 measures only from the client side).
- Load shedding or admission control. The `503` status is reserved in the protocol but never sent in v1.
- Distributed load generation across machines.

## Glossary (named concepts used across all specs)

| Concept | Meaning | Defined in |
|---|---|---|
| **RequestId** | 64-bit id: high 32 bits are the per-client sequence number, low 32 bits are the client id | [protocol/design.md](protocol/design.md#requestid) |
| **PaddedJsonBody** | JSON body `{"id":..,"payload":..}` padded so its byte length is exact | [protocol/design.md](protocol/design.md#paddedjsonbody) |
| **ServiceTime** | CPU time per request, `Exp(response_rate)` seconds, in whole microseconds | [sampling/design.md](sampling/design.md#exponentialsampler) |
| **CooperativeCpuBurn** | Busy loop that checks a cancel flag at a fixed interval | [server/cpu_burner/design.md](server/cpu_burner/design.md#cooperativecpuburn) |
| **SerialStreamWorker** | Per-connection worker that runs requests one at a time in arrival order | [server/connection_handler/design.md](server/connection_handler/design.md#serialstreamworker) |
| **CancelMode** | Server switch: `ignore` or `cooperative` | [api.md](api.md#server-parameters) |
| **ClosedLoopPoissonClient** | Client with a Poisson arrival clock and at most one outstanding request | [load_generator/client/design.md](load_generator/client/design.md#closedlooppoissonclient) |
| **GatedArrival** | A scheduled arrival that must wait for the previous request to resolve | [load_generator/client/design.md](load_generator/client/design.md#gatedarrival) |
| **SLOCancellation** | Client resets a stream once `slo_ms` has passed since send | [load_generator/client/design.md](load_generator/client/design.md#slocancellation) |
| **DrainPhase** | Time after `duration_s` when no new requests are sent and in-flight ones resolve | [load_generator/run_controller/design.md](load_generator/run_controller/design.md#drainphase) |
| **RequestRecord** | One output row per scheduled arrival | [load_generator/metrics/design.md](load_generator/metrics/design.md#requestrecord) |

See [api.md](api.md) for the external interface and [design.md](design.md) for the component breakdown.
