# Version — server/cancel_tracker

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-10-30T00:00:00Z | Initial spec: CancelRegistry, CancelInbox, CancelMatching, CancelCounters, handle_cancel |
| 0.1.1 | 2026-10-30T00:00:00Z | API style: constants, `ConnectionCancelState` and counter functions link their design anchors. No behavior change |
| 0.2.0 | 2026-11-02T00:00:00Z | FinishedWatermark: per-slot `finished` value, `registry_publish_finished`, `registry_finished`. `handle_cancel` answers `202` with a CancelReplyBody in both modes |
