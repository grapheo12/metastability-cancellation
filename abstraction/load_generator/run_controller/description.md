# Run Controller — Description

Leaf component. It runs the whole load generator: it validates the config, opens one h2c connection per
client, fixes the shared `run_start` time origin, starts all clients at once, waits for all of them
to return (the active phase plus the **DrainPhase**), closes the connections, and passes the records to
Metrics for output.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
