# Benchmark Runner — API

## Commands

The program is `bench`, with one command per invocation ([../api.md#benchmark-runner](../api.md#benchmark-runner)):

| Command | Steps run ([design.md#run-flow](design.md#run-flow)) |
|---|---|
| `bench run <config.toml>` | load config → SourcePin → setup both machines → every SweepPoint → plot |
| `bench setup <config.toml>` | load config → SourcePin → setup both machines |
| `bench plot <output_dir>` | PointTable and SweepPlots from the points already in `<output_dir>` |

`run` resumes: SweepPoints that already have a `done` marker in the output directory are skipped. To resume a run,
set `[run] output_dir` to that run's directory.

## Exit status

| Status | When |
|---|---|
| `0` | Every SweepPoint is `done` (for `run`), or setup succeeded (for `setup`), or plots were written (for `plot`) |
| `1` | At least one SweepPoint `failed` (outputs and plots of the other points are still written) |
| `2` | Config error, SourcePin error, setup failure, or usage error. Nothing was run |

## Output directory

```
<output_dir>/
  config.toml                 # copy of the config as given
  run.json                    # {"branch", "commit", "repo_url", "lang", "started_utc", "points"}
  points/
    point_<id, 4 digits>/
      params.json             # the SweepPoint (see config/api.md#sweeppoint)
      placement.json          # prefix words used per program (experiment_executor/design.md#processplacement)
      loadgen.requests.csv    # loadgen outputs (../load_generator/metrics/api.md#outputs)
      loadgen.summary.json
      loadgen.log
      canceller.cancels.csv   # canceller outputs, if the canceller is enabled
      canceller.summary.json
      canceller.log
      server.out              # server stdout + stderr
      server_counters.json    # parsed CancelCounters line
      done | failed           # exactly one marker; "failed" holds the reason
  all_points.csv              # the PointTable
  plots/
    latency_e2e.png  goodput.png  outcomes.png  server_counters.png  throughput_latency.png
    server_counters_normalized.png
```

File meanings are defined in [experiment_executor/design.md](experiment_executor/design.md#point-outputs) and
[result_plotter/design.md](result_plotter/design.md#pointtable).

## Uses

- [../api.md](../api.md): the program parameters of `server`, `loadgen` and `canceller`, the server's
  `listening on` line and its CancelCounters output line.

## Subcomponents

| Subcomponent | Spec |
|---|---|
| Config | [config/api.md](config/api.md) |
| Remote Transport | [remote_transport/api.md](remote_transport/api.md) |
| Remote Setup | [remote_setup/api.md](remote_setup/api.md) |
| Experiment Executor | [experiment_executor/api.md](experiment_executor/api.md) |
| Result Plotter | [result_plotter/api.md](result_plotter/api.md) |
