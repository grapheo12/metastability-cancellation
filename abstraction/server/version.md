# Version — server

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-10-30T00:00:00Z | Initial spec: listener, config validation, cancellation semantics, shutdown |
| 0.2.0 | 2026-10-30T00:00:00Z | `/cancel` endpoint, new Cancel Tracker subcomponent, CancelCounters output, `/cancel` cancellation semantics, connection binding to client id. Children: connection_handler, ping_handler, cpu_burner 0.2.0; cancel_tracker 0.1.0 |
| 0.2.1 | 2026-10-30T00:00:00Z | API style: `ServerConfig` under Types. Children: cancel_tracker 0.1.1, connection_handler 0.2.1, cpu_burner 0.2.1, ping_handler 0.2.1. No behavior change |
| 0.2.2 | 2026-11-02T00:00:00Z | Child bump: cancel_tracker 0.2.0. Cancellation semantics: every `/cancel` `202` carries the FinishedWatermark |
| 0.2.3 | 2026-11-02T00:00:00Z | Child bump: ping_handler 0.3.0 |
