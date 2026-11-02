# Version — benchmark_runner/remote_transport

| Version | Last modified (UTC) | Summary |
|---|---|---|
| 0.1.0 | 2026-10-31T00:00:00Z | Initial spec: SSH and local transports, run, background processes, fetch, command quoting |
| 0.1.1 | 2026-10-31T00:00:00Z | start_background enables job control (`set -m`) so the started program receives SIGINT |
| 0.2.0 | 2026-11-02T00:00:00Z | SshConfigHost: `user`, `ssh_key`, `ssh_port` optional, so unset ones come from `~/.ssh/config` (host may be an alias). New `ssh_hostname` (`ssh -G`) |
