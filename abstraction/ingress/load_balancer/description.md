# Load Balancer — Description

Leaf component. The **LoadBalancer** chooses the backend (`server_id`) for each `/ping` the Ingress forwards, by
the **LbPolicy** named in `lb_policy`, and keeps the **BackendStats** of every backend:

- `round_robin`: backends in turn.
- `least_outstanding`: the backend with the fewest forwards in progress.
- `power_of_two`: two different random backends, then the less loaded of the two.
- `ewma`: two different random backends, then the one with the lower **EwmaScore**, which weighs its forwards in
  progress by its **EwmaLatency** (a moving average of its recent response times).

With `lb_filter = deadline`, the **DeadlineFilter** first narrows the candidates to the backends expected to reach
the request before its RequestDeadline (as an M/M/k queue given its `outstanding`, WorkerCount and EwmaExecTime, the request finishes within its time left with probability at least 0.99); if none is, the
request is dropped instead of forwarded.

It counts each backend's forwards in progress (`outstanding`) and how each forward ended, for the policies and for
the Ingress summary. Each `pick` hands out a **ForwardTicket** that its `finish` gives back, so the LoadBalancer
measures every forward's latency itself. Every `finish` appends a **ForwardRecord** (time, backend, outcome,
latency, EwmaLatency after it, and the backend's reported ExecutionTime with its **EwmaExecTime**) to the **ForwardLog**, which the Ingress writes out at shutdown.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
