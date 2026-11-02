# Experiment Executor — Description

Leaf component. It runs one SweepPoint from start to finish and leaves a self-contained directory of results:

- **ServerLifecycle**: stop any server left over from an earlier point, start the server for this point in the
  background on the server machine, wait for its `listening on` line, and at the end interrupt it so it prints its
  CancelCounters line and exits.
- **ClientRun**: on the client machine, start the load canceller (if enabled) and the load generator together
  against the server, and wait for both, with a timeout.
- **ProcessPlacement**: start each program with its thread count and, where the machine can pin, its CPU set.
- **Point outputs**: download every output file, parse the server's CancelCounters, and mark the point `done` or
  `failed` with a reason. Points already `done` are skipped, so an interrupted run can be resumed.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
