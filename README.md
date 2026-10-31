# Metastability & Cancellation Testbed

An HTTP/2 testbed for measuring how **request cancellation** affects tail latency and wasted work in a
CPU-bound service. A server burns CPU for each `POST /ping`. A load generator drives it with Poisson,
closed-loop clients that cancel requests once they miss their SLO. A load canceller sends server-directed
`DELETE /cancel` requests. A benchmark runner sweeps parameters across two machines and plots the results.

The repository is also an experiment in **natural language as source code**. The Markdown specs are the
source of truth, and the code in each language is a build artifact derived from them. The rules for agents
and humans working here are in [AGENTS.md](AGENTS.md).

## Repository structure

```
.
|- AGENTS.md              # process rules: spec style, traceability, commits, macros (on every branch)
|- README.md              # this file (on every branch)
|- abstraction/           # the specification tree (on every branch)
|  |- description.md      #   what and why
|  |- api.md              #   external interface
|  |- design.md           #   components, diagrams, links to children
|  |- version.md          #   version history of this node
|  |- <component>/        #   same four files, recursively, down to leaf components
|- implementation/        # only on impl/<lang> branches
   |- <lang>/             #   one language implementation per branch, e.g. go/
   |- spec_tools/         #   shared tool: tracegen (traceability tables) and linkcheck (link checker)
   |- scripts/            #   shared tool: the BenchmarkRunner (bench.py)
```

### `abstraction/`: the specification

A tree of components, described top-down. Each node has the same four files, and each level removes
ambiguity until the leaf `design.md` files contain exact pseudocode, state machines and worked examples.

| Component | What it specifies |
|---|---|
| [protocol](abstraction/protocol/description.md) | RequestId layout, JSON bodies, status codes |
| [sampling](abstraction/sampling/description.md) | Seeded RNG, exponential and uniform samplers |
| [server](abstraction/server/description.md) | HTTP/2 server: connection handler, ping handler, CPU burner, cancel tracker |
| [load_generator](abstraction/load_generator/description.md) | Closed-loop Poisson clients with SLO cancellation, metrics |
| [load_canceller](abstraction/load_canceller/description.md) | Open-loop `/cancel` request source |
| [benchmark_runner](abstraction/benchmark_runner/description.md) | Two-machine parameter sweeps, result download, plots |

Start with [abstraction/description.md](abstraction/description.md) (problem and glossary), then
[abstraction/api.md](abstraction/api.md) and [abstraction/design.md](abstraction/design.md).

### Branches

| Branch | Contents |
|---|---|
| `main` | `abstraction/`, `AGENTS.md` and `README.md`. No code, ever |
| `impl/<lang>` | Everything on `main`, plus `implementation/<lang>/`, the shared tools, and a `Design specific to <Lang>` section at the end of each leaf `design.md` that maps every design subsection to exact code lines |

Currently there is one implementation: **`impl/go`**.

The spec and the code link to each other. Code carries `Design: abstraction/...design.md#anchor` comments,
and `tracegen` turns them into the tables in each `Design specific to <Lang>` section. `AGENTS.md` defines
two macros for keeping branches consistent: `sync_spec` (bring spec changes made on an impl branch to `main`
and every other branch) and `sync_impl <branch>` (bring an implementation up to date with the spec).

## Running an experiment (from `impl/go`)

The BenchmarkRunner runs on your machine (e.g. a laptop) and drives two experiment machines over SSH. The
**server machine** runs the server. The **client machine** runs the load generator and the load canceller.

### 1. Prerequisites

On your machine:
- A checkout of this repository on branch `impl/go`, committed and pushed: the runner deploys exactly your
  `HEAD`, and refuses to run if `origin` has a different commit for `impl/go`.
- Python 3.11 or newer, and matplotlib:
  `python3 -m pip install -r implementation/scripts/requirements.txt`.
- SSH access to both machines with a private key, without a passphrase prompt (e.g. via `ssh-agent`).

On both experiment machines:
- `git` and Go (`go` on `PATH`). The runner checks for them but never installs anything.
- Read access to the repository: a GitHub SSH key for `git@github.com:...` URLs, or use the `https://` URL
  if the repository is public.
- The client machine must reach the server machine on the configured `listen_port` (default `8080`).

### 2. Write a config

```
git checkout impl/go
cp implementation/scripts/example_bench.toml my_bench.toml
```

Edit `my_bench.toml`:
- `[repo] url`: where the machines clone from. It defaults to your `origin`.
- `[machines.server]` and `[machines.client]`: `host`, `user`, `ssh_key`, and optionally `ssh_port` and
  `workdir`. On the server, `data_addr` is the address the client uses to reach it (e.g. a private IP).
- `[server]`, `[load_gen]`, `[canceller]`: program parameters, named exactly as in
  [abstraction/api.md](abstraction/api.md). **Any parameter given as an array is swept**, and the runner runs
  the full cross product.
- `[run]`: `output_dir`, `sweep_order` (the first key is the plots' x-axis), `cooldown_s`, `on_failure`, and
  timeouts.

The full schema is in [abstraction/benchmark_runner/config/api.md](abstraction/benchmark_runner/config/api.md).
An untracked config file in the checkout is fine, because only tracked changes block a run.

### 3. Run

```
python3 implementation/scripts/bench.py run my_bench.toml     # setup + every sweep point + plots
python3 implementation/scripts/bench.py setup my_bench.toml   # only clone, check out, check tools, build
python3 implementation/scripts/bench.py plot <output_dir>     # redraw table and plots
```

`run` sets up both machines once (clone into `workdir`, check out your exact commit, build with
`implementation/go/benchmark_manifest.toml`). Then, for each sweep point, it starts the server, runs the
canceller and the load generator together, stops the server with SIGINT so it prints its cancellation
counters, and downloads everything.

Exit status: `0` if every point succeeded, `1` if some point failed (the rest are still plotted), and `2` for a
config, pin or setup error.

### 4. Results

By default results go to `implementation/scripts/results/<UTC timestamp>/`, which git ignores:

```
config.toml, run.json            # the config used, branch and commit
points/point_NNNN/               # per point: params.json, loadgen/canceller CSVs and summaries,
                                 # server.out, server_counters.json, and a done or failed marker
all_points.csv                   # one row per point: swept values and key metrics
plots/latency_e2e.png  goodput.png  outcomes.png  server_counters.png
```

Runs can be resumed. Set `[run] output_dir` to an existing run directory and run again: points already marked
`done` are skipped.

### Trying it on one machine

Set `host = "local"` for both machines (with the same `workdir`). Everything then runs on your own machine,
cloning from `[repo] url`, which may be a local path such as `file:///path/to/this/checkout`.

### Building and running the programs by hand

```
cd implementation/go
CGO_ENABLED=0 go build -o bin/ ./cmd/server ./cmd/loadgen ./cmd/canceller
./bin/server --response_rate 2000 --response_bytes 128 --cancel_mode cooperative
./bin/loadgen --num_clients 8 --global_rate 3000 --request_bytes 128 --slo_ms 5 --duration_s 3
./bin/canceller --num_clients 8 --cancel_rate 2000 --duration_s 3
```

`CGO_ENABLED=0` is required on recent macOS with Go 1.21. All parameters are documented in
[abstraction/api.md](abstraction/api.md).

## Checks

On an `impl/<lang>` branch, from the repository root:

```
python3 implementation/spec_tools/linkcheck.py                                       # spec links resolve
python3 implementation/spec_tools/tracegen.py --lang Go --ext .go --comment //       # Go traceability
python3 implementation/spec_tools/tracegen.py --lang Python --impl implementation/scripts --ext .py --comment '#'
(cd implementation/go && CGO_ENABLED=0 go test ./...)
python3 -B -m unittest discover -s implementation/spec_tools/tests -t implementation/spec_tools
python3 -B -m unittest discover -s implementation/scripts/tests -t implementation/scripts
```
