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
arrival order. For each `/ping` a worker does the work of the **ServerWorkload**: by default it busy-loops on the
CPU for a random time drawn from an exponential distribution (`cpu_burn`); with `message_buffer` it instead writes
new messages into a large **MessageBuffer**, to stress the garbage collector. It then answers `202` (completed) or `503` (cancelled by
`/cancel`) with a JSON body of a fixed size. A **Load Generator** simulates `N` clients, each on its
own connection. Every client has its own Poisson arrival clock (rate `global_rate / N`) and keeps up to
`n_waiting_requests` requests outstanding (a semi-open loop: arrivals wait only when that cap is reached), and
cancels requests that go past their SLO. The
clients talk to an **Ingress**, which serves the same `/ping`: for each request it picks a backend server
with a **LoadBalancer** (`lb_policy`), forwards the request on its own connection for that client and
backend, waits, and relays the answer. While it waits it records the request in a lock-free
**InFlightMap**. A **LoadCanceller** inside the Ingress sends `/cancel` requests on its own Poisson clock,
each naming a request drawn uniformly from the InFlightMap, to the backend serving it. The load generator
writes per-request records plus a summary. The Ingress writes per-cancel records plus a summary with
per-backend counts. Each server prints its CancelCounters when it shuts down.

A **BenchmarkRunner** drives whole experiments from an operator's machine. It reads one **BenchConfig**,
prepares one or more server machines (**ServerMachines**) and a client machine over SSH (clone, pin the exact commit, check tools,
build), then runs every point of the **SweepExpansion** of the swept parameters. Each point runs one or more
server instances on the server machines and the load generator on the client machine; the Ingress runs on the
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
| **PaddedJsonBody** | JSON body `{"id":..,"exec_ns":..,"start_slack_ns":..,"payload":..}` padded so its byte length is exact; the `/ping` response body | [protocol/design.md](protocol/design.md#paddedjsonbody) |
| **WorkerCount** | A server's `n_worker_threads`, sent as the `x-worker-threads` header on every `/ping` `202` and `503` | [protocol/design.md](protocol/design.md#workercount) |
| **PastDeadlineRejection** | With `reject_past_deadline = true`, a server answers `503` to a `/ping` popped after its RequestDeadline instead of running it; checked once, at the pop | [server/ping_handler/design.md](server/ping_handler/design.md#pastdeadlinerejection) |
| **ExecReport** | The server's report in every PaddedJsonBody: ExecutionTime and StartSlack | [protocol/design.md](protocol/design.md#execreport) |
| **ExecutionTime** | `exec_ns`: a request's time on the server minus its queueing delay (worker pop to response built) | [protocol/design.md](protocol/design.md#execreport) |
| **StartSlack** | `start_slack_ns`: RequestDeadline minus the server's wall clock when a worker popped the request; negative when already late | [protocol/design.md](protocol/design.md#execreport) |
| **PingRequestBody** | JSON body `{"id":..,"deadline":..,"payload":..}` of a `/ping` request, padded so its byte length is exact | [protocol/design.md](protocol/design.md#pingrequestbody) |
| **RequestDeadline** | `deadline_unix_ns` in a PingRequestBody: Unix ns of the request's first send plus the SLO; every RequestCopy carries the same one | [load_generator/client/design.md](load_generator/client/design.md#requestdeadline) |
| **CancelBody** | JSON body `{"id":..}` of a `/cancel` request | [protocol/design.md](protocol/design.md#cancelbody) |
| **ServiceTime** | CPU time per request, `Exp(response_rate)` seconds, in whole microseconds | [sampling/design.md](sampling/design.md#exponentialsampler) |
| **ServerWorkload** | What a `/ping` costs the server: `cpu_burn` (burn the ServiceTime), `sleep` (QuantumSleep the ServiceTime), `sleep_constant` (QuantumSleep the ConstantServiceTime) or `message_buffer` (MessageWork) | [server/design.md](server/design.md#why-a-messagebuffer-workload) |
| **MessageBuffer** | Server-wide window of `window_size` MessageTrees, filled before the server listens; requests overwrite slots under one lock | [server/message_buffer/design.md](server/message_buffer/design.md#messagebuffer) |
| **MessageTree** | One MessageBuffer message: a random binary tree of exactly `n_gc_binary_tree_size` nodes, each a u64 value and two child references | [server/message_buffer/design.md](server/message_buffer/design.md#messagetree) |
| **MessageCursor** | Per client, the next MessageBuffer slot its messages overwrite; starts at `(client_id × 7919) mod window_size` | [server/message_buffer/design.md](server/message_buffer/design.md#messagecursor) |
| **MessageWork** | A `message_buffer` request's work: create `n_messages_per_request` messages with cancel checks after each, then push them | [server/ping_handler/design.md](server/ping_handler/design.md#messagework) |
| **CooperativeCpuBurn** | Busy loop that checks the RST flag on a time interval and `/cancel` every `CHECK_ITER` spin units | [server/cpu_burner/design.md](server/cpu_burner/design.md#cooperativecpuburn) |
| **ConstantServiceTime** | With `workload = sleep_constant`, the same ServiceTime for every request, `round(1_000_000 / response_rate)` µs: the `sleep` workload's mean without its variance; no random draw | [server/ping_handler/design.md](server/ping_handler/design.md#constantservicetime) |
| **QuantumSleep** | Wait out the ServiceTime without CPU, sleeping in 20 ms quanta and checking the RST flag and `/cancel` at each wake-up | [server/sleeper/design.md](server/sleeper/design.md#quantumsleep) |
| **WorkQueue** | The server's one unbounded queue of admitted `/ping`s, shared by all connections, popped by the QueueDiscipline | [server/worker_pool/design.md](server/worker_pool/design.md#workqueue) |
| **QueueDiscipline** | The WorkQueue's pop order: `fifo` (admission order) or `edf` (EarliestDeadlineFirst) | [server/worker_pool/design.md](server/worker_pool/design.md#workqueue) |
| **EarliestDeadlineFirst** | `edf` QueueDiscipline: pop the queued job with the smallest `(RequestDeadline, push order)` | [server/worker_pool/design.md](server/worker_pool/design.md#earliestdeadlinefirst) |
| **WorkerPool** | `n_worker_threads` server workers that run WorkQueue jobs in arrival order | [server/worker_pool/design.md](server/worker_pool/design.md#workerpool) |
| **PingJob** | One admitted `/ping` with its RequestId and pre-drawn ServiceTime, waiting for or held by a worker | [server/worker_pool/design.md](server/worker_pool/design.md#pingjob) |
| **CancelMode** | Server switch: `ignore` or `cooperative`. Applies to both cancellation paths | [api.md](api.md#server-parameters) |
| **CancelRegistry** | Global lock-free map from client id to a CancelInbox | [server/cancel_tracker/design.md](server/cancel_tracker/design.md#cancelregistry) |
| **CancelInbox** | Lock-free multi-producer stack of cancelled RequestIds for one client, drained by the worker running that client's request | [server/cancel_tracker/design.md](server/cancel_tracker/design.md#cancelinbox) |
| **StallGate** | Server start-up stall: no `/ping` runs until `initial_stall_ms` after the first one is admitted, so a backlog builds | [server/worker_pool/design.md](server/worker_pool/design.md#stallgate) |
| **ServiceScenario** | A server's optional state machine (`service_scenario`, with `sleep_constant`): ScenarioStates and either a ScenarioSchedule or a MarkovSchedule, followed from the first admitted `/ping` on | [server/service_scenario/design.md](server/service_scenario/design.md#servicescenario) |
| **ScenarioState** | One state of a ServiceScenario: a ConstantServiceTime (`service_us`) for the requests popped in it, or a StalledState | [server/service_scenario/design.md](server/service_scenario/design.md#servicescenario) |
| **ScenarioSchedule** | Time-based transitions: `[(at_ms, state)]` from 0; at every ScenarioClock time the server is in the state of the last entry at or before it | [server/service_scenario/design.md](server/service_scenario/design.md#scenarioschedule) |
| **MarkovSchedule** | Random transitions: a discrete-time Markov chain with `step_ms` and a row-stochastic matrix, one `next_unit` draw per step from `derive_seed(seed, STREAM_SERVER_SCENARIO, stream)` | [server/service_scenario/design.md](server/service_scenario/design.md#markovschedule) |
| **ScenarioClock** | A ServiceScenario's time: ns since the first admitted `/ping` (the StallGate's arm time) | [server/service_scenario/design.md](server/service_scenario/design.md#scenarioclock) |
| **StalledState** | A ScenarioState in which the server acts frozen (nothing popped, running requests held, no heartbeat line) until the state is left | [server/service_scenario/design.md](server/service_scenario/design.md#stalledstate) |
| **ScenarioLog** | A server's CSV of ScenarioState changes, `t_ns,unix_ns,state` (`scenario_log_path`) | [server/service_scenario/design.md](server/service_scenario/design.md#scenariolog) |
| **WorkerFreeze** | Injected server failure: `freeze_after_ms` after the first admitted `/ping`, every worker stops for good, mid-request too, while the process keeps admitting and queueing | [server/worker_pool/design.md](server/worker_pool/design.md#workerfreeze) |
| **FreezeTarget** | Benchmark Runner key `[server] freeze_instance`: the one server instance that gets `freeze_after_ms` (WorkerFreeze) | [benchmark_runner/config/design.md](benchmark_runner/config/design.md#freezetarget) |
| **Heartbeat** | A server's liveness signal: a HeartbeatLine every `heartbeat_interval_ms` on `GET /heartbeat`, stopped by the WorkerFreeze | [server/heartbeat/design.md](server/heartbeat/design.md#heartbeatsender) |
| **BackendLiveness** | The Ingress's view of whether a backend still beats: live while its last HeartbeatLine is younger than `heartbeat_timeout_ms`; only live backends get new requests | [ingress/heartbeat_monitor/design.md](ingress/heartbeat_monitor/design.md#backendliveness) |
| **HeartbeatStream** | Never-ending `200` body of `GET /heartbeat`: one HeartbeatLine `hb <seq>` per heartbeat | [protocol/design.md](protocol/design.md#heartbeatline) |
| **QueueLengthLog** | Optional server CSV of the WorkQueue length (waiting jobs) sampled every `queue_sample_interval_ms` | [server/worker_pool/design.md](server/worker_pool/design.md#queuelengthlog) |
| **ClientCancelState** | Per-client pending `/cancel` ids, admitted-but-unfinished requests and a ring of recently finished ones, in the client's registry slot under a per-client lock | [server/cancel_tracker/design.md](server/cancel_tracker/design.md#clientcancelstate) |
| **CancelMatching** | Per-client algorithm that matches each pending cancel id to its own request by id, in any request order | [server/cancel_tracker/design.md](server/cancel_tracker/design.md#cancelmatching) |
| **CancelCheckPoint** | A point where the worker consults CancelMatching: before a request starts, and every `CHECK_ITER` spin units during its burn | [server/ping_handler/design.md](server/ping_handler/design.md#cancelcheckpoints) |
| **CancelCounters** | Server-wide atomic counters: cancelled before processing, cancelled during processing, completed before cancel | [server/cancel_tracker/design.md](server/cancel_tracker/design.md#cancelcounters) |
| **SemiOpenLoopClient** | Client with a Poisson arrival clock, a sender and a receiver, and up to `n_waiting_requests` outstanding requests | [load_generator/client/design.md](load_generator/client/design.md#semiopenloopclient) |
| **GatedArrival** | A scheduled arrival that must wait for a slot because `n_waiting_requests` are outstanding | [load_generator/client/design.md](load_generator/client/design.md#gatedarrival) |
| **SLOCancellation** | Client resets every RequestCopy of a request once `slo_ms` has passed since its first send | [load_generator/client/design.md](load_generator/client/design.md#slocancellation) |
| **RetryTimer** | Per RequestCopy except the last, `retry_timeout_ms` from its send: fires the next copy. The last copy waits for its answer or the SLO | [load_generator/client/design.md](load_generator/client/design.md#retrytimer) |
| **RequestCopy** | One send of a request on its own stream; all copies carry the same RequestId and body, and only the newest one's answer counts | [load_generator/client/design.md](load_generator/client/design.md#retrytimer) |
| **StallWindow** | The first `initial_stall_ms` of the load generator's run, mirroring the servers' StallGate | [load_generator/client/design.md](load_generator/client/design.md#stallwindow) |
| **StallWindowRequest** | A request first sent in the StallWindow: never retries, flagged `in_stall_window`, left out of every metric | [load_generator/client/design.md](load_generator/client/design.md#stallwindow) |
| **DrainPhase** | Time after `duration_s` when no new requests are sent and in-flight ones resolve | [load_generator/run_controller/design.md](load_generator/run_controller/design.md#drainphase) |
| **RequestRecord** | One output row per scheduled arrival | [load_generator/metrics/design.md](load_generator/metrics/design.md#requestrecord) |
| **LoadCanceller** | Single-sender, open-loop Poisson source of `/cancel` requests inside the Ingress | [ingress/load_canceller/design.md](ingress/load_canceller/design.md#cancelschedule) |
| **FinishedWatermark** | Per client, the highest RequestId the server has finished with any outcome; reported in every `/cancel` `202` | [server/cancel_tracker/design.md](server/cancel_tracker/design.md#finishedwatermark) |
| **CancelReplyBody** | JSON body `{"finished":..}` of a `202` answer to `/cancel` | [protocol/design.md](protocol/design.md#cancelreplybody) |
| **InFlightVictimChoice** | The LoadCanceller's choice: one in-flight request drawn uniformly from an InFlightMap snapshot | [ingress/load_canceller/design.md](ingress/load_canceller/design.md#inflightvictimchoice) |
| **Ingress** | Program in front of several servers: LoadBalancer, UpstreamPool, InFlightMap and LoadCanceller | [ingress/design.md](ingress/design.md#structure) |
| **LbPolicy** | `round_robin`, `least_outstanding`, `power_of_two` or `ewma` | [ingress/load_balancer/design.md](ingress/load_balancer/design.md#lbpolicy) |
| **LoadBalancer** | Picks a backend per `/ping` by LbPolicy and keeps BackendStats | [ingress/load_balancer/design.md](ingress/load_balancer/design.md#loadbalancer) |
| **BackendStats** | Per-backend atomic counters: outstanding, forwarded and outcomes; plus the EwmaLatency | [ingress/load_balancer/design.md](ingress/load_balancer/design.md#loadbalancer) |
| **EwmaLatency** | Per backend, exponentially weighted moving average of the latency of `ok` forwards (ns, starts at 1 ms, weight `ewma_beta` on the old value) | [ingress/load_balancer/design.md](ingress/load_balancer/design.md#ewmalatency) |
| **EwmaExecTime** | Per backend, exponentially weighted moving average of the ExecutionTime the backend reported on `ok` and `rejected` forwards (ns, the first sample starts it, weight `ewma_beta` on the old value); no policy reads it yet | [ingress/load_balancer/design.md](ingress/load_balancer/design.md#ewmaexectime) |
| **LbRetry** | An extra attempt the Ingress starts for a `/ping` on its own (not the client's RequestCopy); the newest attempt's answer counts, at most `lb_max_retries` per client stream | [ingress/retry_policy/design.md](ingress/retry_policy/design.md#retrypolicy) |
| **RetryPolicy** | The pluggable `decide(state, event) -> Retry \| RetryElsewhere \| Wait` that says when to start an LbRetry, named by `lb_retry_policy`; `none` never retries, `timeout` retries after `lb_retry_timeout_ms` | [ingress/retry_policy/design.md](ingress/retry_policy/design.md#retrypolicy) |
| **AnxiousRetry** | The `anxious` RetryPolicy: retry an attempt when `(CurrentExpFinish - RetryExpFinish) / CurrentExpFinish >= lb_retry_anxiety_ratio`, with CurrentExpFinish `= (EdfAhead on its backend + 1) × EwmaExecTime` and RetryExpFinish `= (EdfAhead summed over the live other backends / their number + 1) × EwmaAntiServiceTime`; decides `RetryElsewhere`, only if CurrentExpFinish also exceeds `lb_retry_late_factor` times the request's time left (ExpectedLate) | [ingress/retry_policy/design.md](ingress/retry_policy/design.md#anxious) |
| **AnxiousTracker** | AnxiousRetry's own activity: follows every attempt (TicketSeq, backend, RequestDeadline) through RetryObservations and re-evaluates the anxious set at a 10 ms tick only after a stat or liveness change (AnxiousEvaluation); a refused retry leaves the set until then | [ingress/retry_policy/design.md](ingress/retry_policy/design.md#anxious) |
| **EdfAhead** | On a backend, the number of attempts in flight whose RequestDeadline is at or before a given request's (remaining time `<=` its own): the requests an EDF server would serve before it | [ingress/retry_policy/design.md](ingress/retry_policy/design.md#anxious) |
| **RetryObservation** | What the Frontend tells an observing RetryPolicy about every attempt: `AttemptSent`, `AttemptFinished`, `RetryRefused`, `EwmaChanged` | [ingress/retry_policy/design.md](ingress/retry_policy/design.md#retryobservation) |
| **FilterWaitQueue** | FIFO of first attempts no backend passes under the DeadlineFilter; re-checked every 10 ms, sent when one fits, dropped (`503`) once the RequestDeadline passes | [ingress/load_balancer/design.md](ingress/load_balancer/design.md#filterwaitqueue) |
| **DeadlineFilter** | With `lb_filter` set to a DeadlineStrategy, a request goes only to backends expected to finish it within its time left; none left: it waits in the FilterWaitQueue and is dropped with `503` once its deadline passes |
| **EwmaAntiServiceTime** | Per backend, exponentially weighted moving average of the ExecutionTime reported by every **other** backend (the EwmaExecTime samples, each folded into every backend but the one that served it) | [ingress/load_balancer/design.md](ingress/load_balancer/design.md#ewmaantiservicetime) |
| **ExcludedBackend** | The backend an LbRetry decided `RetryElsewhere` must not go to (the newest attempt's); `pick` removes it from the LiveSet and is `Dropped` if none is left | [ingress/load_balancer/design.md](ingress/load_balancer/design.md#lbpolicy) |
| **TicketSeq** | A ForwardTicket's number, unique per Ingress run and increasing in pick order; names one attempt | [ingress/load_balancer/design.md](ingress/load_balancer/design.md#forwardticket) |
| **QueueAtSend** | A ForwardTicket's backend `outstanding` just before its pick's increment (the DeadlineFilter's `n`) | [ingress/load_balancer/design.md](ingress/load_balancer/design.md#forwardticket) |
| **DeadlineStrategy** | The DeadlineFilter's per-backend test: `deadline_mean` (`(outstanding + 1) × EwmaExecTime <= T`), `deadline_mean_per_worker` (the same divided by the WorkerCount), `deadline_mean_queue_plus_share` (`(outstanding + 1/WorkerCount) × EwmaExecTime <= T`) or `deadline_mmk` (M/M/k on-time probability `>= 0.99`) | [ingress/load_balancer/design.md](ingress/load_balancer/design.md#deadlinefilter) |
| **PickLog** | The LoadBalancer's record, at every pick, of each backend's `outstanding`, EwmaExecTime and WorkerCount (`ingress.picks.csv`) | [ingress/load_balancer/design.md](ingress/load_balancer/design.md#picklog) |
| **EwmaScore** | `(outstanding + 1) × EwmaLatency` of a backend; the `ewma` LbPolicy picks the lower of a random pair | [ingress/load_balancer/design.md](ingress/load_balancer/design.md#ewmascore) |
| **ForwardTicket** | `(server_id, start_ns)` returned by the LoadBalancer's `pick` and given back to `finish`, to time the forward | [ingress/load_balancer/design.md](ingress/load_balancer/design.md#forwardticket) |
| **EstimatedFinish** | At a pick, `(queue_at_send + 1) × EwmaExecTime / WorkerCount` of the chosen backend: how long the LoadBalancer expects the forward to take; carried to `forwards.csv` as `estimate_ns` | [ingress/load_balancer/design.md](ingress/load_balancer/design.md#estimatedfinish) |
| **EstimateError** | Of an `ok` forward, `(latency_ns − EstimatedFinish) / latency_ns`; 0 is exact, negative means it took less than estimated | [ingress/load_balancer/design.md](ingress/load_balancer/design.md#estimatedfinish) |
| **EstimateErrorCdf** | Per SweepPoint, the CDF of the EstimateError of ok forwards, split before and after the FailureTime (`estimate_error.html`) | [benchmark_runner/result_plotter/design.md](benchmark_runner/result_plotter/design.md#estimateerrorcdf) |
| **ForwardRecord** | `(end_ns, server_id, outcome, latency_ns, ewma_latency_ns)` of one finished forward | [ingress/load_balancer/design.md](ingress/load_balancer/design.md#forwardlog) |
| **ForwardLog** | The LoadBalancer's list of every ForwardRecord, written as `<output_path>.forwards.csv` | [ingress/load_balancer/design.md](ingress/load_balancer/design.md#forwardlog) |
| **UpstreamPool** | One h2c connection per (client, backend), dialed once | [ingress/upstream_pool/design.md](ingress/upstream_pool/design.md#upstreampool) |
| **InFlightMap** | Lock-free `request_id → InFlightEntry` of the requests the Ingress is waiting on | [ingress/inflight_map/design.md](ingress/inflight_map/design.md#inflightmap) |
| **InFlightEntry** | `(request_id, server_id)` of one forward in progress | [ingress/inflight_map/design.md](ingress/inflight_map/design.md#inflightmap) |
| **ForwardTask** | The Ingress's handling of one client `/ping`: pick, publish, forward, clear, relay | [ingress/frontend/design.md](ingress/frontend/design.md#forwardtask) |
| **BenchmarkRunner** | Operator-side driver: setup over SSH, sweep execution, result download, plots | [benchmark_runner/design.md](benchmark_runner/design.md#run-flow) |
| **BenchConfig** | The validated TOML experiment configuration | [benchmark_runner/config/design.md](benchmark_runner/config/design.md#benchconfig) |
| **SweepExpansion** | Cross product of every array-valued program parameter except linked keys, in a fixed order | [benchmark_runner/config/design.md](benchmark_runner/config/design.md#sweepexpansion) |
| **SweepPoint** | One element of the SweepExpansion: the full argument lists of the three programs | [benchmark_runner/config/design.md](benchmark_runner/config/design.md#sweepexpansion) |
| **ProcessPlacement** | A program's worker-thread count and CPU set, applied when the runner starts it | [benchmark_runner/config/design.md](benchmark_runner/config/design.md#processplacement) |
| **CancelRateFraction** | Canceller rate given as a fraction of the same point's `global_rate` | [benchmark_runner/config/design.md](benchmark_runner/config/design.md#cancelratefraction) |
| **LinkedSweep** | An array parameter paired index by index with a swept driver key instead of crossed with it | [benchmark_runner/config/design.md](benchmark_runner/config/design.md#linkedsweep) |
| **RetryTimeoutCalculator** | Picks `retry_timeout_ms` per offered rate: the time T with Pr(sojourn > T) = EtaSlack target in an M/M/k model, and predicts whether the retries stay stable | [benchmark_runner/retry_timeout/design.md](benchmark_runner/retry_timeout/design.md#timeoutsolve) |
| **RetryTimeout** | The T computed by the RetryTimeoutCalculator for one rate | [benchmark_runner/retry_timeout/design.md](benchmark_runner/retry_timeout/design.md#timeoutsolve) |
| **EtaSlack** | Target Pr(sojourn > RetryTimeout) = max(0, min(1, η − ρ)) for a target load η; η = 1 is 1 − ρ | [benchmark_runner/retry_timeout/design.md](benchmark_runner/retry_timeout/design.md#etaslack) |
| **RetryFeedback** | The load ρ₀(1 + Pr(sojourn > T \| load r)) that retry copies at a fixed RetryTimeout T create | [benchmark_runner/retry_timeout/design.md](benchmark_runner/retry_timeout/design.md#retryfeedback) |
| **StableLoad** | Smallest fixed point of RetryFeedback below 1: where the load with retries settles | [benchmark_runner/retry_timeout/design.md](benchmark_runner/retry_timeout/design.md#retryfeedback) |
| **TippingLoad** | The unstable fixed point above the StableLoad: a load pushed past it grows beyond 1 (collapse) | [benchmark_runner/retry_timeout/design.md](benchmark_runner/retry_timeout/design.md#retryfeedback) |
| **MaxStableEta** | Largest η whose RetryFeedback still has a StableLoad, by binary search; an upper bound with no margin | [benchmark_runner/retry_timeout/design.md](benchmark_runner/retry_timeout/design.md#maxstableeta) |
| **Transport** | Run commands on, and copy files from, a machine over SSH (or locally) | [benchmark_runner/remote_transport/design.md](benchmark_runner/remote_transport/design.md#transport) |
| **SourcePin** | The exact branch and commit the runner deploys, checked against `repo_url` | [benchmark_runner/remote_setup/design.md](benchmark_runner/remote_setup/design.md#sourcepin) |
| **BuildManifest** | Per-implementation declarative file saying how to build and where the binaries are | [benchmark_runner/remote_setup/design.md](benchmark_runner/remote_setup/design.md#buildmanifest) |
| **ServerLifecycle** | Start, readiness wait and SIGINT stop of the server for one SweepPoint | [benchmark_runner/experiment_executor/design.md](benchmark_runner/experiment_executor/design.md#serverlifecycle) |
| **PointTable** | One row per SweepPoint with swept values and key metrics | [benchmark_runner/result_plotter/design.md](benchmark_runner/result_plotter/design.md#pointtable) |
| **SweepPlots** | The standard figures over all SweepPoints | [benchmark_runner/result_plotter/design.md](benchmark_runner/result_plotter/design.md#sweepplots) |
| **QueueLengthPlot** | Sweep figure of each point's summed WorkQueue length over time, one line per SweepPoint | [benchmark_runner/result_plotter/design.md](benchmark_runner/result_plotter/design.md#queuelengthplot) |
| **ThroughputLatencyPlot** | Latency percentiles against measured goodput, one line per series, points in swept order | [benchmark_runner/result_plotter/design.md](benchmark_runner/result_plotter/design.md#throughputlatencyplot) |
| **CancelOfferedFraction** | A point's canceller `cancel_rate` divided by its `global_rate` (PointTable column `cx_offered_fraction`) | [benchmark_runner/result_plotter/design.md](benchmark_runner/result_plotter/design.md#cancelofferedfraction) |
| **ThroughputLatencyNormalizedPlot** | ThroughputLatencyPlot with goodput scaled by `1 / (1 - CancelOfferedFraction)` | [benchmark_runner/result_plotter/design.md](benchmark_runner/result_plotter/design.md#throughputlatencynormalizedplot) |
| **FigurePage** | One interactive figure of a run, `plots/<name>.html`, that opens offline | [benchmark_runner/result_plotter/design.md](benchmark_runner/result_plotter/design.md#sweepplots) |
| **Availability** | Over a 1 s window of send times, the share of the client's finished requests that were `ok` within `slo_ms` of being sent | [benchmark_runner/result_plotter/design.md](benchmark_runner/result_plotter/design.md#availabilityplot) |
| **FailureTime** | A point's `freeze_after_ms` (FreezeTarget or server parameter): the StartSlackHistogram splits requests by first send before or after it | [benchmark_runner/result_plotter/design.md](benchmark_runner/result_plotter/design.md#startslackhistogram) |
| **ExpectedWaitPlot** | Per backend, `(outstanding + 1) × EwmaExecTime` at each pick over time, one subplot per backend (`expected_wait.html`) | [benchmark_runner/result_plotter/design.md](benchmark_runner/result_plotter/design.md#expectedwaitplot) |
| **AvailabilityPlot** | Availability over time, one line per SweepPoint, with the GcOverlay (`availability.html`) | [benchmark_runner/result_plotter/design.md](benchmark_runner/result_plotter/design.md#availabilityplot) |
| **StartSlackHistogram** | Distribution of the requests' StartSlack per SweepPoint, as fractions per 100 ms bin (`start_slack.html`); all requests, and with a FailureTime also those sent before and after it | [benchmark_runner/result_plotter/design.md](benchmark_runner/result_plotter/design.md#startslackhistogram) |
| **GcTrace** | A runtime's own one-line-per-cycle garbage-collection trace, with times relative to the process start | [server/design.md](server/design.md#processstart) |
| **GcTraceHook** | The BuildManifest keys `gc_trace_env` and `gc_trace_regex`: how to switch a language's GcTrace on and read its lines | [benchmark_runner/remote_setup/design.md](benchmark_runner/remote_setup/design.md#buildmanifest) |
| **GcEvent** | One GC cycle of a server on the wall clock (`server_<k>.gc.csv`): start time and the three phase durations | [benchmark_runner/experiment_executor/design.md](benchmark_runner/experiment_executor/design.md#gcevents) |
| **GcOverlay** | GcEvents drawn as vertical lines on the forward latency figures | [benchmark_runner/result_plotter/design.md](benchmark_runner/result_plotter/design.md#gcoverlay) |
| **LbImbalance** | Largest over smallest per-backend `forwarded` count of a point (PointTable column `lb_imbalance`) | [benchmark_runner/result_plotter/design.md](benchmark_runner/result_plotter/design.md#lbimbalance) |
| **IngressPlacement** | Which experiment machine runs the Ingress: `client` (default) or `server` | [benchmark_runner/config/design.md](benchmark_runner/config/design.md#ingressplacement) |
| **ServerMachines** | The one or more machines that run the server instances, each with `[server] instances` of them; instance ids are global | [benchmark_runner/config/design.md](benchmark_runner/config/design.md#servermachines) |

See [api.md](api.md) for the external interface and [design.md](design.md) for the component breakdown.
