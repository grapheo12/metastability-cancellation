#!/usr/bin/env bash
# BuildManifest build command for impl/envoy (implementation/envoy/benchmark_manifest.toml), run by the
# BenchmarkRunner in implementation/envoy on every machine:
#   - always: the Go /ping server (implementation/go, copied from impl/go), into bin/server (git-ignored);
#   - only where Docker is available (the client machine): Envoy and Nighthawk via build.sh, whose Bazel cache
#     makes repeat builds incremental. Server machines (no Docker) skip it: they only run the server; the
#     ingress and loadgen wrappers exit with a clear message if their binaries are missing.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
(cd "$HERE/../go" && CGO_ENABLED=0 go build -o "$HERE/bin/server" ./cmd/server)
if command -v docker > /dev/null && { docker info > /dev/null 2>&1 || sg docker -c "docker info" > /dev/null 2>&1; }; then
  if docker info > /dev/null 2>&1; then "$HERE/build.sh"; else sg docker -c "$HERE/build.sh"; fi
else
  echo "bench_build: no usable Docker here: skipping Envoy/Nighthawk (server machine)"
fi
