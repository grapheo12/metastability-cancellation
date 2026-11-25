#!/usr/bin/env bash
# Smoke test: nighthawk_client -> envoy-static (ingress) -> nighthawk_test_server, all built by build.sh, run inside
# the same build image. 100 req/s for 10 s; passes if every request got a 2xx and Envoy's upstream 2xx count
# matches. Exit status 0 on pass, 1 on failure.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CFG="$HERE/nighthawk/.github/config.yml"
IMAGE="$(awk '/^[ ]*repo: /{print $2; exit}' "$CFG")@sha256:$(awk '/^[ ]*sha: /{print $2; exit}' "$CFG")"
for b in nighthawk_client nighthawk_test_server envoy-static; do
  [[ -x "$HERE/build/$b" ]] || { echo "missing build/$b: run build.sh first"; exit 1; }
done
docker run --rm -e USER_UID="$(id -u)" -v "$HERE:/src:ro" "$IMAGE" bash -c '
  set -euo pipefail
  cd /tmp
  /src/build/nighthawk_test_server --config-path /src/smoke/test_server.yaml --base-id 1 -l warning > test_server.log 2>&1 &
  /src/build/envoy-static --config-path /src/smoke/envoy.yaml --base-id 2 -l warning > envoy.log 2>&1 &
  for i in $(seq 1 60); do
    curl -sf -o /dev/null http://127.0.0.1:10000/ping && break
    sleep 1
  done
  /src/build/nighthawk_client --rps 100 --duration 10 --concurrency 1 \
      http://127.0.0.1:10000/ping > result.txt 2> client.log || { tail -20 client.log; exit 1; }
  sed -n "/^Counter/,/^\$/p" result.txt
  # Counter table rows: name, value, per second. Pass: >= 900 2xx, no other status class, no failure/timeout
  # counters, and Envoy saw at least as many upstream 2xx.
  xx2=$(awk "\$1 == \"benchmark.http_2xx\" {print \$2}" result.txt)
  bad=$(awk "\$1 ~ /^benchmark\\.http_/ && \$1 != \"benchmark.http_2xx\" && \$2 > 0 || \$1 ~ /(timeout|failure|error)/ && \$2 > 0 {print \$1\"=\"\$2}" result.txt | tr "\n" " ")
  up=$(curl -s http://127.0.0.1:9901/stats | awk -F": " "\$1 == \"cluster.servers.upstream_rq_2xx\" {print \$2}")
  echo "nighthawk 2xx=${xx2:-0} other=[${bad}] envoy upstream_rq_2xx=${up:-0}"
  if [[ "${xx2:-0}" -ge 900 && -z "$bad" && "${up:-0}" -ge "${xx2:-0}" ]]; then
    echo "SMOKE TEST PASSED"
  else
    echo "SMOKE TEST FAILED"; tail -5 envoy.log test_server.log; exit 1
  fi
'
