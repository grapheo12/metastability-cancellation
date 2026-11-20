# Version — server/heartbeat

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-11-20T00:00:00Z | Initial spec: HeartbeatSender answers `GET /heartbeat` with a HeartbeatLine every `heartbeat_interval_ms`, passing `worker_pool.freeze_wait` before each line, so a frozen server stops beating; worked example; edge cases |
