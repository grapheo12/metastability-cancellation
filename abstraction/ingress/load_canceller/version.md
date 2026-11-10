# Version — load_canceller

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-10-30T00:00:00Z | Initial spec: CancelSchedule, CancelTargetChoice, response collection, CancellerDrain, outputs |
| 0.1.1 | 2026-10-30T00:00:00Z | API style: `CancellerConfig` and `CancelRecord` under Types, design links for `CancelRecord` and the CSV. No behavior change |
| 0.2.0 | 2026-11-02T00:00:00Z | Reply-guided CancelTargetChoice: target = reported FinishedWatermark seq + step in `[1, incr_rand_max]` (`note_finished` keeps the max). New parameter `incr_rand_max` (default 4) replaces the constant `INCR_RAND_MAX`. CancelRecord and CSV gain `reported_finished` |
| 0.3.0 | 2026-11-02T00:00:00Z | Moved into the Ingress (no longer its own program). InFlightVictimChoice replaces CancelTargetChoice: uniform victim from the InFlightMap snapshot, `/cancel` on a connection per backend. Schedule from the Ingress run_start; `canceller_start`/`canceller_stop`. CancelRecord gains `server_id`; `incr_rand_max` and `reported_finished` removed |
| 0.4.0 | 2026-11-10T00:00:00Z | Initial stall: CancelSchedule starts at `S = initial_stall_ns` (same draws, shifted), still ends at `D`; worked example |
| 0.5.0 | 2026-11-10T00:00:00Z | InFlightVictimChoice is uniform over in-flight requests (snapshot has one entry per request); worked example with several requests per client |
