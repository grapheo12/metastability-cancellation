# Benchmark Runner — API

## Commands

The program is `bench`, with one command per invocation ([../api.md#benchmark-runner](../api.md#benchmark-runner)):

| Command | Steps run ([design.md#run-flow](design.md#run-flow)) |
|---|---|
| `bench run <config.toml>` | load config → SourcePin → setup every machine → every SweepPoint → plot |
| `bench setup <config.toml>` | load config → SourcePin → setup every machine |
| `bench plot <output_dir>` | PointTable and SweepPlots from the points already in `<output_dir>` |
| `bench retry-timeout --workers K --mean_service_ms S [--eta E] RATE [RATE ...]` | RetryTimeoutCalculator only: RetryTimeout and RetryFeedback per rate; no config, no machines ([retry_timeout/api.md](retry_timeout/api.md#outputs)) |
| `bench stable-eta --workers K --mean_service_ms S RATE [RATE ...]` | RetryTimeoutCalculator only: MaxStableEta per rate; no config, no machines ([retry_timeout/api.md](retry_timeout/api.md#outputs)) |

`run` resumes: SweepPoints that already have a `done` marker in the output directory are skipped. To resume a run,
set `[run] output_dir` to that run's directory.

## Exit status

| Status | When |
|---|---|
| `0` | Every SweepPoint is `done` (for `run`), or setup succeeded (for `setup`), or plots were written (for `plot`), or every rate was solved (for `retry-timeout` and `stable-eta`) |
| `1` | At least one SweepPoint `failed` (outputs and plots of the other points are still written) |
| `2` | Config error, SourcePin error, setup failure, usage error, or (for `retry-timeout` and `stable-eta`) a validation error. Nothing was run |

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
      ingress.cancels.csv     # Ingress outputs (../ingress/api.md#outputs)
      ingress.forwards.csv    # the Ingress's ForwardLog
      ingress.liveness.csv    # the Ingress's LivenessLog (../ingress/heartbeat_monitor/api.md#outputs)
      ingress.picks.csv       # the Ingress's PickLog (../ingress/api.md#output_pathpickscsv)
      ingress.summary.json
      ingress.log
      server_<k>.out          # stdout + stderr of server instance k
      server_<k>.queue.csv    # QueueLengthLog of instance k (../server/api.md#queuelengthlog-output)
      server_<k>.gc.csv       # GcEvents of instance k, with [server] gc_trace (experiment_executor/design.md#gcevents)
      server_counters_<k>.json  # parsed CancelCounters line of instance k
      server_counters.json    # the instances' CancelCounters, summed
      done | failed           # exactly one marker; "failed" holds the reason
  all_points.csv              # the PointTable
  plots/
    latency_e2e.html  goodput.html  outcomes.html  server_counters.html  throughput_latency.html
    server_counters_normalized.html  throughput_latency_normalized.html  queue_length.html
    forward_latency.html  ewma_latency.html   # or forward_latency_server_<k>.html ... with more than one backend
    availability.html  start_slack.html  expected_wait.html
    <shared script file>                      # used by every page, so they open offline
```

File meanings are defined in [experiment_executor/design.md](experiment_executor/design.md#point-outputs) and
[result_plotter/design.md](result_plotter/design.md#pointtable).

## Uses

- [../api.md](../api.md): the program parameters of `server`, `loadgen` and `ingress`, the `listening on` line
  of the server and the Ingress, and the server's CancelCounters output line.

## Subcomponents

| Subcomponent | Spec |
|---|---|
| Config | [config/api.md](config/api.md) |
| Remote Transport | [remote_transport/api.md](remote_transport/api.md) |
| Remote Setup | [remote_setup/api.md](remote_setup/api.md) |
| Experiment Executor | [experiment_executor/api.md](experiment_executor/api.md) |
| Result Plotter | [result_plotter/api.md](result_plotter/api.md) |
| Retry Timeout | [retry_timeout/api.md](retry_timeout/api.md) |
