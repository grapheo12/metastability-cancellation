# Metrics — Design

## RequestRecord

Each client creates its records and keeps them in its own list. There is no shared collector during the
run. The Run Controller concatenates the lists after every client has returned. Before writing,
records are sorted by `(client_id, seq)`.

`exec_ns` and `start_slack_ns` are copied from the ExecReport of the response that decided the outcome
([../client/design.md#outcome-classification](../client/design.md#outcome-classification)). They are only written to
the Requests CSV; `aggregate` does not use them, and the Summary JSON is unchanged.

## Collection

```
function aggregate(records, config, run_end_ns, run_start_unix_ns):
    D_s = config.duration_s - config.initial_stall_ms / 1000.0      # measured_s, > 0 by validation
    counts.stall_window = number of records with r.in_stall_window   # StallWindowRequests
    records = records with not r.in_stall_window                     # everything below ignores them
    counts.scheduled = len(records)
    counts[o] = number of records with outcome o, for each outcome o
    counts.sent = counts.scheduled - counts.not_sent
    counts.copies_sent = sum of r.copies over records     # RequestCopies, >= counts.sent
    rates.offered = counts.scheduled / D_s
    rates.sent    = counts.sent / D_s
    counts.ok_late = number of records with outcome == ok and r.end_ns - r.send_ns > config.slo_ns   # late answers
    rates.goodput = (counts.ok - counts.ok_late) / D_s                 # ok within the SLO only
    ok = records with outcome == ok
    e2e     = [ (r.end_ns - r.scheduled_ns) / 1000.0 for r in ok ]      # µs
    service = [ (r.end_ns - r.send_ns)      / 1000.0 for r in ok ]      # µs
    return SummaryReport(config, run_end_ns / 1e9, run_start_unix_ns, counts, rates,
                         latency_stats(e2e), latency_stats(service))
```

## Summary

- Rates divide by **`measured_s = duration_s - initial_stall_ms / 1000`**: the active phase minus the StallWindow,
  not `run_end_s`. With `initial_stall_ms = 0` this is `duration_s`.
- **The StallWindow is left out.** StallWindowRequests
  ([../client/design.md#stallwindow](../client/design.md#stallwindow)) count only in `counts.stall_window`; every
  other count, every rate and both latency statistics use the remaining records. Their rows stay in the Requests
  CSV, flagged by `in_stall_window`. A request gated during the window but sent after it is counted normally.
- **One record per logical request.** RequestCopies are not counted as requests: `sent` counts requests,
  `copies_sent` counts copies, and `copies_sent - sent` is the number of retries. A request's latency runs from
  its first send to the answer of its newest copy.
- `run_start_unix_ns` is passed through unchanged from the Run Controller; it is only reported.
- **Late answers.** With `slo_cancel = false` an `ok` can arrive after the SLO
  ([../client/design.md#draindeadline](../client/design.md#draindeadline)). It stays in `counts.ok` and in the latency
  statistics, is counted in `counts.ok_late`, and is left out of `rates.goodput`, which counts only answers within
  the SLO (the same test as Availability: `end_ns - send_ns <= slo_ns`). With `slo_cancel = true` the SLO timer
  cancels any request not answered by then, so `ok_late` is 0 apart from a response that wins the race with the
  timer by a few microseconds.

  Worked example, `slo_ns = 5_000_000_000`, `D_s = 10`:

  | outcome | `end_ns - send_ns` | `ok` | `ok_late` | In goodput |
  |---|---|---|---|---|
  | `ok` | 4 s | +1 | | yes |
  | `ok` | 5 s (exactly the SLO) | +1 | | yes: `<=` |
  | `ok` | 7 s | +1 | +1 | no |
  | `cancelled` | 80 s (DrainDeadline) | | | no |

  `counts.ok = 3`, `counts.ok_late = 1`, `rates.goodput = (3 - 1) / 10 = 0.2` requests/s.
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

## Design specific to Envoy

Implementation: `implementation/envoy`: vendored Envoy (`envoy/`) and Nighthawk (`nighthawk/`) with the testbed's
changes, recorded in [VENDORED.md](../../../implementation/envoy/VENDORED.md) ([AGENTS.md](../../../AGENTS.md#vendored-code)),
built in Docker with Bazel by `implementation/envoy/build.sh` (C++ tests: `bazel test` on the targets VENDORED.md
names); Python 3 wrappers `bin/ingress.py`, `bin/loadgen.py` and `wrappers/convert.py` (tests: `cd
implementation/envoy/wrappers && python3 -B -m unittest test_convert`). Only code written or changed for the testbed
carries `Design:` tags; unchanged upstream code does not.

### Envoy choices

- Collection: our per-request log (`request_log.cc`, new, enabled by `NH_REQUEST_LOG_PREFIX`) writes one CSV line per
  request from Nighthawk's StreamDecoder (response, reset or pool failure) with wall-clock times, the id from the
  request body and the ExecReport from the response's first 512 bytes; `convert.read_request_logs` reads every
  worker's file.
- RequestRecord: `scheduled_ns` is when the StreamDecoder was created (the request started), as the spec allows for
  a load generator that cannot see its schedule; `send_ns` is when the pool gave it a stream.
- Summary and Percentiles: `convert.loadgen_summary` and `convert.latency_stats`.

### Traceability

Generated from the `Design:` tags in the testbed's code under `implementation/envoy` by `implementation/spec_tools/tracegen.py` (see `AGENTS.md`). Each link points at the first code line after the tag.
Regenerate from the repository root with `python3 implementation/spec_tools/tracegen.py --lang Envoy --impl implementation/envoy --ext .cc --ext .h --comment // --ext .py:# --write`.

<!-- trace:begin -->
| Design subsection | Implementation (file:line) |
|---|---|
| [RequestRecord](#requestrecord) | [stream_decoder.cc:146](../../../implementation/envoy/nighthawk/source/client/stream_decoder.cc#L146), [stream_decoder.h:145](../../../implementation/envoy/nighthawk/source/client/stream_decoder.h#L145), [convert.py:369](../../../implementation/envoy/wrappers/convert.py#L369), [convert.py:396](../../../implementation/envoy/wrappers/convert.py#L396), [test_convert.py:246](../../../implementation/envoy/wrappers/test_convert.py#L246) |
| [Collection](#collection) | [request_log.cc:82](../../../implementation/envoy/nighthawk/source/client/request_log.cc#L82), [stream_decoder.cc:62](../../../implementation/envoy/nighthawk/source/client/stream_decoder.cc#L62), [stream_decoder.cc:99](../../../implementation/envoy/nighthawk/source/client/stream_decoder.cc#L99), [stream_decoder.cc:130](../../../implementation/envoy/nighthawk/source/client/stream_decoder.cc#L130), [convert.py:357](../../../implementation/envoy/wrappers/convert.py#L357) |
| [Summary](#summary) | [convert.py:417](../../../implementation/envoy/wrappers/convert.py#L417), [test_convert.py:246](../../../implementation/envoy/wrappers/test_convert.py#L246) |
| [Percentiles](#percentiles) | [convert.py:405](../../../implementation/envoy/wrappers/convert.py#L405), [test_convert.py:275](../../../implementation/envoy/wrappers/test_convert.py#L275) |
<!-- trace:end -->
