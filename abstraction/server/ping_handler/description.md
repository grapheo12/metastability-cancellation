# Ping Handler — Description

Leaf component. Handles one routed `POST /ping` StreamTask:

- Parse the RequestId and bind the connection to its client id (`id >> 32`) on the first `/ping`.
- Sample a ServiceTime.
- In `cooperative` mode, run the **CancelCheckPoints**: once before the burn, and inside the burn
  (RST every `cancel_check_interval_us`, `/cancel` every `CHECK_ITER` spin units).
- Answer `202` if the burn completed, `503` if a `/cancel` stopped it, or nothing if an RST stopped it.
  Responses echo the RequestId in a PaddedJsonBody of `response_bytes`.
- Publish the request's id as the client's **FinishedWatermark** whichever way it finished, so `/cancel` replies
  can report it.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
