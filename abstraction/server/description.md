# Server — Description

An HTTP/2 (h2c, prior knowledge) server with one endpoint, `POST /ping`. It accepts connections and
gives each one its own **Connection Handler**, which runs as its own coroutine or thread. Inside a
connection, requests are processed **one at a time, in arrival order**. Each `/ping` request costs a
random amount of pure CPU time (the ServiceTime, `Exp(response_rate)`), after which the server answers
`202` with a PaddedJsonBody of `response_bytes`.

How the server reacts to client cancellation (`RST_STREAM`) is set by `cancel_mode`:

- `ignore`: the server never looks at cancellations. It burns the full ServiceTime for every
  request, including ones that are already cancelled, and tries to write the response, which the HTTP/2
  layer drops.
- `cooperative`: the server skips requests cancelled before they start and stops the CPU burn once a
  cancellation is seen (checked every `cancel_check_interval_us`).

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
