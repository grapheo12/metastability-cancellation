# Ping Handler — API

## Functions

### `handle_ping(task: StreamTask, config: ServerConfig, rng: Rng, cancel_ctx) -> Response | Aborted`

- `Response(202, body)`: the burn completed. Body is a PaddedJsonBody.
- `Response(503, body)`: a `/cancel` matched (`cooperative` only). Body is a PaddedJsonBody.
- `Response(400, empty)`: the body is unparseable, or its client id differs from the connection's.
- `Aborted`: the RST flag stopped the request (`cooperative` only). Nothing is written.

`cancel_ctx` holds the connection's `ConnectionCancelState` plus the process-wide `registry` and
`counters` ([../connection_handler/design.md#shared-state](../connection_handler/design.md#shared-state)).
The function is called by the SerialStreamWorker
([../connection_handler/design.md#routing](../connection_handler/design.md#routing)), only from the
connection's single worker, so it uses `rng` and the state exclusively. See
[design.md#handle_ping](design.md#handle_ping).

## Uses

- [../../protocol/api.md](../../protocol/api.md): `parse_body`, `split_request_id`, `build_body`
- [../../sampling/api.md](../../sampling/api.md): `exponential_us`
- [../cpu_burner/api.md](../cpu_burner/api.md): `burn`
- [../cancel_tracker/api.md](../cancel_tracker/api.md): `should_cancel`, `CancelCounters`
- [../api.md](../api.md#constants): `CHECK_ITER`
