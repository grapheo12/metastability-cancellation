# HTTP/2 Cancellation Testbed — Design

## Components

```mermaid
flowchart LR
  subgraph LG[Load Generator]
    RC[Run Controller] -->|spawns N| C[Client x N]
    C -->|RequestRecords| M[Metrics]
    RC -->|aggregate + write| M
  end
  subgraph IG[Ingress]
    FE[Frontend: ForwardTask per /ping] -->|pick| LB[Load Balancer]
    FE -->|publish / clear| IM[(In-Flight Map)]
    FE --> UP[Upstream Pool]
    LC[Load Canceller] -->|snapshot| IM
  end
  subgraph SV[Server x instances]
    L[Listener] -->|one per connection| CH[Connection Handler]
    CH -->|POST /ping| WQ[(WorkQueue)]
    WQ --> WP[WorkerPool: n_worker_threads]
    WP --> PH[Ping Handler]
    PH --> CB[CPU Burner]
    CH -->|DELETE /cancel, inline| CT[Cancel Tracker]
    PH -->|CancelCheckPoints| CT
  end
  C <-->|"h2c connection i (POST /ping, RST_STREAM)"| FE
  UP <-->|"h2c connection per client x backend (POST /ping, RST_STREAM)"| CH
  LC -->|"h2c connection per backend (DELETE /cancel)"| CH
  P[(Protocol)] -.used by.- C
  P -.used by.- PH
  P -.used by.- FE
  S[(Sampling)] -.used by.- C
  S -.used by.- PH
  S -.used by.- LC
  subgraph OP[Operator machine]
    BR[Benchmark Runner]
  end
  BR ==>|"SSH: setup, start/stop, fetch"| SV
  BR ==>|"SSH: setup, run, fetch"| LG
  BR ==>|"SSH: start/stop, fetch"| IG
```

| Component | Role | Spec |
|---|---|---|
| Protocol | RequestId encoding, PaddedJsonBody and CancelBody, status codes | [protocol/description.md](protocol/description.md) |
| Sampling | Seeded RNG, exponential and uniform-integer samplers, per-stream seed derivation | [sampling/description.md](sampling/description.md) |
| Server | Accepts connections, serves `/ping` with a CPU-bound cost, applies RST and `/cancel` cancellation | [server/description.md](server/description.md) |
| Load Generator | Simulates N clients, applies SLOCancellation, records latency | [load_generator/description.md](load_generator/description.md) |
| Ingress | Load-balances `/ping` across server instances, relays answers, and cancels in-flight requests from inside (Load Canceller) | [ingress/description.md](ingress/description.md) |
| Benchmark Runner | Sets up two machines, runs a parameter sweep of the three programs, downloads and plots results | [benchmark_runner/description.md](benchmark_runner/description.md) |

Protocol and Sampling are shared leaves. Server, Load Generator and Ingress all depend on them and never on
each other's internals. The only coupling between the three programs is the wire contract in
[api.md](api.md#wire-contract-ping) and [api.md](api.md#wire-contract-cancel). The Ingress speaks the `/ping`
contract on both sides, so the load generator cannot tell it from a server, and it relies on one more server
behavior: each connection is bound to one client
([ingress/design.md#why-one-upstream-connection-per-client-and-backend](ingress/design.md#why-one-upstream-connection-per-client-and-backend)).

The Benchmark Runner depends on no other component's internals. It uses only the programs' command-line
parameters, their output files and the server's `listening on` and CancelCounters lines, all from
[api.md](api.md). It learns how to build an implementation from that implementation's BuildManifest
([benchmark_runner/remote_setup/design.md](benchmark_runner/remote_setup/design.md#buildmanifest)).

## End-to-end flow of one request

1. Client `i`'s sender reaches scheduled arrival `seq` (a GatedArrival: it waits while `n_waiting_requests` of
   its requests are unresolved).
2. Client builds RequestId `(i << 32) | seq` and a PaddedJsonBody of `request_bytes`, then sends
   `POST /ping` on its own connection to the Ingress and starts an SLO timer.
   The Ingress's ForwardTask picks a backend, records the request in the InFlightMap, and forwards it unchanged on
   its connection for client `i` and that backend. Everything below happens on that backend, and the answer (or a
   reset) travels back the same way.
3. The server's Connection Handler for that connection admits the request. On the connection's
   first `/ping` it binds the connection to client id `id >> 32`. It samples a ServiceTime from the
   connection's Rng and pushes the request onto the server-wide WorkQueue. One of the `n_worker_threads`
   workers of the WorkerPool takes it in arrival order, checks for cancellation, and runs the CPU Burner,
   checking again during the burn. The Connection Handler writes the answer the worker hands back.
4. Exactly one of these happens:
   - The burn completes. The server answers `202` and the client records `ok` (if within the SLO).
   - A matching `/cancel` is seen at a CancelCheckPoint (`cooperative` mode). The server stops, counts
     it, and answers `503`. The client records `rejected`.
   - The SLO fires first. The client sends `RST_STREAM(CANCEL)` and records `cancelled`. On the
     server, in `cooperative` mode, the worker skips or stops the request and writes nothing. In
     `ignore` mode the server burns the full time and its response is dropped.
5. The client's receiver records the outcome and frees the request's slot. The sender does not wait for steps
   2–4: it moves on to scheduled arrival `seq+1` as soon as that is due and a slot is free.

## End-to-end flow of one cancel

1. The Ingress's Load Canceller reaches a Poisson arrival and takes a snapshot of the InFlightMap. If it is empty,
   the arrival is `skipped`. Otherwise it draws one entry `(c, request_id, server_id)` uniformly.
2. It sends `DELETE /cancel` with that RequestId on its own connection to backend `server_id` and does not wait.
3. That server's handler for the canceller's connection handles the `/cancel` itself (it is never put on the
   WorkQueue): it pushes the id onto client `c`'s CancelInbox in its CancelRegistry and answers `202`.
4. At its next CancelCheckPoint, the worker running client `c`'s request on that server drains the inbox into
   the client's pending set and applies CancelMatching ([server/cancel_tracker/design.md](server/cancel_tracker/design.md#cancelmatching)).
   If the request already finished in between, it is counted `completed_before_cancel`.

## Why the server has one shared WorkQueue

The server runs `/ping`s from all connections on `n_worker_threads` workers fed by **one FIFO, in arrival
order**, like a CPU-bound service with a fixed number of cores. Each client keeps up to `n_waiting_requests` requests live, so the queue can hold many requests per client.
After a cancellation, the client's slot is free again while the server may still be working on the cancelled
request. In `ignore` mode the abandoned work keeps a worker busy, and every
request queued behind it waits, whichever client sent it. That wait is the effect this testbed measures.
`/cancel` never enters the queue, so it is never stuck behind the work it targets. CancelMatching matches each
`/cancel` to its request by id, so it does not depend on the order in which a client's many requests start or
finish. See
[server/design.md#why-one-shared-workqueue](server/design.md#why-one-shared-workqueue) and
[server/worker_pool/design.md#ordering](server/worker_pool/design.md#ordering).

## Time bounds of a run

The active phase is `[0, duration_s)` from the run start. The DrainPhase that follows lasts at
most `slo_ms`, because every in-flight request is resolved by its response or its SLO timer. See
[load_generator/run_controller/design.md](load_generator/run_controller/design.md#drainphase).
The Ingress's Load Canceller starts at the Ingress's first `/ping` and has its own `duration_s` and drain timeout
([ingress/load_canceller/design.md](ingress/load_canceller/design.md#cancellerdrain)). The operator starts the
servers, then the Ingress, then the load generator, and stops them in reverse order
([ingress/design.md#shutdown](ingress/design.md#shutdown)).
