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
| `host` | string | required | SSH host name or IP, or `"local"` to use the operator machine itself |
| `user` | string | required unless `host = "local"` | SSH user |
| `ssh_key` | path | required unless `host = "local"` | Private key file. A leading `~` is the operator's home |
| `ssh_port` | integer `1..=65535` | `22` | SSH port |
| `workdir` | path on that machine | `"~/cancellation-bench"` | Where the clone and per-point files live. A leading `~` is that machine's home |
| `data_addr` | string | `host` (`"127.0.0.1"` when `host = "local"`) | Server table only: the address the client machine uses to reach the server |

### `[server]`

| Key | Type | Default | Meaning |
|---|---|---|---|
| `listen_port` | integer `1..=65535` | `8080` | Runner-owned. The server gets `--listen_addr 0.0.0.0:<listen_port>`. Cannot be swept |
| any server parameter except `listen_addr` | scalar or non-empty array | the program's default | See [../../api.md#server-parameters](../../api.md#server-parameters) |

### `[load_gen]`

| Key | Type | Default | Meaning |
|---|---|---|---|
| any load generator parameter except `target`, `output_path` | scalar or non-empty array | the program's default | See [../../api.md#load-generator-parameters](../../api.md#load-generator-parameters) |

### `[canceller]`

| Key | Type | Default | Meaning |
|---|---|---|---|
| `enabled` | boolean | `true` | Runner-owned. Whether the canceller runs at all. Cannot be swept |
| any canceller parameter except `target`, `num_clients`, `output_path` | scalar or non-empty array | `duration_s`: the point's load generator `duration_s`. Others: the program's default | See [../../api.md#canceller-parameters](../../api.md#canceller-parameters) |

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
`load_gen`, `canceller`, `run`. Program sections keep their keys in file order. See
[design.md#benchconfig](design.md#benchconfig).

### `SweepPoint`

| Field | Type | Meaning |
|---|---|---|
| `id` | integer `>= 0` | Position in the SweepExpansion |
| `swept` | ordered map `"<section>.<param>"` → scalar | This point's value for every swept key, in sweep order. Empty if nothing is swept |
| `server_args` | ordered list of (name, scalar) | Every `[server]` program parameter for this point, excluding runner-owned keys |
| `loadgen_args` | ordered list of (name, scalar) | Every `[load_gen]` program parameter for this point |
| `canceller_args` | ordered list of (name, scalar), or none | Every `[canceller]` program parameter, plus `num_clients` and the defaulted `duration_s`. None if the canceller is disabled |

It is serialized as `params.json` with these field names. See [design.md#sweepexpansion](design.md#sweepexpansion).

## Functions

### `load_config(path) -> BenchConfig | ConfigError`
See [design.md#benchconfig](design.md#benchconfig).

### `expand_sweep(config: BenchConfig) -> list of SweepPoint`
See [design.md#sweepexpansion](design.md#sweepexpansion).

### `format_arg(value: scalar) -> string`
How a scalar is written on a command line. See [design.md#argument-formatting](design.md#argument-formatting).
