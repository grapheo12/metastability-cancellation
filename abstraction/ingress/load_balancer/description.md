# Load Balancer — Description

Leaf component. The **LoadBalancer** chooses the backend (`server_id`) for each `/ping` the Ingress forwards, by
the **LbPolicy** named in `lb_policy`, and keeps the **BackendStats** of every backend:

- `round_robin`: backends in turn.
- `least_outstanding`: the backend with the fewest forwards in progress.
- `power_of_two`: two different random backends, then the less loaded of the two.

It counts each backend's forwards in progress (`outstanding`) and how each forward ended, for the policies and for
the Ingress summary.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
