# Remote Transport — Design

## Transport

```
function open(machine):
    if machine.host == "local": return LocalTransport()
    key = expand_operator_home(ssh_key) if ssh_key is set else none
    return SshTransport(host, user or none, key, ssh_port or none)

SSH_OPTIONS = ["-o", "BatchMode=yes",                      # never prompt
               "-o", "StrictHostKeyChecking=accept-new",   # trust a new host once, reject changed keys
               "-o", "ConnectTimeout=10",
               "-o", "ServerAliveInterval=15"]
```

The SSH variant runs the operator's system `ssh` and `scp` programs as child processes, one per operation.
It holds no persistent connection, which keeps the transport stateless and easy to reason about.

## SshConfigHost

`user`, `ssh_key` and `ssh_port` are each optional. The transport passes only the ones that are set, so anything
left unset comes from the operator's SSH configuration (`~/.ssh/config`), which the system `ssh` and `scp` always
read. `host` can therefore be a `Host` alias whose `HostName`, `User`, `IdentityFile` and `Port` live in that file.
A value set in the BenchConfig wins over the SSH configuration, because a command-line option overrides it.

```
function ssh_target_words(t):                      # options shared by ssh and scp, before the target
    words = []
    if t.key  is not none: words += ["-i", t.key]
    if t.port is not none: words += [PORT_FLAG, decimal(t.port)]   # "-p" for ssh, "-P" for scp
    return words + SSH_OPTIONS

function destination(t):
    return (t.user + "@" if t.user is not none else "") + t.host

function ssh_hostname(host):                       # on the operator machine
    r = run argv ["ssh", "-G", host] locally, timeout 10 s
    if r.exit_status == 0:
        for line in r.stdout lines:
            if line starts with "hostname ": return the rest of the line, trimmed
    return host                                    # fallback: the name as written
```

`ssh -G` prints the configuration `ssh` would use for `host`, after applying `~/.ssh/config`, without connecting.
The config uses `ssh_hostname` for the default `data_addr`, because an alias that only the operator's SSH
configuration knows cannot be resolved by the client machine.

| `user` | `ssh_key` | `ssh_port` | `ssh` argv before the command |
|---|---|---|---|
| unset | unset | unset | `ssh <SSH_OPTIONS> metafail-exp-1` |
| `ubuntu` | `/k/id` | `2222` | `ssh -i /k/id -p 2222 <SSH_OPTIONS> ubuntu@10.0.0.5` |
| `ubuntu` | unset | unset | `ssh <SSH_OPTIONS> ubuntu@metafail-exp-1` |

| `ssh -G metafail-exp-1` stdout contains | `ssh_hostname` |
|---|---|
| `hostname 18.144.18.190` | `18.144.18.190` |
| no `hostname` line, or `ssh -G` fails | `metafail-exp-1` |

Error messages name the machine by `destination(t)`, as written in the config.

## Run

```
function run(t, command, timeout_s):
    SSH:   argv = ["ssh"] + ssh_target_words(t) + [destination(t), "sh -c " + quote(command)]     # PORT_FLAG "-p"
    local: argv = ["sh", "-c", command]
    start argv with stdin closed, capture stdout and stderr
    if not finished after timeout_s: kill it, return CommandResult(-1, captured so far)
    if SSH and exit status == 255 and stderr shows a connection or authentication failure:
        raise TransportError(destination(t) + ": " + first line of stderr)
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
    SSH:   ["scp", "-q"] + ssh_target_words(t) + [destination(t) + ":" + remote_path, local_path]   # PORT_FLAG "-P"
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
- `SshTransport(host, user=None, key=None, port=None)`. `ssh_target_words` is the method `target_words(port_flag)` and `destination(t)` is `destination()`. Unset values are `None` and are simply left out of argv. The parsing of `ssh -G` output is the pure function `parse_ssh_g`, so it can be tested without `ssh`. `ssh_hostname` returns `host` on a non-zero exit, `OSError` or a 10 s timeout.

### Traceability

Generated from the `# Design:` tags in the code by `implementation/spec_tools/tracegen.py` (see `AGENTS.md`). Each link points at the first code line after the tag.
Regenerate from the repository root with `python3 implementation/spec_tools/tracegen.py --lang Python --impl implementation/scripts --ext .py --comment '#' --write`.

<!-- trace:begin -->
| Design subsection | Implementation (file:line) |
|---|---|
| [Transport](#transport) | [transport.py:12](../../../implementation/scripts/benchrunner/transport.py#L12), [transport.py:201](../../../implementation/scripts/benchrunner/transport.py#L201) |
| [SshConfigHost](#sshconfighost) | [transport.py:148](../../../implementation/scripts/benchrunner/transport.py#L148), [transport.py:152](../../../implementation/scripts/benchrunner/transport.py#L152), [transport.py:180](../../../implementation/scripts/benchrunner/transport.py#L180), [transport.py:191](../../../implementation/scripts/benchrunner/transport.py#L191), [test_transport.py:32](../../../implementation/scripts/tests/test_transport.py#L32), [test_transport.py:41](../../../implementation/scripts/tests/test_transport.py#L41) |
| [Run](#run) | [transport.py:65](../../../implementation/scripts/benchrunner/transport.py#L65), [transport.py:81](../../../implementation/scripts/benchrunner/transport.py#L81), [transport.py:161](../../../implementation/scripts/benchrunner/transport.py#L161), [test_transport.py:23](../../../implementation/scripts/tests/test_transport.py#L23), [test_transport.py:57](../../../implementation/scripts/tests/test_transport.py#L57) |
| [Background processes](#background-processes) | [transport.py:96](../../../implementation/scripts/benchrunner/transport.py#L96), [transport.py:105](../../../implementation/scripts/benchrunner/transport.py#L105), [transport.py:109](../../../implementation/scripts/benchrunner/transport.py#L109), [test_transport.py:65](../../../implementation/scripts/tests/test_transport.py#L65) |
| [Fetch](#fetch) | [transport.py:113](../../../implementation/scripts/benchrunner/transport.py#L113), [transport.py:165](../../../implementation/scripts/benchrunner/transport.py#L165), [test_transport.py:81](../../../implementation/scripts/tests/test_transport.py#L81) |
| [Command quoting](#command-quoting) | [transport.py:24](../../../implementation/scripts/benchrunner/transport.py#L24), [transport.py:39](../../../implementation/scripts/benchrunner/transport.py#L39), [test_transport.py:13](../../../implementation/scripts/tests/test_transport.py#L13) |
<!-- trace:end -->
