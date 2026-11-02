# Frontend — Description

Leaf component. The **Frontend** is the client-facing side of the Ingress. It accepts HTTP/2 (h2c) connections
from load-generator clients and handles every `/ping` stream on its own coroutine or thread (**ForwardTask**):

- check the body and the client id,
- ask the Load Balancer for a backend,
- publish the request in the In-Flight Map,
- forward it on the Upstream Pool's connection for that client and backend, and wait,
- clear the In-Flight Map entry, report the outcome to the Load Balancer, and relay the backend's answer.

If the client resets its stream, the ForwardTask resets the upstream stream too, so the server sees the client's
SLOCancellation. The first `/ping` the Frontend accepts also sets the Ingress's `run_start` and starts the Load
Canceller.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
