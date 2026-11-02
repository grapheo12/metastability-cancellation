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
