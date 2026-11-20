# Heartbeat Monitor — API

## Parameters

| Parameter | Type / unit / constraint | Default | Meaning |
|---|---|---|---|
| `heartbeat_timeout_ms` | integer, ms, `>= 0` | `0` | A backend with no HeartbeatLine for this long is not live. `0`: no monitor, every backend live. Same as [../../api.md#ingress-parameters](../../api.md#ingress-parameters) |

## Constants

| Name | Value | Meaning |
|---|---|---|
| `HEARTBEAT_CHECK_MS` | `50` | Tick of the BackendLiveness checker ([design.md#backendliveness](design.md#backendliveness)) |
| `HEARTBEAT_REDIAL_MS` | `100` | Pause before a HeartbeatReader dials again after its stream ended or a dial failed ([design.md#heartbeatreader](design.md#heartbeatreader)) |

## Types

### `HeartbeatMonitor`

| Field | Type | Meaning |
|---|---|---|
| `last_hb_ns` | array[B] of atomic u64, ns, monotonic | When backend `s`'s last HeartbeatLine arrived; the monitor's start time until the first one |
| `live` | array[B] of atomic boolean | BackendLiveness, as last set by the checker; all `true` at start |
| `log` | list of LivenessRecord | The LivenessLog, appended by the checker only |
| `stop` | atomic boolean | Set by `monitor_stop` |

### `LivenessRecord`

| Field | Type | Meaning |
|---|---|---|
| `at_ns` | u64, ns, monotonic | When the checker saw the change (or the start) |
| `server_id` | u32 | The backend |
| `live` | boolean | The new state |

## Functions

### `monitor_start(config: IngressConfig, start_ns: u64) -> HeartbeatMonitor | none`
Returns `none` if `heartbeat_timeout_ms = 0`. Otherwise records one LivenessRecord per backend (`live = true`,
`at_ns = start_ns`), starts one HeartbeatReader per backend and the checker, and returns at once. Called by the
Ingress at startup, before `listening on`. See [design.md#heartbeatreader](design.md#heartbeatreader).

### `is_live(monitor: HeartbeatMonitor | none, s: u32) -> boolean`
`true` if `monitor` is `none`, else `live[s]`. One atomic load; never waits. Called by the Load Balancer on every
`pick`. See [design.md#backendliveness](design.md#backendliveness).

### `monitor_stop(monitor) -> list of LivenessRecord`
Stops the readers and the checker, closes their connections, waits for them, and returns the LivenessLog in append
order. Called at shutdown. See [design.md#backendliveness](design.md#backendliveness).

## Outputs

### `<output_path>.liveness.csv`

The LivenessLog. UTF-8, comma-separated, `\n` line endings, one header line:

```
t_ns,server_id,live
```

| Column | Type / unit | Meaning |
|---|---|---|
| `t_ns` | signed 64-bit integer, ns | `at_ns - run_start` on the Ingress's time base ([../design.md#time-base](../design.md#time-base)); negative for records before the first `/ping`. With no `/ping` received, `at_ns - start_ns` |
| `server_id` | u32 | The backend |
| `live` | `1` \| `0` | The new state |

Rows are in LivenessLog order, which is increasing `at_ns`. Header only when the monitor is off.

## Uses

- [../../protocol/api.md](../../protocol/api.md): `parse_heartbeat_line`, `HEARTBEAT_PATH`, `HEARTBEAT_METHOD`, `STATUS_STREAM_OK`
- [../upstream_pool/api.md](../upstream_pool/api.md): `UPSTREAM_DIAL_TIMEOUT_MS`, and the same `open_h2c_connection`
- [../../server/heartbeat/api.md](../../server/heartbeat/api.md): the HeartbeatStream each backend sends
