# In-Flight Map — Description

Leaf component. The **InFlightMap** is a process-wide, lock-free map from RequestId to the **InFlightEntry** of
each forward the Ingress is currently waiting on:

- The Frontend **publishes** an entry `(request_id, server_id)` when it starts forwarding a client's `/ping`, and
  **clears** it when the forward ends, whatever the outcome.
- The Load Canceller takes a **snapshot** of all entries to choose a request to cancel.

A load-generator client keeps up to `n_waiting_requests` requests outstanding, so the map holds up to that many
entries per client. Valid client ids are bounded by `num_clients`; the map's storage representation is not part of its behavior.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
