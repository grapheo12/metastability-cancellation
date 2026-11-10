# HTTP/2 Cancellation Testbed — Description

Components: Protocol, Sampling, Server, Load Generator, Ingress, Benchmark Runner.

## Problem

We want to measure how **request cancellation** affects tail latency and wasted work in a CPU-bound
HTTP/2 service. The testbed covers two cancellation paths:

- **Client-side SLO cancellation.** A client gives up on a request that has not come back within its
  SLO and resets the stream. The server can keep burning CPU on the abandoned request, or notice the
  reset and stop.
- **Server-directed cancellation.** A **LoadCanceller** inside the Ingress sends `DELETE /cancel` naming a
  RequestId that is in flight. The server records it in a global **CancelRegistry**. The worker running that client's
  request checks for cancellations at fixed **CancelCheckPoints** and stops the request if it matches,
  answering `503`. **CancelCounters** record whether each cancellation landed before processing,
  during processing, or after the request had already completed.

## The system in one paragraph

A **Server** accepts HTTP/2 connections and starts one handler (coroutine or thread) per connection.
It exposes `POST /ping` and `DELETE /cancel`. A handler answers `/cancel` itself and puts every `/ping` on one
process-wide, unbounded **WorkQueue**. A **WorkerPool** of `n_worker_threads` workers takes `/ping`s off it in
arrival order. For each `/ping` a worker busy-loops on the CPU for a random
time drawn from an exponential distribution, then answers `202` (completed) or `503` (cancelled by
`/cancel`) with a JSON body of a fixed size. A **Load Generator** simulates `N` clients, each on its
own connection. Every client has its own Poisson arrival clock (rate `global_rate / N`) but keeps at
most one request outstanding (a gated closed loop), and cancels requests that go past their SLO. The
clients talk to an **Ingress**, which serves the same `/ping`: for each request it picks a backend server
with a **LoadBalancer** (`lb_policy`), forwards the request on its own connection for that client and
backend, waits, and relays the answer. While it waits it records the request in a lock-free
**InFlightMap**. A **LoadCanceller** inside the Ingress sends `/cancel` requests on its own Poisson clock,
each naming a request drawn uniformly from the InFlightMap, to the backend serving it. The load generator
writes per-request records plus a summary. The Ingress writes per-cancel records plus a summary with
per-backend counts. Each server prints its CancelCounters when it shuts down.

A **BenchmarkRunner** drives whole experiments from an operator's machine. It reads one **BenchConfig**,
prepares a server machine and a client machine over SSH (clone, pin the exact commit, check tools,
build), then runs every point of the **SweepExpansion** of the swept parameters. Each point runs one or more
server instances on the server machine and the load generator on the client machine; the Ingress runs on the
client machine or, by its **IngressPlacement**, next to the servers. The runner
downloads each point's results and plots all points together.

## Goals

- Show the effect of server-side cancellation handling (`ignore` vs `cooperative`) on latency.
- Show how often server-directed cancellations arrive before, during or after the work they target.
- Make runs reproducible where possible (seeded randomness, exact body sizes, exact id format).
- Keep the server's per-request cost a pure, controllable CPU cost.
- Make parameter sweeps over two real machines a single command with a reproducible record (config,
  branch, commit, raw outputs, plots).

## Non-goals

- TLS, HTTP/1.1, or any endpoint other than `/ping` and `/cancel`.
- Server metrics beyond the three CancelCounters and the QueueLengthLog.
- Load shedding or admission control. `503` means only "cancelled by `/cancel`" (or, for `/cancel`
  itself, "registry full").
- Distributed load generation across more than one client machine.
- Installing toolchains on the experiment machines. The BenchmarkRunner only checks that they exist.
- Coordinating the Load Canceller with the Load Generator. The two programs run independently.

## Glossary (named concepts used across all specs)

| Concept | Meaning | Defined in |
|---|---|---|
| **RequestId** | 64-bit id: high 32 bits are the client id, low 32 bits are the per-client sequence number | [protocol/design.md](protocol/design.md#requestid) |
| **PaddedJsonBody** | JSON body `{"id":..,"payload":..}` padded so its byte length is exact | [protocol/design.md](protocol/design.md#paddedjsonbody) |
| **CancelBody** | JSON body `{"id":..}` of a `/cancel` request | [protocol/design.md](protocol/design.md#cancelbody) |
| **ServiceTime** | CPU time per request, `Exp(response_rate)` seconds, in whole microseconds | [sampling/design.md](sampling/design.md#exponentialsampler) |
| **CooperativeCpuBurn** | Busy loop that checks the RST flag on a time interval and `/cancel` every `CHECK_ITER` spin units | [server/cpu_burner/design.md](server/cpu_burner/design.md#cooperativecpuburn) |
| **WorkQueue** | The server's one unbounded FIFO of admitted `/ping`s, shared by all connections | [server/worker_pool/design.md](server/worker_pool/design.md#workqueue) |
| **WorkerPool** | `n_worker_threads` server workers that run WorkQueue jobs in arrival order | [server/worker_pool/design.md](server/worker_pool/design.md#workerpool) |
| **PingJob** | One admitted `/ping` with its RequestId and pre-drawn ServiceTime, waiting for or held by a worker | [server/worker_pool/design.md](server/worker_pool/design.md#pingjob) |
| **CancelMode** | Server switch: `ignore` or `cooperative`. Applies to both cancellation paths | [api.md](api.md#server-parameters) |
| **CancelRegistry** | Global lock-free map from client id to a CancelInbox | [server/cancel_tracker/design.md](server/cancel_tracker/design.md#cancelregistry) |
| **CancelInbox** | Lock-free multi-producer stack of cancelled RequestIds for one client, drained by the worker running that client's request | [server/cancel_tracker/design.md](server/cancel_tracker/design.md#cancelinbox) |
| **QueueLengthLog** | Optional server CSV of the WorkQueue length (waiting jobs) sampled every `queue_sample_interval_ms` | [server/worker_pool/design.md](server/worker_pool/design.md#queuelengthlog) |
| **ClientCancelState** | Per-client pending `/cancel` ids and last completed RequestId, in the client's registry slot under a per-client lock | [server/cancel_tracker/design.md](server/cancel_tracker/design.md#clientcancelstate) |
| **CancelMatching** | Per-client algorithm that matches pending cancel ids against the request a worker is running | [server/cancel_tracker/design.md](server/cancel_tracker/design.md#cancelmatching) |
| **CancelCheckPoint** | A point where the worker consults CancelMatching: before a request starts, and every `CHECK_ITER` spin units during its burn | [server/ping_handler/design.md](server/ping_handler/design.md#cancelcheckpoints) |
| **CancelCounters** | Server-wide atomic counters: cancelled before processing, cancelled during processing, completed before cancel | [server/cancel_tracker/design.md](server/cancel_tracker/design.md#cancelcounters) |
| **ClosedLoopPoissonClient** | Client with a Poisson arrival clock and at most one outstanding request | [load_generator/client/design.md](load_generator/client/design.md#closedlooppoissonclient) |
| **GatedArrival** | A scheduled arrival that must wait for the previous request to resolve | [load_generator/client/design.md](load_generator/client/design.md#gatedarrival) |
| **SLOCancellation** | Client resets a stream once `slo_ms` has passed since send | [load_generator/client/design.md](load_generator/client/design.md#slocancellation) |
| **DrainPhase** | Time after `duration_s` when no new requests are sent and in-flight ones resolve | [load_generator/run_controller/design.md](load_generator/run_controller/design.md#drainphase) |
| **RequestRecord** | One output row per scheduled arrival | [load_generator/metrics/design.md](load_generator/metrics/design.md#requestrecord) |
| **LoadCanceller** | Single-sender, open-loop Poisson source of `/cancel` requests inside the Ingress | [ingress/load_canceller/design.md](ingress/load_canceller/design.md#cancelschedule) |
| **FinishedWatermark** | Per client, the highest RequestId the server has finished with any outcome; reported in every `/cancel` `202` | [server/cancel_tracker/design.md](server/cancel_tracker/design.md#finishedwatermark) |
| **CancelReplyBody** | JSON body `{"finished":..}` of a `202` answer to `/cancel` | [protocol/design.md](protocol/design.md#cancelreplybody) |
| **InFlightVictimChoice** | The LoadCanceller's choice: one entry drawn uniformly from an InFlightMap snapshot | [ingress/load_canceller/design.md](ingress/load_canceller/design.md#inflightvictimchoice) |
| **Ingress** | Program in front of several servers: LoadBalancer, UpstreamPool, InFlightMap and LoadCanceller | [ingress/design.md](ingress/design.md#structure) |
| **LbPolicy** | `round_robin`, `least_outstanding` or `power_of_two` | [ingress/load_balancer/design.md](ingress/load_balancer/design.md#lbpolicy) |
| **LoadBalancer** | Picks a backend per `/ping` by LbPolicy and keeps BackendStats | [ingress/load_balancer/design.md](ingress/load_balancer/design.md#loadbalancer) |
| **BackendStats** | Per-backend atomic counters: outstanding, forwarded and outcomes | [ingress/load_balancer/design.md](ingress/load_balancer/design.md#loadbalancer) |
| **UpstreamPool** | One h2c connection per (client, backend), dialed once | [ingress/upstream_pool/design.md](ingress/upstream_pool/design.md#upstreampool) |
| **InFlightMap** | Lock-free `client_id → InFlightEntry` of the requests the Ingress is waiting on | [ingress/inflight_map/design.md](ingress/inflight_map/design.md#inflightmap) |
| **InFlightEntry** | `(request_id, server_id)` of one forward in progress | [ingress/inflight_map/design.md](ingress/inflight_map/design.md#inflightmap) |
| **ForwardTask** | The Ingress's handling of one client `/ping`: pick, publish, forward, clear, relay | [ingress/frontend/design.md](ingress/frontend/design.md#forwardtask) |
| **BenchmarkRunner** | Operator-side driver: setup over SSH, sweep execution, result download, plots | [benchmark_runner/design.md](benchmark_runner/design.md#run-flow) |
| **BenchConfig** | The validated TOML experiment configuration | [benchmark_runner/config/design.md](benchmark_runner/config/design.md#benchconfig) |
| **SweepExpansion** | Cross product of every array-valued program parameter, in a fixed order | [benchmark_runner/config/design.md](benchmark_runner/config/design.md#sweepexpansion) |
| **SweepPoint** | One element of the SweepExpansion: the full argument lists of the three programs | [benchmark_runner/config/design.md](benchmark_runner/config/design.md#sweepexpansion) |
| **ProcessPlacement** | A program's worker-thread count and CPU set, applied when the runner starts it | [benchmark_runner/config/design.md](benchmark_runner/config/design.md#processplacement) |
| **CancelRateFraction** | Canceller rate given as a fraction of the same point's `global_rate` | [benchmark_runner/config/design.md](benchmark_runner/config/design.md#cancelratefraction) |
| **Transport** | Run commands on, and copy files from, a machine over SSH (or locally) | [benchmark_runner/remote_transport/design.md](benchmark_runner/remote_transport/design.md#transport) |
| **SourcePin** | The exact branch and commit the runner deploys, checked against `repo_url` | [benchmark_runner/remote_setup/design.md](benchmark_runner/remote_setup/design.md#sourcepin) |
| **BuildManifest** | Per-implementation declarative file saying how to build and where the binaries are | [benchmark_runner/remote_setup/design.md](benchmark_runner/remote_setup/design.md#buildmanifest) |
| **ServerLifecycle** | Start, readiness wait and SIGINT stop of the server for one SweepPoint | [benchmark_runner/experiment_executor/design.md](benchmark_runner/experiment_executor/design.md#serverlifecycle) |
| **PointTable** | One row per SweepPoint with swept values and key metrics | [benchmark_runner/result_plotter/design.md](benchmark_runner/result_plotter/design.md#pointtable) |
| **SweepPlots** | The standard figures over all SweepPoints | [benchmark_runner/result_plotter/design.md](benchmark_runner/result_plotter/design.md#sweepplots) |
| **ThroughputLatencyPlot** | Latency percentiles against measured goodput, one line per series, points in swept order | [benchmark_runner/result_plotter/design.md](benchmark_runner/result_plotter/design.md#throughputlatencyplot) |
| **CancelOfferedFraction** | A point's canceller `cancel_rate` divided by its `global_rate` (PointTable column `cx_offered_fraction`) | [benchmark_runner/result_plotter/design.md](benchmark_runner/result_plotter/design.md#cancelofferedfraction) |
| **ThroughputLatencyNormalizedPlot** | ThroughputLatencyPlot with goodput scaled by `1 / (1 - CancelOfferedFraction)` | [benchmark_runner/result_plotter/design.md](benchmark_runner/result_plotter/design.md#throughputlatencynormalizedplot) |
| **LbImbalance** | Largest over smallest per-backend `forwarded` count of a point (PointTable column `lb_imbalance`) | [benchmark_runner/result_plotter/design.md](benchmark_runner/result_plotter/design.md#lbimbalance) |
| **IngressPlacement** | Which experiment machine runs the Ingress: `client` (default) or `server` | [benchmark_runner/config/design.md](benchmark_runner/config/design.md#ingressplacement) |

See [api.md](api.md) for the external interface and [design.md](design.md) for the component breakdown.
