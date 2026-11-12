# Version — ingress

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-11-02T00:00:00Z | New component: parameters, BackendStats, outputs, lifecycle, shutdown, time base. Children: inflight_map 0.1.0 |
| 0.1.1 | 2026-11-02T00:00:00Z | Child added: load_balancer 0.1.0 |
| 0.1.2 | 2026-11-02T00:00:00Z | Child added: upstream_pool 0.1.0 |
| 0.1.3 | 2026-11-02T00:00:00Z | Child added: load_canceller 0.3.0 (moved from the root) |
| 0.1.4 | 2026-11-02T00:00:00Z | Child added: frontend 0.1.0 |
| 0.1.5 | 2026-11-02T00:00:00Z | Parameters restated from the root `api.md` (Ingress parameters) |
| 0.1.6 | 2026-11-04T00:00:00Z | InFlightMap key-space wording; child bump: inflight_map 0.2.0 (library-backed lock-free map contract) |
| 0.1.7 | 2026-11-10T00:00:00Z | The server binds connections but no longer serves them serially (shared WorkQueue): design rationale and Uses wording. Child bump: upstream_pool 0.1.1. No behavior change |
| 0.1.8 | 2026-11-10T00:00:00Z | Parameter `initial_stall_ms` (default 1000) restated and validated. Child bump: load_canceller 0.4.0 |
| 0.1.9 | 2026-11-10T00:00:00Z | Many forwards per client at once; InFlightMap keyed by RequestId. Child bump: inflight_map 0.3.0 |
| 0.1.10 | 2026-11-10T00:00:00Z | Child bump: frontend 0.2.0 |
| 0.1.11 | 2026-11-10T00:00:00Z | Child bump: load_canceller 0.5.0 |
| 0.1.12 | 2026-11-10T00:00:00Z | Child bump: upstream_pool 0.1.2 |
| 0.1.13 | 2026-11-11T00:00:00Z | LbPolicy `ewma` and parameter `ewma_beta`; BackendStats and summary `backends` gain `ewma_latency_ns`. Child bump: load_balancer 0.2.0 |
