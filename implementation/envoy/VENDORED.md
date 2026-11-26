# Vendored code

Both trees are checked in directly (not submodules) so they can be modified here. Each was copied without its
`.git` directory and verified entry by entry (mode and blob id) against upstream.

| Directory | Upstream | Commit | Date | Notes |
|---|---|---|---|---|
| `nighthawk/` | https://github.com/envoyproxy/nighthawk | `a968aa27c5919a1701318b96676fd7477a4d6607` (`main`) | 2026-11-24 | Nighthawk has not tagged a release since `0.5.0` (2022); `main` is what its CI builds |
| `envoy/` | https://github.com/envoyproxy/envoy | `2f475e9681d2d0e236e0ffe3ffd232872d2e414a` (1.41.0-dev) | 2026-11-13 | The `ENVOY_COMMIT` that the vendored Nighthawk's `MODULE.bazel` pins |

## Local changes to the vendored trees

| File | Change | Why |
|---|---|---|
| `nighthawk/MODULE.bazel` | `git_override` of `envoy` and `envoy_api` replaced by `local_path_override` to `../envoy` and `../envoy/api` | Nighthawk builds against this repository's Envoy copy, so edits to it take effect |
| `nighthawk/source/request_source/ping_request_source_plugin*` (new), `BUILD`, `source/client/BUILD` | `nighthawk.request_source.ping` RequestSource: `POST /ping` with the testbed's PingRequestBody (`{"id","deadline","payload"}`, exactly `request_bytes`); one instance per worker, client id = `client_id_base` + instance, `seq` 0, 1, … | Drive the Go `/ping` server, which needs a unique RequestId and a RequestDeadline per request. Each request also carries header `x-request-deadline` (the same Unix ns as the body's `deadline`) for Envoy's `peak_ewma_deadline` load balancer, which sees only headers |
| `nighthawk/source/client/request_log.{h,cc}` (new), `stream_decoder.{h,cc}`, `source/client/BUILD` | With `NH_REQUEST_LOG_PREFIX` set, each worker thread writes `<prefix>.<n>.csv`: request id, result (response, reset, pool failure), status, scheduled/send/end wall times, and the response's ExecReport | The testbed's per-request Requests CSV; the loadgen wrapper converts it |
| `nighthawk/source/common/rate_limiter_impl.{h,cc}`, `source/client/factories_impl.cc` | `PoissonRateLimiter`; `NH_POISSON_RATE` (per-worker rate) selects it, `NH_POISSON_SEED` seeds worker k with seed + k | Open-loop Poisson arrivals like the Go load generator (Nighthawk only paces linearly) |
| `nighthawk/test/request_source/ping_request_source_plugin_test.cc` (new), `test/request_source/BUILD`, `test/rate_limiter_test.cc` | Tests for the body format, the log's field parsing and the Poisson limiter | |
| `envoy/contrib/exe/BUILD` | New target `envoy-static-peak-ewma`: `envoy-static` plus only the Peak EWMA contrib load balancing policy and its RTT HTTP filter | `--lb_policy ewma` maps to Peak EWMA; the full contrib binary would also pull every other contrib extension's dependencies (hyperscan, Kafka, Go, …) |
| `envoy/contrib/peak_ewma/load_balancing_policies/source/peak_ewma_lb.{h,cc}` | `PeakEwmaTrace`: with `PEAK_EWMA_TRACE_PREFIX` set, each balancer instance writes `<prefix>.ewma.<n>.csv` (every EWMA update: sample, Δt, alpha, EWMA before and after) and `<prefix>.picks.<n>.csv` (every Power of Two Choices decision: both candidates' active requests, EWMA and cost, and the choice) | Plot the balancer's internals over time (`wrappers/plot_peak_ewma.py`); off without the variable |
| `envoy/contrib/peak_ewma/load_balancing_policies/source/peak_ewma_lb.cc` (`calculateTimeBasedAlpha`), `test/peak_ewma_lb_host_lifecycle_test.cc` | `alpha = 1 - e^(-max(dt, 0)/tau)`: a concurrent or out-of-order RTT sample (dt <= 0) gets alpha = 0 instead of upstream's alpha = 1; test `OutOfOrderSampleHasNoWeight` | Samples from two workers can reach the shared ring buffer a few ns out of order; upstream then replaced the whole EWMA by that one sample (a stall run: dt = -9 ns moved the healthy server's EWMA from 400 to 1151 ms, and under `peak_ewma_deadline` blocked it for about 20 s). Applies to both Peak EWMA policies |
| `envoy/contrib/peak_ewma/load_balancing_policies/source/deadline_lb.{h,cc}`, `deadline_config.{h,cc}`, `BUILD` (new); `api/contrib/envoy/extensions/load_balancing_policies/peak_ewma_deadline/v3alpha/` (new proto); `peak_ewma_lb.{h,cc}` (members `protected` instead of `private`; trace files `evals` and `queue`); `contrib/exe/BUILD`; `api/versioning/BUILD`; `contrib/peak_ewma/load_balancing_policies/test/deadline_lb_test.cc`, `test/BUILD` | `envoy.load_balancing_policies.peak_ewma_deadline`: Peak EWMA's cost as a deadline filter over every host, lowest cost among the kept ones, and a per-worker FIFO wait queue (asynchronous host selection, re-checked every 10 ms, 503 past the deadline); deadline from the `x-request-deadline` header | The testbed's DeadlineFilter and FilterWaitQueue (`--lb_policy ewma --lb_filter deadline_mean`) in Envoy |
| `envoy/contrib/peak_ewma/load_balancing_policies/source/deadline_lb.{h,cc}`, `BUILD`, `test/deadline_lb_test.cc`, `test/BUILD` | LB retries in `peak_ewma_deadline` (stream-info attempt count > 1): picked at once, never behind the FIFO; with no kept host they wait in a separate list re-checked every scan and are never dropped; trace kinds `retry`, `retry_rescan` and queue events `retry_waiting`, `retry_sent`, `retry_gone`; tests | Go's LbRetry picks: a refused retry is re-tried every 10 ms while the earlier attempt keeps running; a `nullptr` host for a retry would make Envoy fail the whole request |

Upstream stores 72 files under `envoy/test/common/http/http1/http1_connection_corpus/` with CRLF line endings although
Envoy's `.gitattributes` says `* text=auto eol=lf`; they were added with `git hash-object --no-filters` so the stored
blobs match upstream byte for byte. Git may report them as modified after a fresh checkout, exactly as it does in an
upstream Envoy clone.
