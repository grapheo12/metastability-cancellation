# Ingress — Description

Subcomponents: In-Flight Map, Load Balancer, Upstream Pool, Load Canceller, Frontend.

The **Ingress** is a program that sits between the load generator's clients and several **server** instances
(its **backends**). It exposes the same `POST /ping` as a server, so a client cannot tell the difference, but it
does no work itself:

- The **Frontend** accepts the clients' HTTP/2 (h2c) connections. Every `/ping` stream gets its own coroutine or
  thread, which asks the **LoadBalancer** for a backend (by `lb_policy`), forwards the request on an
  **UpstreamPool** connection, waits for the backend's answer, and relays it to the client. A client's
  `RST_STREAM` is passed on to the backend, so SLOCancellation still reaches the server.
- While it waits, the Frontend records `request_id → (server_id, request_id)` in the **InFlightMap**, a global
  lock-free map. It holds exactly the requests the Ingress is currently waiting on.
- The **LoadCanceller** runs inside the Ingress. On its Poisson clock it picks a victim uniformly from the
  InFlightMap (**InFlightVictimChoice**) and sends `DELETE /cancel` for that exact RequestId to the backend that
  holds it. It no longer guesses ids.

The Ingress exists so that experiments can put a load balancer and a well-informed canceller in front of more
than one server, without changing the server or the load generator.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
