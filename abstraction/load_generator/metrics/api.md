# Metrics — API

## `RequestRecord`

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
both empty for `not_sent`.

## `aggregate(records, config, run_end_ns) -> SummaryReport`
## `write_requests_csv(path, records)`
## `write_summary_json(path, report)`

## Requests CSV

UTF-8, comma-separated, `\n` line endings, one header line, rows sorted by `(client_id, seq)`:

```
request_id,client_id,seq,outcome,http_status,scheduled_ns,send_ns,end_ns,e2e_latency_ns,service_latency_ns
```

Integers are written in decimal. Empty fields are written as nothing between the commas.

## Summary JSON

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
