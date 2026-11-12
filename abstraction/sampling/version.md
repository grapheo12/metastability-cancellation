# Version — sampling

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-10-30T00:00:00Z | Initial spec: SplitMix64, xoshiro256**, seed derivation, exponential sampler |
| 0.2.0 | 2026-10-30T00:00:00Z | Added UniformIntSampler (`uniform_int`) and the `STREAM_CANCELLER` seed kind. `mix64` is now public |
| 0.2.1 | 2026-10-30T00:00:00Z | API style fixes: constants, `Rng` and every function link their design anchor. No behavior change |
| 0.2.2 | 2026-11-02T00:00:00Z | New stream kind `STREAM_INGRESS_LB = 4` for an Ingress LoadBalancer |
| 0.3.0 | 2026-11-12T00:00:00Z | Stream kinds `STREAM_SERVER_MESSAGES = 5` (per request, derived by client id then seq) and `STREAM_SERVER_PREFILL = 6` (MessageBuffer fill) |
