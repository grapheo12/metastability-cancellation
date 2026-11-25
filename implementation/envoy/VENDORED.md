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

Upstream stores 72 files under `envoy/test/common/http/http1/http1_connection_corpus/` with CRLF line endings although
Envoy's `.gitattributes` says `* text=auto eol=lf`; they were added with `git hash-object --no-filters` so the stored
blobs match upstream byte for byte. Git may report them as modified after a fresh checkout, exactly as it does in an
upstream Envoy clone.
