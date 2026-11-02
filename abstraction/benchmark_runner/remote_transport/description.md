# Remote Transport — Description

Leaf component. A **Transport** is the runner's only way to act on an experiment machine. It can:

- run a POSIX shell command there and collect its exit status and output;
- start a long-running command in the background, returning its process id, and later signal it or check whether
  it is still alive;
- copy a file from the machine to the operator machine.

There are two variants with the same interface. The **SSH** variant uses the system SSH client and never prompts.
Any of user, key and port left out of the BenchConfig comes from the operator's SSH configuration
(**SshConfigHost**), so a machine can be named by a `~/.ssh/config` alias alone. The **local** variant (`host = "local"`) does the same things on the operator
machine, so the whole runner can be tested without remote hosts.

Parent: [../design.md](../design.md). API: [api.md](api.md). Design: [design.md](design.md).
