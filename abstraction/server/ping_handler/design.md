# Ping Handler — Design

## handle_ping

```
function handle_ping(task, config, rng):
    id = protocol.parse_body(task.body)
    if id is ParseError:
        return Response(400, empty)

    service_us = sampling.exponential_us(rng, config.response_rate)      # see Sampling order

    if config.cancel_mode == cooperative:
        if task.cancelled.load():
            return Aborted                                               # skipped: never started
        outcome = cpu_burner.burn(service_us, task.cancelled, config.cancel_check_interval_us)
        if outcome == CANCELLED:
            return Aborted
    else:  # ignore
        cpu_burner.burn(service_us, NONE, config.cancel_check_interval_us)

    status = 202                                                          # v1 never returns 503
    return Response(status, protocol.build_body(id, config.response_bytes))
```

## Sampling order

`exponential_us` is called **exactly once for each request whose body parses, before any
cancellation check**, in both modes. The `k`-th valid request on connection `c` therefore always
gets the `k`-th sample of that connection's stream, whatever the cancellation timing. A `400`
request draws no sample.

## Cancel mode behavior

- `cooperative`: there are two checks. Before the burn, a request already cancelled is skipped. During
  the burn, the CPU Burner polls the flag. A third check, before writing, belongs to the
  Connection Handler ([../connection_handler/design.md#response-writing](../connection_handler/design.md#response-writing)).
- `ignore`: the flag is never read. The full `service_us` is always burned and a response is
  always returned to the writer.

## Response status

v1 always answers `202`. The `503` status (`STATUS_REJECTED`) is reserved for a future
admission or deadline policy. When it is used, it must carry the same PaddedJsonBody as `202`.
