# Ping Handler — Description

Leaf component. Handles one routed `POST /ping` in two steps.

On the Connection Handler, when the request is fully received (**admit**):

- Parse the RequestId and bind the connection to its client id (`id >> 32`) on the first `/ping`.
- Sample a ServiceTime from the connection's Rng (`workload = cpu_burn` only).
- Package the request as a **PingJob** for the WorkQueue, or answer `400` straight away.

On a worker of the WorkerPool (**run**):

- Do the request's work: burn the ServiceTime (`cpu_burn`), or **MessageWork** (`message_buffer`): create
  `n_messages_per_request` messages, then write them into the MessageBuffer under its lock.
- In `cooperative` mode, run the **CancelCheckPoints**: once before the work, and inside it (for a burn, RST
  every `cancel_check_interval_us` and `/cancel` every `CHECK_ITER` spin units; for MessageWork, both after each
  message).
- Answer `202` if the work completed, `503` if a `/cancel` stopped it, or nothing if an RST stopped it.
  Responses echo the RequestId in a PaddedJsonBody of `response_bytes`.
- Publish the request's id as the client's **FinishedWatermark** whichever way it finished, so `/cancel` replies
  can report it.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
