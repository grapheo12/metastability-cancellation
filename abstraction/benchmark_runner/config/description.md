# Config — Description

Leaf component. It turns the operator's TOML file into a validated **BenchConfig** and expands it into the
**SweepExpansion**:

- Program parameters are written under the program they belong to, with exactly the parameter names from
  [../../api.md](../../api.md). A scalar value is fixed for the whole run. An array value is **swept**.
- The SweepExpansion is the cross product of all swept arrays, in a fixed, documented order. Each element is a
  **SweepPoint**, which holds the complete argument lists of the three programs for one experiment.
- Parameters the runner owns (addresses, ports, output paths, the Ingress's backends and client count) cannot be set, which
  keeps the three programs consistent with each other.
- Each program can be given a **ProcessPlacement** (a worker-thread count and a CPU set), so the programs can be
  split across the cores of a machine. It is fixed for the whole run.
- The servers run on one or more **ServerMachines**, each with the same number of instances; the Ingress spreads
  the load over all of them.
- The Ingress runs on the client machine or, by its **IngressPlacement**, on the server machine next to the
  servers (only when there is one). That choice sets the load generator's target and the Ingress's backend
  addresses.
- The Ingress's cancel rate can be given as a **CancelRateFraction** of the load generator's `global_rate`, so a
  sweep over offered load keeps the cancel load proportional. The fraction itself can be swept.
- A **FreezeTarget** injects the server's WorkerFreeze into a single server instance, so one server fails while the
  others keep working.
- A **LinkedSweep** pairs an array parameter with a swept key element by element instead of crossing them, so a
  parameter can follow another one (for example a retry timeout chosen per offered rate) without adding a sweep
  dimension.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
