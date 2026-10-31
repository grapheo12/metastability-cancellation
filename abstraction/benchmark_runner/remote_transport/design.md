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
