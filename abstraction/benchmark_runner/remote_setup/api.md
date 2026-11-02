# Remote Setup — API

## Constants

| Name | Value | Meaning |
|---|---|---|
| `MANIFEST_PATH` | `implementation/<lang>/benchmark_manifest.toml` | Where each implementation's BuildManifest lives ([design.md#buildmanifest](design.md#buildmanifest)) |
| `BRANCH_PREFIX` | `impl/` | Only branches named `impl/<lang>` can be deployed ([design.md#sourcepin](design.md#sourcepin)) |

## Types

### `SourcePin`

| Field | Type | Meaning |
|---|---|---|
| `repo_url` | string | Where machines clone from |
| `branch` | string | `impl/<lang>` |
| `lang` | string | `<lang>`, the part after `impl/` |
| `commit` | string (40 hex digits) | The commit both machines check out |
| `manifest` | BuildManifest | Read from the operator's checkout of `commit` |

See [design.md#sourcepin](design.md#sourcepin).

### `BuildManifest`

The TOML file each implementation provides:

| Key | Type | Meaning |
|---|---|---|
| `lang` | string | Must equal `<lang>` of the branch |
| `required_tools` | array of strings | Command names that must be on the machine's `PATH` (besides `git` and `sh`) |
| `build` | string | Shell command run in `implementation/<lang>/` that builds all three programs |
| `binaries.server` | path | Server executable, relative to `implementation/<lang>/` |
| `binaries.loadgen` | path | Load generator executable, same base |
| `binaries.ingress` | path | Ingress executable, same base. A manifest that still names `binaries.canceller` is rejected |
| `thread_env` | string, optional | Name of the environment variable that sets a program's worker-thread count in this language's runtime. Needed only when a config sets `threads`. Absent: the implementation has no such knob |

See [design.md#buildmanifest](design.md#buildmanifest).

### `MachineReady`

| Field | Type | Meaning |
|---|---|---|
| `transport` | Transport | The machine's Transport |
| `role` | `"server"` \| `"client"` | Which machine this is |
| `workdir` | absolute path | `workdir` with `~` resolved |
| `data_addr` | string | Server only: the `data_addr` of its machine table, used by the client as the target host |
| `repo_dir` | absolute path | `<workdir>/repo` |
| `binaries` | map program → absolute path | The three executables |
| `thread_env` | string or none | The BuildManifest's `thread_env` |
| `cpu_pinning` | boolean | Whether the machine can pin a process to CPUs: `taskset` is on its `PATH` |

See [design.md#machine-setup](design.md#machine-setup).

### `PinError`, `SetupError`

One-line errors that stop the run before any SweepPoint executes.

## Functions

### `source_pin(repo_url: string) -> SourcePin | PinError`
See [design.md#sourcepin](design.md#sourcepin).

### `setup_machine(t: Transport, role, machine, pin: SourcePin, timeout_s) -> MachineReady | SetupError`
`machine` is the `[machines.<role>]` table from the BenchConfig.
See [design.md#machine-setup](design.md#machine-setup).

## Uses

- [../remote_transport/api.md](../remote_transport/api.md): `run`, `home`, `quote`
