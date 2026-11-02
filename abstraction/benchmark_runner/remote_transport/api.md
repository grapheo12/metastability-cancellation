# Remote Transport — API

## Types

### `Transport`

An open handle to one machine, created from a `[machines.<name>]` table
([../config/api.md#machinesserver-and-machinesclient](../config/api.md#machinesserver-and-machinesclient)). See
[design.md#transport](design.md#transport).

### `CommandResult`

| Field | Type | Meaning |
|---|---|---|
| `exit_status` | integer | Exit status of the command. `-1` if it timed out and was killed |
| `stdout` | string | Standard output, decoded as UTF-8 with replacement |
| `stderr` | string | Standard error, decoded the same way |

### `TransportError`

Raised when the transport itself fails: SSH cannot connect or authenticate, a copy fails, or the local command
cannot be started. Distinct from a command that runs and exits non-zero.

## Functions

### `open(machine) -> Transport`
Builds the variant chosen by `host`. It does not connect yet. SSH options left unset in the machine table come from
the operator's SSH configuration. See [design.md#transport](design.md#transport) and
[design.md#sshconfighost](design.md#sshconfighost).

### `ssh_hostname(host: string) -> string`
The `HostName` the operator's SSH configuration gives `host` (from `ssh -G`), or `host` itself if there is none.
Runs on the operator machine and never connects. Used by the config for the default `data_addr`. See
[design.md#sshconfighost](design.md#sshconfighost).

### `run(t, command: string, timeout_s: float) -> CommandResult`
Runs `command` with `sh -c` on the machine and waits for it, killing it after `timeout_s`. See
[design.md#run](design.md#run).

### `home(t) -> path`
The machine's home directory, absolute. It is computed on first use and cached. Used to resolve a leading `~`.
See [design.md#run](design.md#run).

### `start_background(t, command: string, log_path: path) -> pid`
Starts `command` detached from the session, with stdout and stderr appended to `log_path`, and returns its
process id at once. See [design.md#background-processes](design.md#background-processes).

### `signal(t, pid, name: "INT" | "KILL") -> ok | gone`
See [design.md#background-processes](design.md#background-processes).

### `alive(t, pid) -> boolean`
See [design.md#background-processes](design.md#background-processes).

### `fetch(t, remote_path: path, local_path: path) -> ok | missing`
Copies one file. Returns `missing` if the remote file does not exist. See [design.md#fetch](design.md#fetch).

### `quote(word: string) -> string`
POSIX shell quoting of one word. See [design.md#command-quoting](design.md#command-quoting).
