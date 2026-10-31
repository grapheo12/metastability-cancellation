# Ping Handler — API

## `handle_ping(task: StreamTask, config: ServerConfig, rng: Rng) -> Response | Aborted`

- `Response(status, body)`: `202` with a PaddedJsonBody, or `400` with an empty body.
- `Aborted`: the request was cancelled and `cancel_mode == cooperative`. Nothing is written.

Called by the SerialStreamWorker ([../connection_handler/design.md#routing](../connection_handler/design.md#routing)).
It is called only from the connection's single worker, so it uses `rng` exclusively.

## Uses

- [../../protocol/api.md](../../protocol/api.md): `parse_body`, `build_body`
- [../../sampling/api.md](../../sampling/api.md): `exponential_us`
- [../cpu_burner/api.md](../cpu_burner/api.md): `burn`
