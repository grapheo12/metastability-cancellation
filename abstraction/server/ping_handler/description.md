# Ping Handler — Description

Leaf component. Handles one routed `POST /ping` StreamTask:
parse the RequestId, sample a ServiceTime, burn that much CPU (stopping early only in
`cooperative` mode), and build the `202` response echoing the RequestId in a PaddedJsonBody of
`response_bytes`.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
