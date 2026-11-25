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

### Peak EWMA trace

With `--lb_policy ewma`, the ingress wrapper sets `PEAK_EWMA_TRACE_PREFIX` and Envoy's Peak EWMA (vendored change,
see `VENDORED.md`) logs every EWMA update and every pick. The wrapper merges them into
`ingress.peak_ewma_updates.csv` (`t_ns,server_id,rtt_ms,dt_ns,alpha,ewma_before_ms,ewma_after_ms`) and
`ingress.peak_ewma_picks.csv` (`t_ns,server_a,active_a,ewma_a_ms,cost_a,server_b,active_b,ewma_b_ms,cost_b,chosen`),
with `t_ns` on the `forwards.csv` time base. Plot them (needs plotly, as `bench.py plot` does):

```
python3 implementation/envoy/wrappers/plot_peak_ewma.py <results dir>   # writes plots/peak_ewma.html
```

## Not done yet

- Ingress: DeadlineFilter, LbRetries, Heartbeat Monitor, LoadCanceller (the wrapper rejects their flags).
- LoadGenerator: client retries (RequestCopies) and SLO cancellation.
- No `Design specific to C++` sections yet: the wrappers and Nighthawk changes are not traced to `abstraction/`.
