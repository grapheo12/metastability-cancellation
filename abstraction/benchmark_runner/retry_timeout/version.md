# Version — benchmark_runner/retry_timeout

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-11-18T00:00:00Z | Initial spec: RetryTimeoutCalculator for a pooled M/M/k model: Erlang C, sojourn-time tail, bisection for `Pr(sojourn > T) = 1 − ρ`, validation, output format, worked examples |
| 0.2.0 | 2026-11-18T00:00:00Z | EtaSlack target `max(0, min(1, η − ρ))` (`--eta`, default 1 = the old rule; infinite RetryTimeout at target 0, written as `NO_RETRY_TIMEOUT_MS` in the TOML line); RetryFeedback StableLoad and TippingLoad; MaxStableEta search and `bench stable-eta`; output columns `target`, `stable_rho`, `tipping_rho`; the model may be one server instance |
| 0.3.0 | 2026-11-18T00:00:00Z | Convergence section: assumptions H1–H6 with the prose M/M/k Lemma 1, and Theorems 1–3 machine-checked in Lean in `proofs/` (iteration to the StableLoad or collapse, sound grid scan and bisection, stable `η` form a down-set and the `η` bisection brackets the MaxStableEta); `above(x[0])` note corrected (rounding of `1 + target`, not underflow) |
