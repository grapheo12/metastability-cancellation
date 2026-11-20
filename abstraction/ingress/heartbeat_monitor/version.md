# Version — ingress/heartbeat_monitor

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-11-20T00:00:00Z | Initial spec: HeartbeatReader per backend (own connection, `GET /heartbeat`, redial after `HEARTBEAT_REDIAL_MS = 100`); BackendLiveness checker every `HEARTBEAT_CHECK_MS = 50` (`live` while the last line is younger than `heartbeat_timeout_ms`; `0` = off); `is_live`; LivenessLog and `<output_path>.liveness.csv`; worked example |
