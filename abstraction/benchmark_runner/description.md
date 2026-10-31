# Benchmark Runner — Description

Subcomponents: Config, Remote Transport, Remote Setup, Experiment Executor, Result Plotter.

The **BenchmarkRunner** runs on an operator machine (e.g. a laptop) and turns one TOML file into a complete,
reproducible experiment on two remote machines:

- **Config** reads and validates the **BenchConfig**. It expands every program parameter given as an array into
  the **SweepExpansion**, the cross product of all swept values, made of **SweepPoints**.
- **Remote Transport** is the **Transport** to each machine: run a shell command, start a background process,
  signal it, and copy a file back. It works over SSH, or locally for testing.
- **Remote Setup** first fixes the **SourcePin**: the operator's current `impl/<lang>` branch and commit, which must
  already be at `repo_url`. Then, once per machine, it clones or updates the repository, checks out that exact
  commit, checks the tools the implementation's **BuildManifest** requires (it never installs them), and builds
  the binaries.
- **Experiment Executor** runs each SweepPoint. It starts the server on the server machine (**ServerLifecycle**),
  runs the load canceller and the load generator together on the client machine, stops the server with an
  interrupt so it prints its CancelCounters, and downloads every output.
- **Result Plotter** builds the **PointTable** of all points and draws the **SweepPlots** over every swept value.

Setup happens once per run. Only the experiment repeats per SweepPoint.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
