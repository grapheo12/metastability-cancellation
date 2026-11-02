# Version — load_canceller

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-10-30T00:00:00Z | Initial spec: CancelSchedule, CancelTargetChoice, response collection, CancellerDrain, outputs |
| 0.1.1 | 2026-10-30T00:00:00Z | API style: `CancellerConfig` and `CancelRecord` under Types, design links for `CancelRecord` and the CSV. No behavior change |
| 0.2.0 | 2026-11-02T00:00:00Z | Reply-guided CancelTargetChoice: target = reported FinishedWatermark seq + step in `[1, incr_rand_max]` (`note_finished` keeps the max). New parameter `incr_rand_max` (default 4) replaces the constant `INCR_RAND_MAX`. CancelRecord and CSV gain `reported_finished` |
