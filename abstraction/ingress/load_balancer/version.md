# Version — ingress/load_balancer

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-11-02T00:00:00Z | Initial spec: LbPolicy (round_robin, least_outstanding, power_of_two), pick/finish, BackendStats, races, worked examples |
| 0.2.0 | 2026-11-11T00:00:00Z | LbPolicy `ewma` (EwmaScore over the power_of_two pair), EwmaLatency with `ewma_beta` and `EWMA_INITIAL_LATENCY_NS`, ForwardTicket returned by `pick` and given to `finish`, `ewma_latency_ns` in the snapshot |
| 0.3.0 | 2026-11-12T00:00:00Z | ForwardLog: every `finish` appends a ForwardRecord (end_ns, server_id, outcome, latency_ns, ewma_latency_ns after it) under a short lock; `ewma_update` returns the stored value; `forward_log` copy for the Ingress; worked example |
| 0.4.0 | 2026-11-20T00:00:00Z | LiveSet: `choose` runs every policy over the live backends (`heartbeat_monitor.is_live`), falling back to all when none is live; `random_pair` over the LiveSet; `lb_new` takes the monitor; unchanged without a monitor; worked example |
| 0.5.0 | 2026-11-20T00:00:00Z | EwmaExecTime: `finish` takes the backend's `exec_ns`; per-backend `ewma_exec_ns` (first sample starts it, `EXEC_NO_SAMPLE` sentinel, same `beta`, CAS loop) updated on `ok`/`rejected`; ForwardRecord `exec_ns`, `ewma_exec_ns`; worked example |
| 0.6.0 | 2026-11-20T00:00:00Z | DeadlineFilter (`lb_filter = deadline`): `pick(lb, remaining_ns)` keeps the live backends with `(outstanding + 1) × EwmaExecTime <= remaining_ns` (no sample: eligible), the LbPolicy chooses among them, none left: `Dropped` and `filter_dropped += 1`; worked example |
| 0.7.0 | 2026-11-20T00:00:00Z | DeadlineFilter divides the expected wait by the backend's WorkerCount (`worker_count`, stored by `finish` from each relayed `202`/`503`; unknown: 1); `finish` takes `worker_count`; worked example rescaled |
