#!/usr/bin/env bash
# Build the load generator (nighthawk_client), the smoke-test upstream (nighthawk_test_server) and the ingress
# (envoy-static: Envoy plus the Peak EWMA contrib load balancer, @envoy//contrib/exe:envoy-static-peak-ewma) from the
# vendored sources, inside the build image Nighthawk's CI pins
# (nighthawk/.github/config.yml), with the same flags as nighthawk/ci/do_ci.sh's optimized build.
# Binaries land in build/ (git-ignored) for the image's architecture (linux/arm64 on an Apple-silicon Mac).
#
# usage: implementation/envoy/build.sh            # JOBS=<n> to cap Bazel's parallelism
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CFG="$HERE/nighthawk/.github/config.yml"
IMAGE="$(awk '/^[ ]*repo: /{print $2; exit}' "$CFG")@sha256:$(awk '/^[ ]*sha: /{print $2; exit}' "$CFG")"
CACHE_VOLUME="${CACHE_VOLUME:-impl-envoy-bazel-cache}"   # Bazel output root, kept between builds
JOBS="${JOBS:-auto}"
ENVOY_TARGET="@envoy//contrib/exe:envoy-static-peak-ewma"
TARGETS="//:nighthawk_client //:nighthawk_test_server $ENVOY_TARGET"
mkdir -p "$HERE/build"

# The image's entrypoint starts as root, sets its envoybuild user's uid to USER_UID and runs the command as that user
# (Bazel's hermetic Python refuses to run as root). The cache volume must belong to that uid.
docker run --rm --entrypoint chown -v "$CACHE_VOLUME:/build" "$IMAGE" "$(id -u):$(id -g)" /build
docker run --rm -e USER_UID="$(id -u)" \
  -v "$HERE:/src" -v "$CACHE_VOLUME:/build" -w /src/nighthawk "$IMAGE" bash -c "
    set -euo pipefail
    export PATH=/opt/llvm/bin:\$PATH CC=clang CXX=clang++ BAZEL_COMPILER=clang   # setup_clang_toolchain
    OPTS='-c opt --config=clang --define tcmalloc=gperftools --verbose_failures --jobs=$JOBS'
    bazel --output_user_root=/build/root build \$OPTS $TARGETS
    cp -f bazel-bin/nighthawk_client bazel-bin/nighthawk_test_server /src/build/
    cp -f \"\$(bazel --output_user_root=/build/root cquery \$OPTS --output=files $ENVOY_TARGET 2>/dev/null)\" /src/build/envoy-static
    chmod +x /src/build/*
    for b in nighthawk_client nighthawk_test_server envoy-static; do /src/build/\$b --version | head -2; done
  "
echo "built: $(ls "$HERE/build")"
