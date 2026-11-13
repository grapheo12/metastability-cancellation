# Version — load_generator/metrics

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-10-30T00:00:00Z | Initial spec: RequestRecord, aggregation, CSV/JSON output, nearest-rank percentiles |
| 0.1.1 | 2026-10-30T00:00:00Z | API style: Types / Functions / Outputs / Uses sections, `SummaryReport` type, every function documented with design links. No behavior change |
| 0.2.0 | 2026-11-12T00:00:00Z | Summary JSON `run_start_unix_ns` (wall clock of `run_start`), passed to `aggregate` |
| 0.3.0 | 2026-11-12T00:00:00Z | RequestRecord field `copies` (last CSV column); outcome `dropped`; `send_ns` is the first copy's send; summary counts `dropped` and `copies_sent` |
| 0.4.0 | 2026-11-12T00:00:00Z | RequestRecord `in_stall_window` (last CSV column); `aggregate` leaves StallWindowRequests out of every count, rate and latency statistic and reports them as `counts.stall_window`; rates divide by `duration_s - initial_stall_ms / 1000` |
