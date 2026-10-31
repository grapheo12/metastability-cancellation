# HTTP/2 Cancellation Testbed — Description

Components: Protocol, Sampling, Server, Load Generator, Load Canceller, Benchmark Runner.

## Problem

We want to measure how **request cancellation** affects tail latency and wasted work in a CPU-bound
HTTP/2 service. The testbed covers two cancellation paths:

- **Client-side SLO cancellation.** A client gives up on a request that has not come back within its
  SLO and resets the stream. The server can keep burning CPU on the abandoned request, or notice the
  reset and stop.
- **Server-directed cancellation.** A separate **LoadCanceller** sends `DELETE /cancel` naming a
  RequestId. The server records it in a global **CancelRegistry**. The worker serving that client
  checks for cancellations at fixed **CancelCheckPoints** and stops the request if it matches,
  answering `503`. **CancelCounters** record whether each cancellation landed before processing,
  during processing, or after the request had already completed.

## The system in one paragraph

A **Server** accepts HTTP/2 connections and starts one handler (coroutine or thread) per connection.
It exposes `POST /ping` and `DELETE /cancel`. For each `/ping` it busy-loops on the CPU for a random
time drawn from an exponential distribution, then answers `202` (completed) or `503` (cancelled by
`/cancel`) with a JSON body of a fixed size. A **Load Generator** simulates `N` clients, each on its
own connection. Every client has its own Poisson arrival clock (rate `global_rate / N`) but keeps at
most one request outstanding (a gated closed loop), and cancels requests that go past their SLO. A
**Load Canceller** sends `/cancel` requests on its own Poisson clock from a single sender, choosing
client ids uniformly at random and advancing a per-client sequence counter by small random steps.
The load generator writes per-request records plus a summary. The canceller writes per-cancel records
plus a summary. The server prints its CancelCounters when it shuts down.

A **BenchmarkRunner** drives whole experiments from an operator's machine. It reads one **BenchConfig**,
prepares a server machine and a client machine over SSH (clone, pin the exact commit, check tools,
build), then runs every point of the **SweepExpansion** of the swept parameters. Each point runs the
server on the server machine and the load generator plus the canceller on the client machine. The runner
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
- Server metrics beyond the three CancelCounters.
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
| **SerialStreamWorker** | Per-connection worker that runs requests one at a time in arrival order | [server/connection_handler/design.md](server/connection_handler/design.md#serialstreamworker) |
| **CancelMode** | Server switch: `ignore` or `cooperative`. Applies to both cancellation paths | [api.md](api.md#server-parameters) |
| **CancelRegistry** | Global lock-free map from client id to a CancelInbox | [server/cancel_tracker/design.md](server/cancel_tracker/design.md#cancelregistry) |
| **CancelInbox** | Lock-free multi-producer stack of cancelled RequestIds for one client, drained by its worker | [server/cancel_tracker/design.md](server/cancel_tracker/design.md#cancelinbox) |
| **CancelMatching** | Per-connection algorithm that matches pending cancel ids against the current request | [server/cancel_tracker/design.md](server/cancel_tracker/design.md#cancelmatching) |
| **CancelCheckPoint** | A point where the worker consults CancelMatching: before a request starts, and every `CHECK_ITER` spin units during its burn | [server/ping_handler/design.md](server/ping_handler/design.md#cancelcheckpoints) |
| **CancelCounters** | Server-wide atomic counters: cancelled before processing, cancelled during processing, completed before cancel | [server/cancel_tracker/design.md](server/cancel_tracker/design.md#cancelcounters) |
| **ClosedLoopPoissonClient** | Client with a Poisson arrival clock and at most one outstanding request | [load_generator/client/design.md](load_generator/client/design.md#closedlooppoissonclient) |
| **GatedArrival** | A scheduled arrival that must wait for the previous request to resolve | [load_generator/client/design.md](load_generator/client/design.md#gatedarrival) |
| **SLOCancellation** | Client resets a stream once `slo_ms` has passed since send | [load_generator/client/design.md](load_generator/client/design.md#slocancellation) |
| **DrainPhase** | Time after `duration_s` when no new requests are sent and in-flight ones resolve | [load_generator/run_controller/design.md](load_generator/run_controller/design.md#drainphase) |
| **RequestRecord** | One output row per scheduled arrival | [load_generator/metrics/design.md](load_generator/metrics/design.md#requestrecord) |
| **LoadCanceller** | Single-sender, open-loop Poisson source of `/cancel` requests | [load_canceller/design.md](load_canceller/design.md#cancelschedule) |
| **CancelTargetChoice** | Uniform client choice plus per-client counter advanced by `[1, INCR_RAND_MAX]` | [load_canceller/design.md](load_canceller/design.md#canceltargetchoice) |
| **BenchmarkRunner** | Operator-side driver: setup over SSH, sweep execution, result download, plots | [benchmark_runner/design.md](benchmark_runner/design.md#run-flow) |
| **BenchConfig** | The validated TOML experiment configuration | [benchmark_runner/config/design.md](benchmark_runner/config/design.md#benchconfig) |
| **SweepExpansion** | Cross product of every array-valued program parameter, in a fixed order | [benchmark_runner/config/design.md](benchmark_runner/config/design.md#sweepexpansion) |
| **SweepPoint** | One element of the SweepExpansion: the full argument lists of the three programs | [benchmark_runner/config/design.md](benchmark_runner/config/design.md#sweepexpansion) |
| **Transport** | Run commands on, and copy files from, a machine over SSH (or locally) | [benchmark_runner/remote_transport/design.md](benchmark_runner/remote_transport/design.md#transport) |
| **SourcePin** | The exact branch and commit the runner deploys, checked against `repo_url` | [benchmark_runner/remote_setup/design.md](benchmark_runner/remote_setup/design.md#sourcepin) |
| **BuildManifest** | Per-implementation declarative file saying how to build and where the binaries are | [benchmark_runner/remote_setup/design.md](benchmark_runner/remote_setup/design.md#buildmanifest) |
| **ServerLifecycle** | Start, readiness wait and SIGINT stop of the server for one SweepPoint | [benchmark_runner/experiment_executor/design.md](benchmark_runner/experiment_executor/design.md#serverlifecycle) |
| **PointTable** | One row per SweepPoint with swept values and key metrics | [benchmark_runner/result_plotter/design.md](benchmark_runner/result_plotter/design.md#pointtable) |
| **SweepPlots** | The standard figures over all SweepPoints | [benchmark_runner/result_plotter/design.md](benchmark_runner/result_plotter/design.md#sweepplots) |

See [api.md](api.md) for the external interface and [design.md](design.md) for the component breakdown.
