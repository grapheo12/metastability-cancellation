# impl/envoy: Envoy ingress and Nighthawk load generator

This branch replaces the Go Ingress with **Envoy** and the Go load generator with **Nighthawk**. Both are vendored
here as plain source trees (not submodules) so they can be modified; see [VENDORED.md](VENDORED.md) for the exact
upstream commits and the one local change (Nighthawk builds against `envoy/` instead of fetching Envoy).

| Path | What |
|---|---|
| `envoy/` | Envoy at `2f475e96` (1.41.0-dev), the commit the vendored Nighthawk pins |
| `nighthawk/` | Nighthawk `main` at `a968aa27` |
| `build.sh` | Builds `nighthawk_client`, `nighthawk_test_server` and `envoy-static` into `build/` |
| `smoke_test.sh`, `smoke/` | End-to-end check: `nighthawk_client` → `envoy-static` → `nighthawk_test_server` |

## Build

```
implementation/envoy/build.sh            # JOBS=<n> to cap Bazel's parallelism
```

It runs Nighthawk's pinned CI build image (`nighthawk/.github/config.yml`, `envoyproxy/envoy-build@sha256:…`,
multi-arch) with the flags of `nighthawk/ci/do_ci.sh`'s optimized build (`-c opt --config=clang
--define tcmalloc=gperftools`). The image's entrypoint runs the build as its `envoybuild` user with the caller's uid
(`USER_UID`). The Bazel output root lives in the Docker volume `impl-envoy-bazel-cache`, so rebuilds after source
changes are incremental. Needs Docker; about 30 GB of disk for the cache and plenty of RAM (16+ GB).

Reference build: metafail-exp-1 (x86_64, 32 vCPU, 61 GB, Ubuntu 26.04, Docker 29.1) took about 25 minutes from an
empty cache. The binaries (about 500 MB each, unstripped) also run directly on the host, outside the container.

## Smoke test

```
implementation/envoy/smoke_test.sh
```

In the build image: starts `nighthawk_test_server` on :10001 (200 with a 128-byte body), `envoy-static` on :10000
routing to it (round robin, admin on :9901), then `nighthawk_client --rps 100 --duration 10`. It passes when
Nighthawk reports at least 900 `benchmark.http_2xx`, no other status class or failure counter, and Envoy's
`cluster.servers.upstream_rq_2xx` is at least that count. First run: 1000 2xx at 100.00/s, Envoy upstream 2xx 1001
(one extra from the readiness probe).

## Experiments

`bin/ingress` and `bin/loadgen` are drop-in replacements for the Go Ingress and LoadGenerator: they take the Go
programs' flags, run Envoy and Nighthawk, and write the Go programs' output files (`wrappers/convert.py`), so
`implementation/scripts/bench.py run|plot` works unchanged with `benchmark_manifest.toml`. Configs are in
`experiments/`. The `/ping` server is the Go one (`../go`).

`--lb_policy` maps to Envoy's `round_robin`, `least_outstanding` (LEAST_REQUEST over all hosts), `power_of_two`
(LEAST_REQUEST, 2 choices) and `ewma` (the contrib Peak EWMA policy and its RTT filter, built into
`build/envoy-static` from `@envoy//contrib/exe:envoy-static-peak-ewma`).

`--lb_filter deadline_mean` with `--lb_policy ewma` selects `peak_ewma_deadline`, a policy added to the vendored
contrib (`envoy/contrib/peak_ewma/load_balancing_policies/source/deadline_lb.h`): the DeadlineFilter and
FilterWaitQueue of `abstraction/ingress/load_balancer/design.md` with Peak EWMA's RTT EWMA as the test.
- Every backend is evaluated: rejected if its `ProvisionalCurrentRTT > time left`. ProvisionalCurrentRTT treats the
  backend's pending requests as answered now: with nothing pending it is the EWMA RTT; otherwise it is
  `(1 − α) × EWMA RTT + α × L`, L the latency so far of its oldest pending request and α the EWMA's own time-based
  weight `1 − e^(−(now − last sample)/τ)`, so a stalled backend's value climbs toward its stall age while a healthy
  one stays at its EWMA. (Earlier: `EWMA RTT > time left`, and before that `EWMA RTT × (active + 1) <= time left`.)
  A backend with no RTT sample and nothing pending is kept. Pending requests are recorded when the balancer hands
  out a backend and removed when the request's stream ends. The lowest
  Peak EWMA cost (`EWMA RTT × (active + 1)`) among the kept ones is chosen (ties at random). The EWMA RTT already
  includes the time a request waits at the server, so the test does not multiply it by the active requests (the
  first version, up to commit `1495570d`, kept a backend if `EWMA RTT × (active + 1) <= time left`).
- The time left comes from the `x-request-deadline` header (Unix ns) that the ping RequestSource sends with each
  request, since Envoy chooses a host before the body arrives.
- When nothing is kept, or requests are already waiting, the request waits (Envoy's asynchronous host selection)
  in its worker's FIFO, re-checked every 10 ms; past its deadline it fails with a 503 (`no healthy upstream`).
- Differences from the Go `deadline_mean` (`(n + 1) × EwmaExecTime <= time left`): the test is the EWMA of the
  time to first response byte as Envoy measures it, alone, not multiplied by the outstanding requests (Envoy never
  parses the ExecReport); and there is one queue per Envoy worker, so FIFO order holds per worker, while `active`
  and the EWMA are shared by all workers.
- The ingress summary's `filter_*` counters come from the queue trace.
- LB retries (below) are picked like Go's LbRetry picks: at once, never behind the FIFO; a retry that fits nowhere
  is refused and re-checked every 10 ms while the earlier attempt keeps running, and is never dropped (that would
  fail the whole request). `lb_retry_refused` counts refused picks, one per 10 ms re-check, as Go counts one per
  tick.

`--lb_retry_policy timeout` with `--lb_max_retries N` (N > 0) maps to Envoy hedging: the route's `retry_policy`
has `per_try_timeout = lb_retry_timeout_ms`, `num_retries = N`, `retry_on: retriable-status-codes` with no codes
(errors, 503 and 400 are never retried, as in Go) and a back-off of at most 1 ms, and `hedge_policy` has
`hedge_on_per_try_timeout`, so the slow attempt is kept while the retry runs. Differences from Go's LbRetryLoop:
1. The first answer from any attempt is relayed (Go: the newest attempt's answer).
2. The other attempts are then reset: the server gets a cancel and Envoy's active count drops (Go: older attempts
   keep running, and counting at their server, until they finish).
3. The timeout counts from when the request is fully sent upstream, not from the pick, and fires exactly (Go:
   checked every 10 ms).
`forwards.csv` has one row per upstream attempt (from the router's upstream log, `attempt` numbered by start time
within a request); a hedging loser is `reset`. `lb_retries` in the summary counts attempts beyond each request's
first.

`--lb_retry_policy anxious` with `--lb_max_retries N` (N > 0), `--lb_retry_anxiety_ratio` (alpha),
`--lb_retry_late_factor` (beta) and `--ewma_beta` maps to the Go Ingress's AnxiousRetry
(`abstraction/ingress/retry_policy/design.md#anxious`), built from three vendored pieces:
- **`testbed_anxious` upstream filter** (`envoy/contrib/peak_ewma/anxious/`), in the cluster's upstream filter
  chain, one instance per upstream attempt: AttemptSent (TicketSeq, backend, the `x-request-deadline`) on request
  headers, AttemptFinished when the attempt ends, and the ExecReport's `exec_ns` of 202 / 503 answers for
  EwmaExecTime and EwmaAntiServiceTime (`ewma_beta`). It keeps the AnxiousTracker (EdfAhead, CurrentExpFinish,
  RetryExpFinish, the anxious set at most every 10 ms after a change) exactly as the spec's pseudocode; its tests
  are the spec's worked examples.
- **Router retry hook** (`envoy/source/common/router/testbed_retry_hook.h`): while the filter is configured, every
  upstream attempt ticks every 10 ms; on the newest attempt of an unanswered request the router asks the tracker
  (anxious and ExpectedLate: CurrentExpFinish > beta × time left) and, on yes, starts a hedge.
- **RetryElsewhere**: the route's `retry_host_predicate: previous_hosts`; Peak EWMA and `peak_ewma_deadline` leave
  out the hosts it excludes (unless all are).
Differences from Go: as for `timeout`, the first answer wins and the other attempts are reset (their tracker
entries end then); every backend is live (no Heartbeat Monitor); RetryRefused is not observed. The trace adds
`ingress.anxious.csv` (evaluations and retries decided) and `ingress.exec.csv` (EWMAs after each sample), and the
summary reports each backend's final `ewma_exec_ns` and `ewma_anti_exec_ns`.

### Peak EWMA trace

With `--lb_policy ewma`, the ingress wrapper sets `PEAK_EWMA_TRACE_PREFIX` and Envoy's Peak EWMA (vendored change,
see `VENDORED.md`) logs every EWMA update and every pick. The wrapper merges them into
`ingress.peak_ewma_updates.csv` (`t_ns,server_id,rtt_ms,dt_ns,alpha,ewma_before_ms,ewma_after_ms`) and
`ingress.peak_ewma_picks.csv` (`t_ns,server_a,active_a,ewma_a_ms,cost_a,server_b,active_b,ewma_b_ms,cost_b,chosen`),
with `t_ns` on the `forwards.csv` time base. `peak_ewma_deadline` writes `ingress.peak_ewma_evals.csv`
(`t_ns,worker,decision,kind,server_id,active,ewma_ms,cost,remaining_ns,eligible,chosen`, every backend at every
decision) and `ingress.peak_ewma_queue.csv` (`t_ns,worker,event,wait_ns,remaining_ns,server_id,refused`; `refused`: the refused
picks of an LB retry, on its `retry_sent` / `retry_gone` event) instead of picks. A refused retry's re-checks are
not traced one by one in the evals. Plot them (needs plotly, as `bench.py plot` does):

```
python3 implementation/envoy/wrappers/plot_peak_ewma.py <results dir>   # writes plots/peak_ewma.html
```

## Not done yet

- Ingress: DeadlineFilter strategies other than `deadline_mean` with `ewma`, Heartbeat Monitor, LoadCanceller
  (the wrapper rejects their flags).
- LoadGenerator: client retries (RequestCopies) and SLO cancellation.
- No `Design specific to C++` sections yet: the wrappers and Nighthawk changes are not traced to `abstraction/`.
