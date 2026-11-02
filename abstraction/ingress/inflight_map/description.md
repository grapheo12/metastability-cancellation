# In-Flight Map — Description

Leaf component. The **InFlightMap** is a process-wide, lock-free map from client id to the **InFlightEntry** of the
forward the Ingress is currently waiting on for that client:

- The Frontend **publishes** an entry `(request_id, server_id)` when it starts forwarding a client's `/ping`, and
  **clears** it when the forward ends, whatever the outcome.
- The Load Canceller takes a **snapshot** of all entries to choose a request to cancel.

A load-generator client keeps at most one request outstanding, so one slot per client is enough. The map is
therefore a fixed array indexed by client id, sized `num_clients`.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
