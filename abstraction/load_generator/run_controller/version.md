# Version — load_generator/run_controller

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-10-30T00:00:00Z | Initial spec: validation, startup, DrainPhase, shutdown ordering |
| 0.1.1 | 2026-10-30T00:00:00Z | API style: Functions section, design links for `main`. No behavior change |
| 0.2.0 | 2026-11-10T00:00:00Z | Validation of `n_waiting_requests` (`1 ..= MAX_WAITING_REQUESTS`); DrainPhase with many outstanding requests per client, same end bound; stream-limit note |
| 0.3.0 | 2026-11-12T00:00:00Z | Reads `run_start_unix_ns` (wall clock) with `run_start` and passes it to `aggregate` |
