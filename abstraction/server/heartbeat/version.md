# Version — server/heartbeat

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-11-20T00:00:00Z | Initial spec: HeartbeatSender answers `GET /heartbeat` with a HeartbeatLine every `heartbeat_interval_ms`, passing `worker_pool.freeze_wait` before each line, so a frozen server stops beating; worked example; edge cases |
| 0.2.0 | 2026-11-27T00:00:00Z | StalledState: `freeze_wait` also blocks while a ServiceScenario stalls; the next line is written at once when the stall ends, `seq` continuing; worked example; edge cases (permanent stall, shutdown during a stall) |
