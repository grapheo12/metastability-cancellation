# Experiment Executor — API

## Constants

| Name | Value | Meaning |
|---|---|---|
| `READY_LINE` | `listening on` | Substring of the server's and the Ingress's startup line ([../../server/api.md#lifecycle](../../server/api.md#lifecycle), [../../ingress/api.md#lifecycle](../../ingress/api.md#lifecycle)) |
| `COUNTERS_PREFIX` | `{"cancelled_before_processing":` | Start of the server's CancelCounters line ([../../server/api.md#cancelcounters-output](../../server/api.md#cancelcounters-output)) |
| `POLL_INTERVAL_S` | `0.2` | Polling period for readiness and exit checks |
| `MAX_INSTANCES` | `64` | Largest `[server] instances`; leftover cleanup checks this many pid files |

They are used in [design.md#backgroundprogram](design.md#backgroundprogram) and [design.md#run_point](design.md#run_point).

## Types

### `PointResult`

`DONE`, `SKIPPED` (already done), or `FAILED(reason: string)`. See [design.md#run_point](design.md#run_point).

## Functions

### `run_point(point: SweepPoint, config: BenchConfig, server: MachineReady, client: MachineReady, run_id: string) -> PointResult`
Runs one SweepPoint and writes `<output_dir>/points/point_<id>/`
([../api.md#output-directory](../api.md#output-directory)). See [design.md#run_point](design.md#run_point).

### `placement_prefix(program: "server" | "loadgen" | "ingress", point: SweepPoint, ready: MachineReady) -> list of string`
The words that go before the program's executable on its command line: thread count and CPU pinning. Used by
`start_servers`, `start_ingress`, the client run and `placement.json`. See [design.md#processplacement](design.md#processplacement).

### `stop_servers(server: MachineReady, count: integer, stop_timeout_s) -> ok | FAILED`
Stops server instances `0 ..= count-1` recorded in the server machine's pid files, if running. Also used on operator
interrupt. See [design.md#serverlifecycle](design.md#serverlifecycle).

### `stop_ingress(client: MachineReady, stop_timeout_s) -> ok | FAILED`
Stops the Ingress recorded in the client machine's pid file, if running; it writes its outputs as it exits. Also
used on operator interrupt. See [design.md#ingresslifecycle](design.md#ingresslifecycle).

## Uses

- [../config/api.md](../config/api.md): `SweepPoint` (including `ingress_args`, `server_instances`, `placement`), `format_arg`
- [../remote_transport/api.md](../remote_transport/api.md): `run`, `start_background`, `signal`, `alive`, `fetch`, `quote`
- [../remote_setup/api.md](../remote_setup/api.md): `MachineReady` (including `thread_env`, `cpu_pinning`)
