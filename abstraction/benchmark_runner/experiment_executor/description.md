# Experiment Executor — Description

Leaf component. It runs one SweepPoint from start to finish and leaves a self-contained directory of results:

- **ServerLifecycle**: stop any servers left over from an earlier point, start this point's `instances` servers
  (one port each) in the background on the server machine, wait for each one's `listening on` line, and at the end
  interrupt them so they print their CancelCounters lines and exit.
- **IngressLifecycle**: start the Ingress on the machine its IngressPlacement names (client by default, or the
  server machine), pointed at every server instance, and wait for its `listening on` line; at the end interrupt
  it so it writes its outputs.
- **ClientRun**: on the client machine, run the load generator against the Ingress, with a timeout.
- **ProcessPlacement**: start each program with its thread count and, where the machine can pin, its CPU set.
- **Point outputs**: download every output file, parse and sum the servers' CancelCounters, and mark the point `done` or
  `failed` with a reason. Points already `done` are skipped, so an interrupted run can be resumed.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
