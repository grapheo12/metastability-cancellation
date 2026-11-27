# Ingress — Design

## Structure

```mermaid
flowchart LR
  C[Load-generator clients] -->|"h2c, POST /ping"| FE[Frontend: one handler per stream]
  FE -->|pick| LB[Load Balancer: lb_policy + BackendStats]
  FE -->|"publish / clear"| IM[(In-Flight Map)]
  FE -->|"forward, wait"| UP[Upstream Pool: conn per client x backend]
  LC[Load Canceller: single sender] -->|snapshot| IM
  LC -->|"DELETE /cancel, own conn per backend"| S0[server 0]
  UP --> S0
  UP --> S1[server 1 ...]
  LC --> S1
  HM[Heartbeat Monitor: reader per backend + checker] -->|"GET /heartbeat, own conn per backend"| S0
  HM --> S1
  LB -->|is_live| HM
  FE -->|"decide (LbRetry)"| RP[Retry Policy]
```

| Subcomponent | Responsibility | Spec |
|---|---|---|
| In-Flight Map | Lock-free `request_id → InFlightEntry` of the forwards in progress, any number per client | [inflight_map/description.md](inflight_map/description.md) |
| Load Balancer | LbPolicy (`pick`), BackendStats (`finish`) | [load_balancer/description.md](load_balancer/description.md) |
| Upstream Pool | One h2c connection per (client, backend), dialed once, dropped on connection error | [upstream_pool/description.md](upstream_pool/description.md) |
| Load Canceller | CancelSchedule from the first `/ping`, InFlightVictimChoice, `/cancel` on its own connection per backend | [load_canceller/description.md](load_canceller/description.md) |
| Frontend | Client connections, ForwardTask per `/ping`, reset propagation, StartOnce | [frontend/description.md](frontend/description.md) |
| Heartbeat Monitor | HeartbeatReader per backend, BackendLiveness (`is_live`), LivenessLog; off when `heartbeat_timeout_ms = 0` | [heartbeat_monitor/description.md](heartbeat_monitor/description.md) |
| Retry Policy | RetryPolicy `decide` for LbRetries, RetryPolicyRegistry; `none` never retries, `timeout` mimics the client RetryTimer | [retry_policy/description.md](retry_policy/description.md) |

## Shared state

One of each per process, created at startup and never replaced:

| State | Owner | Shared with |
|---|---|---|
| `config: IngressConfig` | Startup | everyone, read only |
| `inflight: InFlightMap`, with client ids bounded by `num_clients` | In-Flight Map | Frontend (publish, clear), Load Canceller (snapshot) |
| `stats: array[B] of BackendStats` | Load Balancer | Frontend (outcomes), Shutdown (summary) |
| ForwardLog | Load Balancer | Frontend (one append per `finish`), Shutdown (`forwards.csv`) |
| PickLog | Load Balancer | Frontend (`B` appends per `pick`), Shutdown (`picks.csv`) |
| `run_start` | set once, by the first `/ping` | Load Canceller (time base) |
| `monitor: HeartbeatMonitor` or none | Heartbeat Monitor | Load Balancer (`is_live`), Retry Policy (`is_live`, through `attach`), Shutdown (`liveness.csv`) |
| `retry_policy: RetryPolicy` | Retry Policy, built by `retry_policy_new` at startup, then `retry_policy.attach(retry_policy, is_live)` (optional) before the first request | Frontend (`decide`, `observe`), Shutdown (`stop`) |

`B` is the number of backends.

## Configuration validation

```
function validate(params):
    listen_addr parses as host:port                         else error
    backends = params.backends split on ","; each element trimmed
    1 <= len(backends) <= MAX_BACKENDS                      else error
    every backend parses as host:port                       else error "backend <i> must be host:port"
    1 <= num_clients <= MAX_CLIENTS                         else error
    lb_policy in {round_robin, least_outstanding, power_of_two, ewma, peak_ewma}  else error
    lb_filter in {none, deadline_mean, deadline_mean_per_worker, deadline_mean_queue_plus_share, deadline_mmk,
                  deadline_provisional_rtt}                 else error   # default none
    if lb_filter == deadline_provisional_rtt: lb_policy == peak_ewma
                                                            else error "lb_filter deadline_provisional_rtt needs lb_policy peak_ewma"
    if lb_policy == peak_ewma: lb_filter in {none, deadline_provisional_rtt}
                                                            else error "lb_policy peak_ewma needs lb_filter none or deadline_provisional_rtt"
    every OptionalValue given is realized here              else error "<parameter> <value> is not supported by this implementation"
    0 <= ewma_beta <= 1 and finite                          else error "ewma_beta must be a number in [0, 1]"
    cancel_rate >= 0 and finite                             else error
    if cancel_rate > 0: duration_s > 0 and finite           else error "duration_s is required with cancel_rate > 0"
    seed parses as u64                                      else error
    initial_stall_ms is an integer >= 0                     else error
    heartbeat_timeout_ms is an integer >= 0                 else error "heartbeat_timeout_ms must be an integer >= 0"
    lb_retry_policy names a RetryPolicy (retry_policy_new)  else error             # default none
    lb_max_retries is an integer in 0 ..= 2^32-1             else error             # default 0
    lb_retry_timeout_ms > 0 and finite                       else error             # default 150; lb_retry_timeout_ns = round(ms * 1e6)
    lb_retry_anxiety_ratio >= 0 and finite                   else error             # default 0.3
    lb_retry_late_factor >= 0 and finite                     else error             # default 10
    return IngressConfig(..., duration_ns = round(duration_s * 1e9) if cancel_rate > 0,
                         initial_stall_ns = initial_stall_ms * 1_000_000)
```

Duplicate backends are allowed: they are separate `server_id`s that happen to share an address.

## Concurrency model

- **Frontend:** every `/ping` stream is handled by its own coroutine or thread, which blocks while the backend
  works. A reader for each client connection keeps reading frames while forwards are in progress, so a client's
  `RST_STREAM` is seen at once and passed upstream.
- **Heartbeat Monitor:** one reader per backend, each blocked on its stream, and one checker on a timer. The
  Load Balancer only loads their atomic `live` flags.
- **Load Canceller:** one sender activity. Its `/cancel` responses are collected by the reader of its own
  connections, never by the sender.
- **Lock-free paths:** the In-Flight Map, the BackendStats counters and each EwmaLatency (a compare-and-swap loop)
  use only atomic operations. The Upstream Pool may lock while it creates a connection, but never while a request
  is in progress on one. The ForwardLog takes its lock only to append one record
  ([load_balancer/design.md#forwardlog](load_balancer/design.md#forwardlog)).
- If the implementation uses cooperative coroutines on one thread, nothing in the Ingress may busy-wait: every
  wait is on network I/O, a timer, or a lock held only briefly.
- **Ingress workers.** An implementation may run the Frontend on several workers (threads), each serving whole client
  connections. Only the PeakEwmaDeadline depends on them: it keeps one wait queue, retry list and scan timer per worker
  ([load_balancer/design.md#peakewmadeadline](load_balancer/design.md#peakewmadeadline)); every other structure is
  shared by all workers.

## Time base

`now()` is a monotonic clock. `run_start` is the monotonic time at which the Frontend receives the **first**
`/ping` whose body parses. The Load Canceller's schedule is measured from `run_start`, so it starts with the load
generator's traffic, not with the Ingress process. `run_end_s` in the summary is `(time of shutdown - run_start)`
in seconds, or `0` if no `/ping` was ever received.

When it sets `run_start`, the Frontend also reads the wall clock once, `run_start_unix_ns` (Unix epoch, ns). It is
only reported, in the summary, so that tools can place the Ingress's times (for example the `t_ns` of
`forwards.csv`) next to other processes' wall-clock times, such as a server's GcTrace. The Ingress never uses it
for timing. It is `0` if no `/ping` was ever received.

## Shutdown

```
procedure shutdown():                                  # on interrupt or termination signal
    stop accepting; send GOAWAY on every client connection
    deadline = now() + INGRESS_DRAIN_TIMEOUT_MS * 1e6
    wait until no forward is in progress (sum of stats[*].outstanding == 0) or now() >= deadline
    stop the Load Canceller if it is still sending, then run its drain
    retry_policy.stop(retry_policy)                         # optional: ends an observing policy's own activity
    close every upstream and canceller connection           # errors ignored
    liveness = heartbeat_monitor.monitor_stop(monitor) if monitor is not none else []
    write <output_path>.cancels.csv, <output_path>.forwards.csv, <output_path>.liveness.csv, <output_path>.picks.csv
          and <output_path>.summary.json
    exit 0
```

`forwards.csv` is the ForwardLog (`load_balancer.forward_log`) with `t_ns = end_ns - run_start` for every record,
sorted stably by `t_ns`. Every record has `end_ns >= run_start`: `run_start` is set before the first `pick`. If no
`/ping` was received, the log is empty and the file has only its header.

`picks.csv` is the PickLog (`load_balancer.pick_log`) with `t_ns = t - run_start`, sorted stably by `t_ns`; every
pick happens after `run_start` is set.

Forwards still in progress at the deadline are abandoned: their client streams are reset and they are counted in
neither `ok` nor `rejected`. The load generator finishes before the operator stops the Ingress, so in a normal run
nothing is in progress at shutdown.

## Why one upstream connection per client and backend

The server binds each connection to the client id of its first `/ping`
([../server/description.md](../server/description.md)). Giving each (client, backend) pair its own connection
keeps that rule true behind the Ingress without changing the server. A shared connection would mix client ids and
get `400`s. It also keeps each client's ServiceTime draws on that client's own connection stream, as if the client
had connected directly.
