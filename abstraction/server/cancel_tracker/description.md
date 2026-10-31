# Cancel Tracker — Description

Leaf component. It holds everything the server needs for server-directed cancellation:

- **CancelRegistry**: a process-wide, lock-free, fixed-capacity map from client id to a CancelInbox.
  Entries are created on first use and never deleted.
- **CancelInbox**: a lock-free multi-producer stack of RequestIds for one client. `/cancel` handlers
  push onto it. The single worker bound to that client drains it all at once.
- **CancelMatching**: the per-connection algorithm the worker runs at each CancelCheckPoint. It
  moves drained ids into a local ordered pending set and decides whether the current request is
  cancelled. Along the way it counts cancels that arrived too late.
- **CancelCounters**: three process-wide atomic counters.
- **`handle_cancel`**: the `/cancel` request handler.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
