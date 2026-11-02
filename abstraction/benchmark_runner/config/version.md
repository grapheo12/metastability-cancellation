# Version — benchmark_runner/config

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-10-31T00:00:00Z | Initial spec: config file schema, BenchConfig validation, SweepExpansion, argument formatting |
| 0.2.0 | 2026-11-02T00:00:00Z | ProcessPlacement runner keys (`threads`, `cpu_set`) and SweepPoint `placement`. CancelRateFraction (`canceller.cancel_rate_fraction`, sweepable, derives `cancel_rate = global_rate × fraction`) |
| 0.2.1 | 2026-11-02T00:00:00Z | `incr_rand_max` added to the canceller program parameters (settable and sweepable) |
| 0.2.2 | 2026-11-02T00:00:00Z | Machine tables: `user`, `ssh_key`, `ssh_port` optional (from `~/.ssh/config`); `data_addr` default via `ssh_hostname`. `validate_machine` pseudocode and table |
