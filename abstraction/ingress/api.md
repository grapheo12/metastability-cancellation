# Ingress — API

## Program parameters

Same as [../api.md#ingress-parameters](../api.md#ingress-parameters). Each is passed as `--<name> <value>`. Invalid
or missing required parameters make the program exit non-zero with a one-line error before any network activity
([design.md#configuration-validation](design.md#configuration-validation)).

| Parameter | Type / unit | Default | Meaning |
|---|---|---|---|
| `listen_addr` | `host:port` | `0.0.0.0:9090` | Address the Frontend accepts client connections on |
| `backends` | comma-separated list of `host:port`, at least 1, at most `MAX_BACKENDS` | required | The server instances. A backend's `server_id` is its 0-based position in this list |
| `num_clients` (`N`) | integer, `1 ..= MAX_CLIENTS` | required | Number of load-generator clients. Client ids must be `< N`. Bounds the InFlightMap key space |
| `lb_policy` | `round_robin` \| `least_outstanding` \| `power_of_two` \| `ewma` | `least_outstanding` | The LoadBalancer policy |
| `lb_filter` | `none` \| `deadline_mean` \| `deadline_mean_per_worker` \| `deadline_mean_queue_plus_share` \| `deadline_mmk` | `none` | DeadlineFilter strategy: send a request only to backends where `(outstanding + 1) × EwmaExecTime` (`deadline_mean`), that divided by the WorkerCount (`deadline_mean_per_worker`), or `(outstanding + 1/WorkerCount) × EwmaExecTime` (`deadline_mean_queue_plus_share`) fits in its time left, or where as an M/M/k queue it finishes in time with probability `>= 0.99` (`deadline_mmk`); otherwise it waits in the FilterWaitQueue, re-checked every 10 ms, and is dropped with `503` once its RequestDeadline has passed. `none`: off ([load_balancer/design.md](load_balancer/design.md#deadlinefilter)) |
| `ewma_beta` | float, `0 <= ewma_beta <= 1` | `0.7` | Weight of the old value in each EwmaLatency update; used by `lb_policy = ewma`, validated always ([load_balancer/design.md](load_balancer/design.md#ewmalatency)) |
| `cancel_rate` | float, cancels/s, `>= 0` | `0` | Rate of the LoadCanceller's Poisson process. `0` disables the LoadCanceller |
| `duration_s` | float, s, `> 0` | required if `cancel_rate > 0`, else ignored | Length of the LoadCanceller's sending phase |
| `initial_stall_ms` | integer, ms, `>= 0` | `1000` | The LoadCanceller schedules no cancel earlier than this after the Ingress's first `/ping`; sending still ends at `duration_s`. `0`: no stall ([load_canceller/design.md](load_canceller/design.md#cancelschedule)) |
| `heartbeat_timeout_ms` | integer, ms, `>= 0` | `0` | Heartbeat Monitor: a backend whose last HeartbeatLine is this old gets no new requests. `0`: off, every backend is used ([heartbeat_monitor/design.md](heartbeat_monitor/design.md#backendliveness)) |
| `lb_retry_policy` | `none` \| `timeout` | `none` | RetryPolicy: when the Ingress retries a forward itself (an LbRetry), consulted when the newest attempt fails and on a tick ([retry_policy/design.md](retry_policy/design.md#retrypolicyregistry)) |
| `lb_max_retries` | integer, `>= 0` | `0` | Most LbRetries per ForwardTask (one client stream). `0`: no LB retries whatever the policy ([retry_policy/design.md](retry_policy/design.md#retrypolicy)) |
| `lb_retry_timeout_ms` | float, ms, `> 0` | `150` | `timeout` RetryPolicy: start an LbRetry once the newest attempt has run this long without an answer ([retry_policy/design.md](retry_policy/design.md#retrypolicyregistry)) |
| `seed` | unsigned 64-bit integer | `0` | Base seed for the LoadBalancer's and the LoadCanceller's random choices |
| `output_path` | path prefix | `ingress` | Outputs go to `<output_path>.cancels.csv`, `<output_path>.forwards.csv`, `<output_path>.liveness.csv`, `<output_path>.picks.csv` and `<output_path>.summary.json` |

## Constants

| Name | Value | Meaning |
|---|---|---|
| `MAX_CLIENTS` | `2^20` | Largest `num_clients`; bounds accepted client ids and any InFlightMap capacity hint |
| `MAX_BACKENDS` | `64` | Largest number of backends |
| `INGRESS_DRAIN_TIMEOUT_MS` | `2000` | Longest wait at shutdown for forwards still in progress ([design.md#shutdown](design.md#shutdown)) |

## Types

### `IngressConfig`

An immutable record of the parameters above after validation, with `backends` as a list of `host:port` strings
and `duration_ns = round(duration_s * 1e9)` when the LoadCanceller is enabled
([design.md#configuration-validation](design.md#configuration-validation)).

### `BackendStats`

One record per backend, kept by the LoadBalancer. The counters are atomic u64 starting at 0; `ewma_latency_ns` is
an atomic binary64 starting at `EWMA_INITIAL_LATENCY_NS` ([load_balancer/api.md](load_balancer/api.md#constants)).

| Field | Meaning |
|---|---|
| `outstanding` | Forwards to this backend in progress right now |
| `forwarded` | Forwards started |
| `ok` | Relayed `202` |
| `rejected` | Relayed `503` |
| `bad_request` | Relayed `400` |
| `bad_gateway` | Answered `502` (upstream failure) |
| `reset` | Ended because the client reset its stream |
| `ewma_latency_ns` | EwmaLatency: moving average of the latency of `ok` forwards, in ns (float), under every policy ([load_balancer/design.md](load_balancer/design.md#ewmalatency)) |
| `worker_count` | The backend's latest WorkerCount (`x-worker-threads` of its last `202`/`503`), `0` before its first answer; divides the DeadlineFilter's expected wait ([load_balancer/design.md](load_balancer/design.md#deadlinefilter)) |
| `ewma_exec_ns` | EwmaExecTime: moving average of the ExecutionTime the backend reported on `ok` and `rejected` forwards, in ns (float), or none before the first sample ([load_balancer/design.md](load_balancer/design.md#ewmaexectime)) |
| `ewma_anti_exec_ns` | EwmaAntiServiceTime: moving average of the ExecutionTime every **other** backend reported (the samples of their EwmaExecTime), in ns (float), or none before the first such sample ([load_balancer/design.md](load_balancer/design.md#ewmaantiservicetime)) |

## Behavior visible on the wire

- Speaks HTTP/2 over cleartext TCP with prior knowledge to clients and to backends.
- Serves `POST /ping` exactly as in [../api.md#wire-contract-ping](../api.md#wire-contract-ping): the backend's
  status and body are relayed unchanged. `502` with an empty body means the backend could not be reached or its
  stream failed ([../protocol/api.md](../protocol/api.md): `STATUS_BAD_GATEWAY`).
- A body that does not parse, or whose client id is `>= num_clients`, gets `400` with an empty body. Other paths
  get `404`, other methods on `/ping` get `405`. The Ingress does not serve `/cancel`.
- Requests on one client connection are handled concurrently. A load-generator client keeps up to
  `n_waiting_requests` outstanding, so a client can have that many forwards in progress at once.

## Outputs

### `<output_path>.cancels.csv`

The LoadCanceller's CancelRecords, one per arrival. The format is in
[load_canceller/api.md](load_canceller/api.md#output_pathcancelscsv). It is written even when the LoadCanceller is
disabled (header only).

### `<output_path>.forwards.csv`

The LoadBalancer's ForwardLog ([load_balancer/api.md#forwardrecord](load_balancer/api.md#forwardrecord)), one row
per finished forward. UTF-8, comma-separated, `\n` line endings, one header line:

```
t_ns,server_id,outcome,latency_ns,ewma_latency_ns,exec_ns,ewma_exec_ns,attempt
```

| Column | Type / unit | Meaning |
|---|---|---|
| `t_ns` | u64, ns | `end_ns - run_start`: when the forward finished, on the Ingress's time base ([design.md#time-base](design.md#time-base)) |
| `server_id` | u32 | The backend |
| `outcome` | `ok` \| `rejected` \| `bad_request` \| `bad_gateway` \| `reset` | How the forward ended |
| `latency_ns` | u64, ns | From `pick` to `finish` |
| `ewma_latency_ns` | float, ns | The backend's EwmaLatency right after this forward |
| `exec_ns` | u64, ns, or empty | The ExecutionTime the backend reported in the relayed body's ExecReport; empty unless `ok` or `rejected` with a valid one |
| `ewma_exec_ns` | float, ns, or empty | The backend's EwmaExecTime right after this forward; empty before its first sample |
| `attempt` | u32 | Which attempt of its ForwardTask this forward was: `0` for the first, `k` for the k-th LbRetry |

Rows are sorted by `t_ns`; rows with equal `t_ns` keep their ForwardLog order. Integers are decimal. `ewma_latency_ns`
and `ewma_exec_ns` are written in decimal without an exponent, as the shortest string that reads back as the same binary64 (for example
`1300000` or `850000.5`). The file is written at shutdown, header only when no `/ping` was ever forwarded
([design.md#shutdown](design.md#shutdown)).

### `<output_path>.liveness.csv`

The Heartbeat Monitor's LivenessLog, one row per change of a backend's liveness plus one per backend at start. The
format is in [heartbeat_monitor/api.md](heartbeat_monitor/api.md#output_pathlivenesscsv). Header only when the
monitor is off. Written at shutdown ([design.md#shutdown](design.md#shutdown)).

### `<output_path>.picks.csv`

The LoadBalancer's PickLog ([load_balancer/api.md#pickrecord](load_balancer/api.md#pickrecord)): one row per
backend per `/ping` picked (forwarded or dropped by the DeadlineFilter). UTF-8, comma-separated, `\n` line endings,
one header line:

```
t_ns,server_id,outstanding,ewma_exec_ns,worker_count
```

| Column | Type / unit | Meaning |
|---|---|---|
| `t_ns` | u64, ns | `t - run_start`: when the pick happened, on the Ingress's time base ([design.md#time-base](design.md#time-base)) |
| `server_id` | u32 | The backend |
| `outstanding` | integer | Its forwards in progress, not counting this pick's |
| `ewma_exec_ns` | float, ns, or empty | Its EwmaExecTime; empty before its first sample |
| `worker_count` | u32 | Its WorkerCount; `0` before its first answer |

Rows are sorted by `t_ns`; rows with equal `t_ns` keep their PickLog order (backends in id order within a pick).
`ewma_exec_ns` is written like `ewma_latency_ns` in `forwards.csv`. Header only when no `/ping` was picked. Written
at shutdown ([design.md#shutdown](design.md#shutdown)).

### `<output_path>.summary.json`

```
{
  "config":      { every IngressConfig parameter, by its parameter name; backends as an array of strings },
  "run_end_s":   float,
  "run_start_unix_ns": integer,
  "counts":      { "scheduled", "sent", "accepted", "rejected", "bad_request", "error", "timeout", "skipped" },
  "rates_per_s": { "offered", "sent", "accepted" },
  "clients_targeted": integer,
  "filter_dropped": integer,
  "filter_queued": integer,
  "filter_sent_from_queue": integer,
  "filter_queue_max": integer,
  "lb_retry_refused": integer,
  "lb_retries": integer,
  "backends": [ { "server_id", "addr", "forwarded", "ok", "rejected", "bad_request", "bad_gateway", "reset", "ewma_latency_ns", "ewma_exec_ns", "ewma_anti_exec_ns", "worker_count" }, ... ]
}
```

`counts`, `rates_per_s` and `clients_targeted` describe the LoadCanceller and keep the names they had when it was a
separate program, so result tools can read them unchanged (all zero when it is disabled). `filter_queued` counts the `/ping`s that waited in the FilterWaitQueue because no backend passed the DeadlineFilter, `filter_sent_from_queue` those later sent from it, `filter_dropped` those dropped once their RequestDeadline passed while waiting, and `filter_queue_max` the longest queue a scan saw ([load_balancer/design.md](load_balancer/design.md#filterwaitqueue)); all 0 with `lb_filter = none`. `lb_retry_refused` counts LbRetries the DeadlineFilter refused (no attempt started). `lb_retries` is the number of LbRetries started (attempts beyond each ForwardTask's first; 0 with `lb_retry_policy = none` or `lb_max_retries = 0`). `backends` is in
`server_id` order and holds the final BackendStats; `ewma_exec_ns` is `null` for a backend with no ExecutionTime sample, and `ewma_anti_exec_ns` for one with no sample from another backend. `run_start_unix_ns` is the wall-clock time (Unix epoch, ns) of
`run_start`, or `0` when no `/ping` was received ([design.md#time-base](design.md#time-base)). Written by [design.md#shutdown](design.md#shutdown).

## Lifecycle

- **Start:** validate the config, create the InFlightMap, the LoadBalancer and the UpstreamPool, bind
  `listen_addr`, start the Heartbeat Monitor if `heartbeat_timeout_ms > 0` (it dials every backend for
  `GET /heartbeat`), then print one line `listening on <addr>` to standard output. No `/ping` reaches a backend yet.
- **Run:** accept client connections until stopped. The LoadCanceller starts with the first `/ping`.
- **Stop:** on interrupt or termination signal, see [design.md#shutdown](design.md#shutdown). Exit `0` after the
  outputs are written.
- **Error:** an invalid config or a failed bind exits non-zero before accepting. A failure to write the outputs
  exits non-zero.

## Uses

- [../protocol/api.md](../protocol/api.md): `parse_body`, `parse_exec_report`, `split_request_id`, status constants, `PING_PATH`, `CANCEL_PATH`
- [../sampling/api.md](../sampling/api.md): `derive_seed`, `rng_new`, `exponential_ns`, `uniform_int`, `STREAM_CANCELLER`, `STREAM_INGRESS_LB`
- [../server/api.md](../server/api.md): the backends' wire behavior (one client per connection, the HeartbeatStream)

## Subcomponents

| Subcomponent | Spec |
|---|---|
| In-Flight Map | [inflight_map/api.md](inflight_map/api.md) |
| Load Balancer | [load_balancer/api.md](load_balancer/api.md) |
| Upstream Pool | [upstream_pool/api.md](upstream_pool/api.md) |
| Load Canceller | [load_canceller/api.md](load_canceller/api.md) |
| Frontend | [frontend/api.md](frontend/api.md) |
| Heartbeat Monitor | [heartbeat_monitor/api.md](heartbeat_monitor/api.md) |
| Retry Policy | [retry_policy/api.md](retry_policy/api.md) |
