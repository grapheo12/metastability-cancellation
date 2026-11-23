# Frontend — API

## Types

### `ForwardTask`

The handling of one client `/ping` stream: its RequestId, client id, RequestDeadline, its ForwardAttempts (attempt 0
and any LbRetries, each with its ForwardTicket, InFlightEntry and cancel signal), and the client stream's reset flag.
See [design.md#forwardtask](design.md#forwardtask) and [design.md#forwardattempt](design.md#forwardattempt).

## Constants

| Name | Value | Meaning |
|---|---|---|
| `LB_RETRY_TICK_MS` | `10` | How often the LbRetryLoop asks the RetryPolicy (`Tick`) while the newest attempt runs, when LB retries are on ([design.md#lbretryloop](design.md#lbretryloop)) |

## Functions

### `serve(config: IngressConfig, listener) -> nothing`
Accepts client connections until shutdown and serves every stream on them. See
[design.md#client-connections](design.md#client-connections).

### `handle_ping(stream, ctx) -> Response | Aborted`
Runs one ForwardTask. `ctx` holds the In-Flight Map, the Load Balancer, the Upstream Pool, the RetryPolicy and the
start-once state. The answer is the newest attempt's ([design.md#lbretryloop](design.md#lbretryloop)).
`Aborted` means the client reset the stream and nothing is written. See [design.md#forwardtask](design.md#forwardtask).

## Behavior visible on the wire

| Client request | Ingress answer |
|---|---|
| `POST /ping`, body parses, `client_id < num_clients`, backend answers | the backend's status (`202`, `503` or `400`) and body, unchanged, with `content-type: application/json` when the body is non-empty |
| `POST /ping`, body parses, `client_id < num_clients`, dial or upstream stream fails | `502`, empty body |
| `POST /ping`, body does not parse, or `client_id >= num_clients` | `400`, empty body; no backend is contacted |
| other method on `/ping` | `405`, empty body |
| any other path (including `/cancel`) | `404`, empty body |
| client resets the stream while it is being forwarded | nothing written; every running attempt's upstream stream is reset |
| with LbRetries on, the RetryPolicy starts another attempt | the newest attempt's answer, as in the rows above; older attempts' answers are never written |

## Uses

- [../../protocol/api.md](../../protocol/api.md): `parse_body`, `parse_deadline`, `parse_exec_report`, `parse_worker_count`, `build_cancel_body`, `split_request_id`, status constants
- [../inflight_map/api.md](../inflight_map/api.md): `publish`, `clear`
- [../load_balancer/api.md](../load_balancer/api.md): `pick`, `finish`, `ForwardTicket`, `ForwardOutcome`
- [../retry_policy/api.md](../retry_policy/api.md): `decide`, `RetryState`, `RetryEvent`
- [../upstream_pool/api.md](../upstream_pool/api.md): `get`, `discard`
- [../load_canceller/api.md](../load_canceller/api.md): `canceller_start`
