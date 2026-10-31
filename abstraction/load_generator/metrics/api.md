# Metrics — API

## Types

### `RequestRecord`

| Field | Type | Meaning |
|---|---|---|
| `request_id` | u64 | RequestId |
| `client_id` | u32 | Client index |
| `seq` | u32 | Per-client arrival index |
| `outcome` | `ok` \| `rejected` \| `cancelled` \| `error` \| `not_sent` | See [../client/api.md#outcome](../client/api.md#outcome) |
| `http_status` | integer or empty | Status received. Empty for `cancelled`, `not_sent`, and transport errors |
| `scheduled_ns` | integer | Scheduled arrival time |
| `send_ns` | integer or empty | When the request was sent. Empty for `not_sent` |
| `end_ns` | integer or empty | When the request resolved. Empty for `not_sent` |

Derived: `e2e_latency_ns = end_ns - scheduled_ns` and `service_latency_ns = end_ns - send_ns`,
both empty for `not_sent`. See [design.md#requestrecord](design.md#requestrecord).

### `SummaryReport`

The record written as [Summary JSON](#summary-json). See [design.md#summary](design.md#summary).

## Functions

### `aggregate(records, config, run_end_ns) -> SummaryReport`
Counts outcomes, computes rates over `duration_s`, and computes latency statistics over `ok` records. See
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
request_id,client_id,seq,outcome,http_status,scheduled_ns,send_ns,end_ns,e2e_latency_ns,service_latency_ns
```

Integers are written in decimal. Empty fields are written as nothing between the commas. One row
per [design.md#requestrecord](design.md#requestrecord).

### Summary JSON

```
{
  "config":   { every LoadGenConfig parameter, by its parameter name },
  "run_end_s": float,
  "counts":   { "scheduled", "sent", "ok", "rejected", "cancelled", "error", "not_sent" },
  "rates_per_s": { "offered", "sent", "goodput" },
  "e2e_latency_us":     { "count", "mean", "p50", "p90", "p99", "p999", "max" },
  "service_latency_us": { "count", "mean", "p50", "p90", "p99", "p999", "max" }
}
```

Definitions are in [design.md#summary](design.md#summary).

## Uses

- [../client/api.md](../client/api.md#outcome): `Outcome`
- [../api.md](../api.md#loadgenconfig): `LoadGenConfig`
