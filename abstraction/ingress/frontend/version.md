# Version — ingress/frontend

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-11-02T00:00:00Z | Initial spec: client connections, ForwardTask (pick, publish, forward, clear before relay, finish), reset propagation, StartOnce, state machine |
| 0.2.0 | 2026-11-10T00:00:00Z | InFlightMap publish/clear by entry (keyed by RequestId); many concurrent ForwardTasks per client |
| 0.2.1 | 2026-11-11T00:00:00Z | ForwardTask keeps the ForwardTicket from `pick` and gives it to `finish` (Load Balancer 0.2.0) |
