# Metrics — API

## Types

### `RequestRecord`

| Field | Type | Meaning |
|---|---|---|
| `request_id` | u64 | RequestId |
| `client_id` | u32 | Client index |
| `seq` | u32 | Per-client arrival index |
| `outcome` | `ok` \| `rejected` \| `cancelled` \| `dropped` \| `error` \| `not_sent` | See [../client/api.md#outcome](../client/api.md#outcome) |
| `http_status` | integer or empty | Status received. Empty for `cancelled`, `dropped`, `not_sent`, and transport errors |
| `scheduled_ns` | integer | Scheduled arrival time |
| `send_ns` | integer or empty | When the request's first RequestCopy was sent. Empty for `not_sent` |
| `end_ns` | integer or empty | When the request resolved. Empty for `not_sent` |
| `copies` | u32 | RequestCopies the client started, `1 ..= 1 + max_retries`; `0` for `not_sent` ([../client/design.md#retrytimer](../client/design.md#retrytimer)) |
| `in_stall_window` | boolean | First sent in the StallWindow (`send_ns < initial_stall_ns`); always false for `not_sent` ([../client/design.md#stallwindow](../client/design.md#stallwindow)) |

Derived: `e2e_latency_ns = end_ns - scheduled_ns` and `service_latency_ns = end_ns - send_ns`,
both empty for `not_sent`. See [design.md#requestrecord](design.md#requestrecord).

### `SummaryReport`

The record written as [Summary JSON](#summary-json). See [design.md#summary](design.md#summary).

## Functions

### `aggregate(records, config, run_end_ns, run_start_unix_ns: u64) -> SummaryReport`
Leaves out StallWindowRequests (counting them in `counts.stall_window`), counts outcomes, computes rates over
`duration_s - initial_stall_ms / 1000`, and computes latency statistics over `ok` records. See
[design.md#collection](design.md#collection), [design.md#summary](design.md#summary) and
[design.md#percentiles](design.md#percentiles).

### `write_requests_csv(path, records)`
Writes [Requests CSV](#requests-csv), with records sorted by `(client_id, seq)`. See
[design.md#requestrecord](design.md#requestrecord).

### `write_summary_json(path, report)`
Writes [Summary JSON](#summary-json). See [design.md#summary](design.md#summary).

## Outputs

### Requests CSV

UTF-8, comma-separated, `\n` line endings, one header line, rows sorted by `(client_id, seq)`:

```
request_id,client_id,seq,outcome,http_status,scheduled_ns,send_ns,end_ns,e2e_latency_ns,service_latency_ns,copies,in_stall_window
```

`copies` and `in_stall_window` are last, so readers that find columns by header name keep working.
`in_stall_window` is written `1` or `0`.

Integers are written in decimal. Empty fields are written as nothing between the commas. One row
per [design.md#requestrecord](design.md#requestrecord).

### Summary JSON

```
{
  "config":   { every LoadGenConfig parameter, by its parameter name },
  "run_end_s": float,
  "run_start_unix_ns": integer,
  "counts":   { "scheduled", "sent", "ok", "rejected", "cancelled", "dropped", "error", "not_sent",
                "copies_sent", "stall_window" },
  "rates_per_s": { "offered", "sent", "goodput" },
  "e2e_latency_us":     { "count", "mean", "p50", "p90", "p99", "p999", "max" },
  "service_latency_us": { "count", "mean", "p50", "p90", "p99", "p999", "max" }
}
```

Definitions are in [design.md#summary](design.md#summary). `run_start_unix_ns` is the wall-clock time (Unix epoch,
ns) of the Run Controller's `run_start`, the origin of every `*_ns` field of the Requests CSV, so tools can place
those times next to other processes' wall-clock times.

## Uses

- [../client/api.md](../client/api.md#outcome): `Outcome`
- [../api.md](../api.md#loadgenconfig): `LoadGenConfig`
