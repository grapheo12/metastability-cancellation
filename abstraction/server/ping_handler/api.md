# Ping Handler — API

A `POST /ping` is handled in two steps on two different activities: `admit_ping` on the Connection Handler, and
`run_ping` on a worker of the WorkerPool. See [design.md#handle_ping](design.md#handle_ping).

## Functions

### `admit_ping(task: StreamTask, config: ServerConfig, rng: Rng, conn_state: ConnectionCancelState) -> PingJob | Response`

- `PingJob`: the body parses and its client id matches the connection's binding (or binds it). The job carries
  the RequestId, the client id and a freshly drawn ServiceTime (`0` with `workload = message_buffer`, which
  draws nothing), and has an empty `reply`
  ([../worker_pool/api.md#pingjob](../worker_pool/api.md#pingjob)).
- `Response(400, empty)`: the body is unparseable, or its client id differs from the connection's. No sample is
  drawn.

Called by the Connection Handler for every routed `POST /ping`, one call at a time per connection and in the
order the requests were fully received, so it uses `rng` and `conn_state` exclusively. See
[design.md#admit_ping](design.md#admit_ping).

### `run_ping(job: PingJob, config: ServerConfig, registry: CancelRegistry, counters: CancelCounters, buf: MessageBuffer | NONE) -> Response | Aborted`

- `Response(202, body)`: the burn (`cpu_burn`) or the MessageWork (`message_buffer`, which writes into `buf`)
  completed. Body is a PaddedJsonBody. See [design.md#messagework](design.md#messagework).
- `Response(503, body)`: a `/cancel` matched (`cooperative` only). Body is a PaddedJsonBody.
- `Aborted`: the RST flag stopped the request (`cooperative` only). Nothing is written.

Called by a worker ([../worker_pool/design.md#workerpool](../worker_pool/design.md#workerpool)). Any worker may
run any client's job, and two jobs of one client may run at once; the client's shared state is reached through
`registry` only. See [design.md#run_ping](design.md#run_ping).

## Uses

- [../../protocol/api.md](../../protocol/api.md): `parse_body`, `split_request_id`, `build_body`
- [../../sampling/api.md](../../sampling/api.md): `exponential_us`
- [../cpu_burner/api.md](../cpu_burner/api.md): `burn`, `BurnOutcome`
- [../message_buffer/api.md](../message_buffer/api.md): `make_message`, `push_messages`, `MessageBuffer`
- [../cancel_tracker/api.md](../cancel_tracker/api.md): `ConnectionCancelState`, `registry_client`, `registry_start`, `should_cancel`, `registry_publish_finished`, `CancelCounters`
- [../worker_pool/api.md](../worker_pool/api.md): `PingJob`
- [../api.md](../api.md#constants): `CHECK_ITER`
