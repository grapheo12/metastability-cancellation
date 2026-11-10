# Config — API

## Config file

A TOML document. Every table and key is listed here. Unknown tables or keys are errors. See
[design.md#benchconfig](design.md#benchconfig).

### `[repo]`

| Key | Type | Default | Meaning |
|---|---|---|---|
| `url` | string | the operator repository's `origin` remote URL | Where both machines clone from. Required if there is no `origin` |

### `[machines.server]` and `[machines.client]`

Both tables are required.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `host` | string | required | SSH host name, IP, or a `Host` alias from the operator's `~/.ssh/config`; or `"local"` to use the operator machine itself |
| `user` | string | unset: from the operator's SSH configuration | SSH user. Ignored for `local` |
| `ssh_key` | path | unset: from the operator's SSH configuration | Private key file. A leading `~` is the operator's home. Ignored for `local` |
| `ssh_port` | integer `1..=65535` | unset: from the operator's SSH configuration, else 22 | SSH port. Ignored for `local` |
| `workdir` | path on that machine | `"~/cancellation-bench"` | Where the clone and per-point files live. A leading `~` is that machine's home |
| `data_addr` | string | the `HostName` of `host` in the operator's SSH configuration, else `host` (`"127.0.0.1"` when `host = "local"`) | Server table only: the address the client machine uses to reach the server. Set it to a private address when the machines share a network |

A key set here overrides the SSH configuration ([../remote_transport/design.md#sshconfighost](../remote_transport/design.md#sshconfighost)).

### `[server]`

| Key | Type | Default | Meaning |
|---|---|---|---|
| `listen_port` | integer `1..=65535` | `8080` | Runner-owned. Server instance `k` gets `--listen_addr 0.0.0.0:<listen_port + k>`. Cannot be swept |
| `instances` | integer `1..=64`, and `listen_port + instances - 1 <= 65535` | `1` | Runner-owned. Number of server processes on the server machine, all with the same program parameters. They are the Ingress's backends. Cannot be swept |
| `threads`, `cpu_set` | see [ProcessPlacement keys](#processplacement-keys) | unset | Runner-owned. The server's ProcessPlacement. Cannot be swept |
| any server parameter except `listen_addr`, `queue_log_path` | scalar or non-empty array | the program's default (`queue_sample_interval_ms`: 10) | See [../../api.md#server-parameters](../../api.md#server-parameters) |

### `[load_gen]`

| Key | Type | Default | Meaning |
|---|---|---|---|
| `threads`, `cpu_set` | see [ProcessPlacement keys](#processplacement-keys) | unset | Runner-owned. The load generator's ProcessPlacement. Cannot be swept |
| any load generator parameter except `target`, `output_path` | scalar or non-empty array | the program's default | See [../../api.md#load-generator-parameters](../../api.md#load-generator-parameters) |

### `[ingress]`

The Ingress always runs, on the machine named by `machine` (its **IngressPlacement**). The load generator talks to
it, and it forwards to the server instances ([../../ingress/description.md](../../ingress/description.md)).

| Key | Type | Default | Meaning |
|---|---|---|---|
| `machine` | `"client"` \| `"server"` | `"client"` | Runner-owned **IngressPlacement**: which experiment machine runs the Ingress. Cannot be swept. See [design.md#ingressplacement](design.md#ingressplacement) |
| `listen_port` | integer `1..=65535` | `9090` | Runner-owned. The Ingress gets `--listen_addr 0.0.0.0:<listen_port>`; the load generator's `--target` is this port on the Ingress's machine ([design.md#ingressplacement](design.md#ingressplacement)). Cannot be swept |
| `threads`, `cpu_set` | see [ProcessPlacement keys](#processplacement-keys) | unset | Runner-owned. The Ingress's ProcessPlacement. Cannot be swept |
| `cancel_rate_fraction` | float `> 0`, scalar or non-empty array | unset | Runner-owned **CancelRateFraction**: the Ingress's `cancel_rate` becomes `fraction × global_rate` of the same SweepPoint. It **can** be swept. Setting it together with `cancel_rate` is an error. See [design.md#cancelratefraction](design.md#cancelratefraction) |
| any Ingress parameter except `listen_addr`, `backends`, `num_clients`, `output_path` | scalar or non-empty array | `duration_s`: the point's load generator `duration_s`. Others: the program's default (so `cancel_rate` 0, no cancels, unless set) | See [../../api.md#ingress-parameters](../../api.md#ingress-parameters) |

### ProcessPlacement keys

The **ProcessPlacement** of a program says how many worker threads it may use and which CPUs it may run on. Both
keys are optional and runner-owned, and neither can be swept.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `threads` | integer `>= 1` | unset: the language runtime's default | Worker-thread count, passed through the BuildManifest's `thread_env` ([../remote_setup/api.md#buildmanifest](../remote_setup/api.md#buildmanifest)) |
| `cpu_set` | string, a CPU list matching `^[0-9]+(-[0-9]+)?(,[0-9]+(-[0-9]+)?)*$`, e.g. `"0-3"` or `"0,2,4-5"` | unset: no pinning | CPUs the program is pinned to. The Experiment Executor applies it only where the machine supports pinning |

See [design.md#processplacement](design.md#processplacement).

### `[run]`

| Key | Type | Default | Meaning |
|---|---|---|---|
| `output_dir` | path | `"<runner directory>/results/<UTC start time as YYYYMMDDTHHMMSSZ>"` | Where results go. A relative path is relative to the current directory |
| `sweep_order` | array of `"<section>.<param>"` | swept keys in file order | Order of the swept keys. If given, it must list every swept key exactly once |
| `cooldown_s` | float `>= 0` | `2.0` | Pause between SweepPoints |
| `on_failure` | `"continue"` \| `"stop"` | `"continue"` | Whether a failed SweepPoint stops the run |
| `server_ready_timeout_s` | float `> 0` | `30.0` | Longest wait for the server's `listening on` line |
| `stop_timeout_s` | float `> 0` | `30.0` | Longest wait for the server to exit after an interrupt |
| `client_timeout_margin_s` | float `> 0` | `60.0` | Slack added to the client run's expected duration |
| `setup_timeout_s` | float `> 0` | `900.0` | Longest wait for clone plus build on one machine |

A **scalar** is a TOML string, integer, float or boolean. Arrays may not be nested or empty.

## Types

### `BenchConfig`

The validated file with every default filled in: `repo_url`, `machines.server`, `machines.client`, `server`,
`load_gen`, `ingress`, `run`, plus the server's `instances`. Program sections keep their keys in file order. See
[design.md#benchconfig](design.md#benchconfig).

### `SweepPoint`

| Field | Type | Meaning |
|---|---|---|
| `id` | integer `>= 0` | Position in the SweepExpansion |
| `swept` | ordered map `"<section>.<param>"` → scalar | This point's value for every swept key, in sweep order. Empty if nothing is swept |
| `server_args` | ordered list of (name, scalar) | Every `[server]` program parameter for this point, excluding runner-owned keys |
| `loadgen_args` | ordered list of (name, scalar) | Every `[load_gen]` program parameter for this point |
| `ingress_args` | ordered list of (name, scalar) | Every `[ingress]` program parameter, plus the runner's `backends` and `num_clients`, the defaulted `duration_s`, and the derived `cancel_rate` when CancelRateFraction is set |
| `server_instances` | integer `>= 1` | `[server] instances`; instance `k` listens on `listen_port + k` |
| `ingress_machine` | `"client"` \| `"server"` | The IngressPlacement: the machine the Ingress runs on |
| `loadgen_target` | `host:port` | Where the load generator sends its requests: the Ingress, as seen from the client machine |
| `placement` | map program (`server`, `loadgen`, `ingress`) → {`threads`: integer or none, `cpu_set`: string or none} | Each program's ProcessPlacement (the server's applies to every instance). All three programs are always present; unset keys are none |

It is serialized as `params.json` with these field names. See [design.md#sweepexpansion](design.md#sweepexpansion).

## Functions

### `load_config(path) -> BenchConfig | ConfigError`
See [design.md#benchconfig](design.md#benchconfig).

### `expand_sweep(config: BenchConfig) -> list of SweepPoint`
See [design.md#sweepexpansion](design.md#sweepexpansion).

### `format_arg(value: scalar) -> string`
How a scalar is written on a command line. See [design.md#argument-formatting](design.md#argument-formatting).
