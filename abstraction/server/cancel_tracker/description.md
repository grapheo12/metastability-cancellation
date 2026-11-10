# Cancel Tracker — Description

Leaf component. It holds everything the server needs for server-directed cancellation:

- **CancelRegistry**: a process-wide, fixed-capacity map from client id to a slot holding the client's
  CancelInbox, its ClientCancelState and its FinishedWatermark. Entries are created on first use and never deleted.
- **CancelInbox**: a lock-free multi-producer stack of RequestIds for one client. `/cancel` handlers
  push onto it. The worker running that client's request drains it all at once.
- **ClientCancelState**: per client, the drained `/cancel` ids not yet resolved, the requests started and not yet
  finished, and a ring of recently finished requests with their outcomes, guarded by a per-client lock because
  any worker may run any client's request and a client has many requests at once.
- **CancelMatching**: the per-client algorithm a worker runs at each CancelCheckPoint. It matches `/cancel` ids to
  requests **by id**, whatever order the requests arrive, run and finish in, and decides whether the current
  request is cancelled. Along the way it counts cancels that arrived too late.
- **CancelCounters**: three process-wide atomic counters.
- **FinishedWatermark**: per client, the highest RequestId the server has finished with any outcome, kept in the
  client's registry slot.
- **`handle_cancel`**: the `/cancel` request handler. It answers with the target client's FinishedWatermark, so
  the canceller can aim at requests that have not finished yet. It never waits for a worker.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
