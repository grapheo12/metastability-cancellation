# Version — ingress/load_balancer

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-11-02T00:00:00Z | Initial spec: LbPolicy (round_robin, least_outstanding, power_of_two), pick/finish, BackendStats, races, worked examples |
| 0.2.0 | 2026-11-11T00:00:00Z | LbPolicy `ewma` (EwmaScore over the power_of_two pair), EwmaLatency with `ewma_beta` and `EWMA_INITIAL_LATENCY_NS`, ForwardTicket returned by `pick` and given to `finish`, `ewma_latency_ns` in the snapshot |
