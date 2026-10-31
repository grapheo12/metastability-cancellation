# Experiment Executor — API

## Constants

| Name | Value | Meaning |
|---|---|---|
| `READY_LINE` | `listening on` | Substring of the server's startup line ([../../server/api.md#lifecycle](../../server/api.md#lifecycle)) |
| `COUNTERS_PREFIX` | `{"cancelled_before_processing":` | Start of the server's CancelCounters line ([../../server/api.md#cancelcounters-output](../../server/api.md#cancelcounters-output)) |
| `POLL_INTERVAL_S` | `0.2` | Polling period for readiness and exit checks |
| `CANCELLER_DRAIN_S` | `1.0` | Extra time allowed for the canceller after its `duration_s` (its drain timeout) |

They are used in [design.md#serverlifecycle](design.md#serverlifecycle) and [design.md#clientrun](design.md#clientrun).

## Types

### `PointResult`

`DONE`, `SKIPPED` (already done), or `FAILED(reason: string)`. See [design.md#run_point](design.md#run_point).

## Functions

### `run_point(point: SweepPoint, config: BenchConfig, server: MachineReady, client: MachineReady, run_id: string) -> PointResult`
Runs one SweepPoint and writes `<output_dir>/points/point_<id>/`
([../api.md#output-directory](../api.md#output-directory)). See [design.md#run_point](design.md#run_point).

### `stop_server(server: MachineReady, stop_timeout_s) -> ok | FAILED`
Stops the server recorded in the server machine's pid file, if one is running. It is also used on operator
interrupt. See [design.md#serverlifecycle](design.md#serverlifecycle).

## Uses

- [../config/api.md](../config/api.md): `SweepPoint`, `format_arg`
- [../remote_transport/api.md](../remote_transport/api.md): `run`, `start_background`, `signal`, `alive`, `fetch`, `quote`
- [../remote_setup/api.md](../remote_setup/api.md): `MachineReady`
