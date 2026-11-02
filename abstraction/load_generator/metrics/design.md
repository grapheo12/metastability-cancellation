# Metrics — Design

## RequestRecord

Each client creates its records and keeps them in its own list. There is no shared collector during the
run. The Run Controller concatenates the lists after every client has returned. Before writing,
records are sorted by `(client_id, seq)`.

## Collection

```
function aggregate(records, config, run_end_ns):
    D_s = config.duration_s
    counts.scheduled = len(records)
    counts[o] = number of records with outcome o, for each outcome o
    counts.sent = counts.scheduled - counts.not_sent
    rates.offered = counts.scheduled / D_s
    rates.sent    = counts.sent / D_s
    rates.goodput = counts.ok / D_s
    ok = records with outcome == ok
    e2e     = [ (r.end_ns - r.scheduled_ns) / 1000.0 for r in ok ]      # µs
    service = [ (r.end_ns - r.send_ns)      / 1000.0 for r in ok ]      # µs
    return SummaryReport(config, run_end_ns / 1e9, counts, rates,
                         latency_stats(e2e), latency_stats(service))
```

## Summary

- Rates divide by the configured `duration_s` (the active phase), not by `run_end_s`.
- **Latency statistics cover only `ok` requests.** Cancelled requests end at about `slo_ms` by
  construction, so their share is reported through `counts.cancelled` instead. All per-request
  latencies remain in the CSV for other analyses.

## Percentiles

Nearest-rank method:

```
function latency_stats(values):
    n = len(values)
    if n == 0: return { count: 0, mean: null, p50: null, p90: null, p99: null, p999: null, max: null }
    v = sort ascending(values)
    # p is given in thousandths of a percent (p50 -> 50000, p999 -> 99900) so that the
    # rank is computed with exact integer arithmetic: rank = ceil(p_milli * n / 100000)
    pct(p_milli) = v[ (p_milli * n + 99999) div 100000 - 1 ]
    return { count: n, mean: sum(v) / n,
             p50: pct(50000), p90: pct(90000), p99: pct(99000), p999: pct(99900), max: v[n-1] }
```

Worked example: `n = 10` gives `p50 = v[4]`, `p90 = v[8]`, `p99 = v[9]`, `p999 = v[9]`.

## Design specific to Go

Implementation: `implementation/go` (module `testbed`, Go 1.21, `golang.org/x/net v0.17.0`).
Build and test with `CGO_ENABLED=0 go build ./...` / `CGO_ENABLED=0 go test ./...`. On recent macOS the dynamic
loader rejects cgo-linked Go 1.21 binaries (missing `LC_UUID`), so cgo is disabled.

### Go choices

- File `internal/loadgen/metrics.go`.
- Empty CSV fields: `HTTPStatus == 0` means empty, and `Sent == false` blanks `send_ns`, `end_ns` and both latencies.
- `null` statistics are `*float64` set to `nil`. JSON field order comes from struct field order (`encoding/json`).
- The percentile rank uses Go `int` arithmetic: `(pMilli*n + 99999) / 100000 - 1`.

### Traceability

Generated from the `// Design:` tags in the code by `implementation/spec_tools/tracegen.py` (see `AGENTS.md`). Each link points at the first code line after the tag.
Regenerate from the repository root with `python3 implementation/spec_tools/tracegen.py --lang Go --ext .go --comment // --write`.

<!-- trace:begin -->
| Design subsection | Implementation (file:line) |
|---|---|
| [RequestRecord](#requestrecord) | [metrics.go:16](../../../implementation/go/internal/loadgen/metrics.go#L16), [metrics.go:141](../../../implementation/go/internal/loadgen/metrics.go#L141) |
| [Collection](#collection) | [metrics.go:82](../../../implementation/go/internal/loadgen/metrics.go#L82), [metrics_test.go:23](../../../implementation/go/internal/loadgen/metrics_test.go#L23) |
| [Summary](#summary) | [metrics.go:82](../../../implementation/go/internal/loadgen/metrics.go#L82) |
| [Percentiles](#percentiles) | [metrics.go:118](../../../implementation/go/internal/loadgen/metrics.go#L118), [metrics_test.go:11](../../../implementation/go/internal/loadgen/metrics_test.go#L11) |
<!-- trace:end -->
