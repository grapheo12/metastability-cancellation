# Version — benchmark_runner/remote_setup

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-10-31T00:00:00Z | Initial spec: SourcePin, BuildManifest contract, machine setup |
| 0.2.0 | 2026-11-02T00:00:00Z | BuildManifest gains optional `thread_env` (language hook for a worker-thread count). MachineReady gains `thread_env` and `cpu_pinning` (`taskset` found on the machine) |
| 0.3.0 | 2026-11-02T00:00:00Z | BuildManifest binaries are `server`, `loadgen`, `ingress` (the canceller program is gone) |
