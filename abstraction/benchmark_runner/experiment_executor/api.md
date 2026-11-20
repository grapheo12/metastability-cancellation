# Experiment Executor — API

## Constants

| Name | Value | Meaning |
|---|---|---|
| `READY_LINE` | `listening on` | Substring of the server's and the Ingress's startup line ([../../server/api.md#lifecycle](../../server/api.md#lifecycle), [../../ingress/api.md#lifecycle](../../ingress/api.md#lifecycle)) |
| `COUNTERS_PREFIX` | `{"cancelled_before_processing":` | Start of the server's CancelCounters line ([../../server/api.md#cancelcounters-output](../../server/api.md#cancelcounters-output)) |
| `POLL_INTERVAL_S` | `0.2` | Polling period for readiness and exit checks |

They are used in [design.md#backgroundprogram](design.md#backgroundprogram) and [design.md#run_point](design.md#run_point).

## Types

### `PointResult`

`DONE`, `SKIPPED` (already done), or `FAILED(reason: string)`. See [design.md#run_point](design.md#run_point).

## Functions

### `run_point(point: SweepPoint, config: BenchConfig, servers: list of MachineReady, client: MachineReady, run_id: string) -> PointResult`
Runs one SweepPoint, with `servers` the ServerMachines in config order, and writes `<output_dir>/points/point_<id>/`
([../api.md#output-directory](../api.md#output-directory)). See [design.md#run_point](design.md#run_point).

### `placement_prefix(program: "server" | "loadgen" | "ingress", point: SweepPoint, ready: MachineReady) -> list of string`
The words that go before the program's executable on its command line: thread count and CPU pinning. Used by
`start_servers`, `start_ingress`, the client run and `placement.json`. See [design.md#processplacement](design.md#processplacement).

### `stop_servers(servers: list of MachineReady, server_hosts: list of integer, stop_timeout_s) -> ok | FAILED`
Stops server instances `0 ..= len(server_hosts)-1`, each recorded in the pid file on its machine
`servers[server_hosts[k]]`, if running. Also used on operator interrupt. See
[design.md#serverlifecycle](design.md#serverlifecycle).

### `stop_leftover_servers(m: MachineReady, stop_timeout_s)`
Stops every server instance with a pid file on machine `m`, whatever point started it. Run on each server machine
before a point starts its servers. See [design.md#serverlifecycle](design.md#serverlifecycle).

### `stop_ingress(m: MachineReady, stop_timeout_s) -> ok | FAILED`
Stops the Ingress recorded in machine `m`'s pid file, if running; it writes its outputs as it exits. Also used on
operator interrupt, on the Ingress's machine. See [design.md#ingresslifecycle](design.md#ingresslifecycle).

## Uses

- [../config/api.md](../config/api.md): `SweepPoint` (including `ingress_args`, `server_instances`, `server_hosts`, `ingress_machine`, `loadgen_target`, `placement`), `format_arg`
- [../remote_transport/api.md](../remote_transport/api.md): `run`, `start_background`, `signal`, `alive`, `fetch`, `quote`
- [../remote_setup/api.md](../remote_setup/api.md): `MachineReady` (including `thread_env`, `cpu_pinning`)
