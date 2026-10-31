# Config — Description

Leaf component. It turns the operator's TOML file into a validated **BenchConfig** and expands it into the
**SweepExpansion**:

- Program parameters are written under the program they belong to, with exactly the parameter names from
  [../../api.md](../../api.md). A scalar value is fixed for the whole run. An array value is **swept**.
- The SweepExpansion is the cross product of all swept arrays, in a fixed, documented order. Each element is a
  **SweepPoint**, which holds the complete argument lists of the three programs for one experiment.
- Parameters the runner owns (addresses, ports, output paths, the canceller's client count) cannot be set, which
  keeps the three programs consistent with each other.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
