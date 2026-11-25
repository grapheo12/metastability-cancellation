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

## Not done yet

- Nothing here realizes a spec component yet: Envoy and Nighthawk still have to be configured as the Ingress and the
  LoadGenerator of `abstraction/` (DeadlineFilter, LbRetries, Heartbeat Monitor, the Requests CSV, …), with
  `Design specific to C++` sections.
- Our servers expect each `/ping` body to be a PaddedJsonBody with a unique RequestId; Nighthawk sends fixed bodies.
  Planned: a Nighthawk RequestSource plugin that generates the body.
