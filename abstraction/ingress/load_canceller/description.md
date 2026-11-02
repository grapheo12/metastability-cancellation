# Load Canceller — Description

Leaf component of the Ingress (it used to be its own program, `canceller`). It is the **LoadCanceller**: a source
of server-directed cancellations that knows exactly which requests are in flight.

- It runs inside the Ingress and starts when the Ingress receives its first `/ping`. It opens **one** HTTP/2 (h2c)
  connection to each backend, used only for `/cancel`, and sends from a **single sender**.
- It sends `DELETE /cancel` requests at the arrivals of a Poisson process of rate `cancel_rate`
  (**CancelSchedule**), for `duration_s`. It is open loop: it never waits for a response before the next arrival.
- For each arrival it takes a snapshot of the Ingress's **InFlightMap** and picks one entry uniformly at random
  (**InFlightVictimChoice**). It cancels that exact RequestId at the backend that is serving it. If nothing is in
  flight, the arrival is `skipped`.
- Collects responses as they arrive, waits a bounded time for the outstanding ones at the end (**CancellerDrain**),
  and keeps one **CancelRecord** per arrival for the Ingress outputs.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
