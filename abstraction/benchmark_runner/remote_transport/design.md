# Remote Transport — Design

## Transport

```
function open(machine):
    if machine.host == "local": return LocalTransport()
    return SshTransport(host, user, expand_operator_home(ssh_key), ssh_port)

SSH_OPTIONS = ["-o", "BatchMode=yes",                      # never prompt
               "-o", "StrictHostKeyChecking=accept-new",   # trust a new host once, reject changed keys
               "-o", "ConnectTimeout=10",
               "-o", "ServerAliveInterval=15"]
```

The SSH variant runs the operator's system `ssh` and `scp` programs as child processes, one per operation.
It holds no persistent connection, which keeps the transport stateless and easy to reason about.

## Run

```
function run(t, command, timeout_s):
    SSH:   argv = ["ssh", "-i", key, "-p", port] + SSH_OPTIONS + [user + "@" + host, "sh -c " + quote(command)]
    local: argv = ["sh", "-c", command]
    start argv with stdin closed, capture stdout and stderr
    if not finished after timeout_s: kill it, return CommandResult(-1, captured so far)
    if SSH and exit status == 255 and stderr shows a connection or authentication failure:
        raise TransportError(host + ": " + first line of stderr)
    return CommandResult(exit status, stdout, stderr)

function home(t):
    if cached: return it
    r = run(t, 'printf %s "$HOME"', 30)
    require r.exit_status == 0 and r.stdout starts with "/"
    cache and return r.stdout
```

Every path that reaches the transport is absolute. Callers resolve a leading `~` with `home(t)` first.

## Background processes

```
function start_background(t, command, log_path):
    r = run(t, "set -m; nohup sh -c " + quote(command) + " >> " + quote(log_path) + " 2>&1 < /dev/null & echo $!", 30)
    require r.exit_status == 0 and r.stdout.strip() is a positive integer
    return that integer

function signal(t, pid, name):
    r = run(t, "kill -s " + name + " " + pid, 30)
    return ok if r.exit_status == 0 else gone

function alive(t, pid):
    return run(t, "kill -0 " + pid, 30).exit_status == 0
```

`nohup ... &` together with redirecting all three standard streams detaches the process from the SSH session, so it
survives the connection closing. `set -m` turns on job control first. Without it, a POSIX shell starts an
asynchronous command with SIGINT and SIGQUIT ignored, and an ignored signal cannot be re-enabled by a shell, so the
interrupt that stops the server ([../experiment_executor/design.md#serverlifecycle](../experiment_executor/design.md#serverlifecycle))
would never arrive. The returned pid belongs to the `sh -c` wrapper, so it must `exec` the program for
signals to reach it. Callers therefore always start commands as `exec <program> <args>`
([../experiment_executor/design.md#serverlifecycle](../experiment_executor/design.md#serverlifecycle)).

## Fetch

```
function fetch(t, remote_path, local_path):
    if run(t, "test -f " + quote(remote_path), 30).exit_status != 0: return missing
    create the parent directory of local_path
    SSH:   ["scp", "-q", "-i", key, "-P", port] + SSH_OPTIONS + [user + "@" + host + ":" + remote_path, local_path]
    local: copy the file
    on failure raise TransportError
    return ok
```

## Command quoting

```
function quote(word):
    if word is non-empty and every character is in [A-Za-z0-9_./:=@%+,-]: return word
    return "'" + word with every "'" replaced by "'\''" + "'"
```

Commands are built from words joined with single spaces, each word passed through `quote`. Fixed shell syntax
(`&&`, `&`, `;`, `$!`, redirections) is never quoted.

Worked examples:

| Word | Quoted |
|---|---|
| `--global_rate` | `--global_rate` |
| `cooperative` | `cooperative` |
| `/home/u/cancellation-bench` | `/home/u/cancellation-bench` |
| `a b` | `'a b'` |
| `it's` | `'it'\''s'` |
| (empty) | `''` |

## Design specific to Python

Shared tool `implementation/scripts/` (tooling language Python 3.11+; see `AGENTS.md` "Shared tooling"). Identical on
every `impl/<lang>` branch. Plotting needs `matplotlib` (`python3 -m pip install -r implementation/scripts/requirements.txt`).
Tests: `python3 -B -m unittest discover -s implementation/scripts/tests -t implementation/scripts`.

### Python choices

- One `subprocess.run` per operation (`ssh` / `scp` / `sh -c`), with stdin set to `DEVNULL`, output captured, and the given timeout. On timeout, `subprocess.run` kills the child, and the call returns exit status `-1`.
- `Transport` is a base class with `LocalTransport` and `SshTransport` subclasses. `run_argv` / `copy_argv` build the exact command lines, which the tests compare without a network.
- SSH connection failures are recognized from exit status 255 together with known `ssh` error messages, and raised as `TransportError`.

### Traceability

Generated from the `# Design:` tags in the code by `implementation/spec_tools/tracegen.py` (see `AGENTS.md`). Each link points at the first code line after the tag.
Regenerate from the repository root with `python3 implementation/spec_tools/tracegen.py --lang Python --impl implementation/scripts --ext .py --comment '#' --write`.

<!-- trace:begin -->
| Design subsection | Implementation (file:line) |
|---|---|
| [Transport](#transport) | [transport.py:12](../../../implementation/scripts/benchrunner/transport.py#L12), [transport.py:165](../../../implementation/scripts/benchrunner/transport.py#L165) |
| [Run](#run) | [transport.py:65](../../../implementation/scripts/benchrunner/transport.py#L65), [transport.py:81](../../../implementation/scripts/benchrunner/transport.py#L81), [test_transport.py:23](../../../implementation/scripts/tests/test_transport.py#L23), [test_transport.py:41](../../../implementation/scripts/tests/test_transport.py#L41) |
| [Background processes](#background-processes) | [transport.py:96](../../../implementation/scripts/benchrunner/transport.py#L96), [transport.py:105](../../../implementation/scripts/benchrunner/transport.py#L105), [transport.py:109](../../../implementation/scripts/benchrunner/transport.py#L109), [test_transport.py:49](../../../implementation/scripts/tests/test_transport.py#L49) |
| [Fetch](#fetch) | [transport.py:113](../../../implementation/scripts/benchrunner/transport.py#L113), [test_transport.py:65](../../../implementation/scripts/tests/test_transport.py#L65) |
| [Command quoting](#command-quoting) | [transport.py:24](../../../implementation/scripts/benchrunner/transport.py#L24), [transport.py:39](../../../implementation/scripts/benchrunner/transport.py#L39), [test_transport.py:13](../../../implementation/scripts/tests/test_transport.py#L13) |
<!-- trace:end -->
