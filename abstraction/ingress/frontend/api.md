# Frontend — API

## Types

### `ForwardTask`

The handling of one client `/ping` stream: its RequestId, client id, ForwardTicket (chosen `server_id`), published InFlightEntry, and
the client stream's reset flag. See [design.md#forwardtask](design.md#forwardtask).

## Functions

### `serve(config: IngressConfig, listener) -> nothing`
Accepts client connections until shutdown and serves every stream on them. See
[design.md#client-connections](design.md#client-connections).

### `handle_ping(stream, ctx) -> Response | Aborted`
Runs one ForwardTask. `ctx` holds the In-Flight Map, the Load Balancer, the Upstream Pool and the start-once state.
`Aborted` means the client reset the stream and nothing is written. See [design.md#forwardtask](design.md#forwardtask).

## Behavior visible on the wire

| Client request | Ingress answer |
|---|---|
| `POST /ping`, body parses, `client_id < num_clients`, backend answers | the backend's status (`202`, `503` or `400`) and body, unchanged, with `content-type: application/json` when the body is non-empty |
| `POST /ping`, body parses, `client_id < num_clients`, dial or upstream stream fails | `502`, empty body |
| `POST /ping`, body does not parse, or `client_id >= num_clients` | `400`, empty body; no backend is contacted |
| other method on `/ping` | `405`, empty body |
| any other path (including `/cancel`) | `404`, empty body |
| client resets the stream while it is being forwarded | nothing written; the upstream stream is reset |

## Uses

- [../../protocol/api.md](../../protocol/api.md): `parse_body`, `split_request_id`, status constants
- [../inflight_map/api.md](../inflight_map/api.md): `publish`, `clear`
- [../load_balancer/api.md](../load_balancer/api.md): `pick`, `finish`, `ForwardTicket`, `ForwardOutcome`
- [../upstream_pool/api.md](../upstream_pool/api.md): `get`, `discard`
- [../load_canceller/api.md](../load_canceller/api.md): `canceller_start`
