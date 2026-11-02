# Load Canceller — Description

Leaf component, and its own program (`canceller`). It is the **LoadCanceller**: a source of
server-directed cancellations that runs alongside the load generator and independently of it.

- Opens **one** HTTP/2 (h2c) connection to the server and sends from a **single sender**.
- Sends `DELETE /cancel` requests at the arrivals of a Poisson process of rate `cancel_rate`
  (**CancelSchedule**). It is open loop: it never waits for a response before the next arrival.
- For each arrival it picks a client id uniformly from `0 ..= N-1` and aims a random step in
  `[1, incr_rand_max]` past the last request the server reported as finished for that client
  (**CancelTargetChoice**). The server reports that **FinishedWatermark** in every `202` answer to
  `/cancel`. The RequestId it cancels is `(client_id << 32) | seq`. It usually names the request in
  flight or one that is about to be sent, but it can still name one that has finished since the last
  reply, or one that will never be sent.
- Collects responses as they arrive, stops sending at `duration_s`, waits a bounded time for the
  outstanding responses (**CancellerDrain**), and writes one **CancelRecord** per arrival plus a
  summary.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
