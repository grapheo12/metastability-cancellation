# Version — server/cancel_tracker

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-10-30T00:00:00Z | Initial spec: CancelRegistry, CancelInbox, CancelMatching, CancelCounters, handle_cancel |
| 0.1.1 | 2026-10-30T00:00:00Z | API style: constants, `ConnectionCancelState` and counter functions link their design anchors. No behavior change |
| 0.2.0 | 2026-11-02T00:00:00Z | FinishedWatermark: per-slot `finished` value, `registry_publish_finished`, `registry_finished`. `handle_cancel` answers `202` with a CancelReplyBody in both modes |
| 0.3.0 | 2026-11-10T00:00:00Z | Shared workers: ClientCancelState (per-client lock, `last_completed_id`, `pending`) moves into the registry slot; `ConnectionCancelState` keeps only the binding. New `registry_client`, `ClientSlot`; `registry_drain`, `should_cancel` and `registry_publish_finished` take a slot, run under the lock, and use max-updates. New section "Overlap and ordering" with a worked example |
| 0.4.0 | 2026-11-10T00:00:00Z | Exact per-request CancelMatching: ClientCancelState gains `unfinished`, the RecentFinished ring (`RECENT_FINISHED` = 4096) and `finish_count`, loses `last_completed_id`; new `registry_start`; `resolve`; `registry_publish_finished` resolves a pending id and ages out stale ones; "Out-of-order requests" replaces "Overlap and ordering"; worked examples |
