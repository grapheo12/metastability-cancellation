# Tooling language: Python 3.11+ (shared tool implementation/scripts)
# Source: abstraction/benchmark_runner/remote_transport/design.md (remote_transport v0.2.0)
"""Transport: run commands on, and copy files from, a machine over SSH or locally."""

import os
import re
import shutil
import subprocess
from dataclasses import dataclass

# Design: abstraction/benchmark_runner/remote_transport/design.md#transport
SSH_OPTIONS = [
    "-o", "BatchMode=yes",
    "-o", "StrictHostKeyChecking=accept-new",
    "-o", "ConnectTimeout=10",
    "-o", "ServerAliveInterval=15",
]
SSH_FAILURE_MARKERS = (
    "Connection refused", "Connection timed out", "Could not resolve", "Permission denied",
    "Host key verification failed", "No route to host", "Connection closed", "Operation timed out",
)

# Design: abstraction/benchmark_runner/remote_transport/design.md#command-quoting
SAFE_WORD = re.compile(r"[A-Za-z0-9_./:=@%+,-]+")


class TransportError(Exception):
    """The transport itself failed (connect, authenticate, copy)."""


@dataclass
class CommandResult:
    exit_status: int
    stdout: str
    stderr: str


# Design: abstraction/benchmark_runner/remote_transport/design.md#command-quoting
def quote(word):
    """POSIX shell quoting of one word."""
    if word and SAFE_WORD.fullmatch(word):
        return word
    return "'" + word.replace("'", "'\\''") + "'"


def join(words):
    return " ".join(quote(w) for w in words)


def _decode(b):
    if b is None:
        return ""
    return b.decode("utf-8", "replace") if isinstance(b, bytes) else b


class Transport:
    """Common operations; subclasses provide run_argv and copy_argv/copy."""

    name = "machine"

    def __init__(self):
        self._home = None

    # Design: abstraction/benchmark_runner/remote_transport/design.md#run
    def run(self, command, timeout_s=120.0):
        argv = self.run_argv(command)
        try:
            p = subprocess.run(argv, stdin=subprocess.DEVNULL, capture_output=True, timeout=timeout_s)
        except subprocess.TimeoutExpired as e:  # subprocess.run kills the child
            return CommandResult(-1, _decode(e.stdout), _decode(e.stderr))
        except OSError as e:
            raise TransportError(f"{self.name}: cannot start {argv[0]}: {e}") from None
        result = CommandResult(p.returncode, _decode(p.stdout), _decode(p.stderr))
        self.check_connection(result)
        return result

    def check_connection(self, result):
        pass

    # Design: abstraction/benchmark_runner/remote_transport/design.md#run
    def home(self):
        if self._home is None:
            r = self.run('printf %s "$HOME"', 30)
            if r.exit_status != 0 or not r.stdout.startswith("/"):
                raise TransportError(f"{self.name}: cannot determine the home directory")
            self._home = r.stdout
        return self._home

    def resolve(self, path):
        """Resolve a leading ~ against this machine's home."""
        if path == "~" or path.startswith("~/"):
            return self.home() + path[1:]
        return path

    # Design: abstraction/benchmark_runner/remote_transport/design.md#background-processes
    def start_background(self, command, log_path):
        # set -m: without job control, POSIX shells start & commands with SIGINT ignored
        r = self.run(f"set -m; nohup sh -c {quote(command)} >> {quote(log_path)} 2>&1 < /dev/null & echo $!", 30)
        pid = r.stdout.strip()
        if r.exit_status != 0 or not pid.isdigit() or int(pid) <= 0:
            raise TransportError(f"{self.name}: cannot start background command: {r.stderr.strip()}")
        return int(pid)

    # Design: abstraction/benchmark_runner/remote_transport/design.md#background-processes
    def signal(self, pid, name):
        return self.run(f"kill -s {name} {int(pid)}", 30).exit_status == 0

    # Design: abstraction/benchmark_runner/remote_transport/design.md#background-processes
    def alive(self, pid):
        return self.run(f"kill -0 {int(pid)}", 30).exit_status == 0

    # Design: abstraction/benchmark_runner/remote_transport/design.md#fetch
    def fetch(self, remote_path, local_path):
        """Copy one file to the operator machine. Returns False if it does not exist."""
        if self.run(f"test -f {quote(remote_path)}", 30).exit_status != 0:
            return False
        os.makedirs(os.path.dirname(os.path.abspath(local_path)), exist_ok=True)
        self.copy(remote_path, local_path)
        return True


class LocalTransport(Transport):
    """host = "local": every operation runs on the operator machine."""

    name = "local"

    def run_argv(self, command):
        return ["sh", "-c", command]

    def copy(self, remote_path, local_path):
        try:
            shutil.copyfile(remote_path, local_path)
        except OSError as e:
            raise TransportError(f"local: copy {remote_path}: {e}") from None


class SshTransport(Transport):
    """System ssh/scp, one child process per operation, never prompting.

    user, key and port may be None: ssh/scp then take them from ~/.ssh/config."""

    def __init__(self, host, user=None, key=None, port=None):
        super().__init__()
        self.host, self.user, self.key, self.port = host, user, key, port
        self.name = self.destination()

    # Design: abstraction/benchmark_runner/remote_transport/design.md#sshconfighost
    def destination(self):
        return f"{self.user}@{self.host}" if self.user else self.host

    # Design: abstraction/benchmark_runner/remote_transport/design.md#sshconfighost
    def target_words(self, port_flag):
        words = []
        if self.key:
            words += ["-i", self.key]
        if self.port is not None:
            words += [port_flag, str(self.port)]
        return words + SSH_OPTIONS

    # Design: abstraction/benchmark_runner/remote_transport/design.md#run
    def run_argv(self, command):
        return ["ssh", *self.target_words("-p"), self.destination(), "sh -c " + quote(command)]

    # Design: abstraction/benchmark_runner/remote_transport/design.md#fetch
    def copy_argv(self, remote_path, local_path):
        return ["scp", "-q", *self.target_words("-P"), f"{self.destination()}:{remote_path}", local_path]

    def check_connection(self, result):
        if result.exit_status == 255 and any(m in result.stderr for m in SSH_FAILURE_MARKERS):
            first = result.stderr.strip().splitlines()[0] if result.stderr.strip() else "ssh failed"
            raise TransportError(f"{self.destination()}: {first}")

    def copy(self, remote_path, local_path):
        p = subprocess.run(self.copy_argv(remote_path, local_path), stdin=subprocess.DEVNULL, capture_output=True)
        if p.returncode != 0:
            raise TransportError(f"{self.destination()}: scp {remote_path}: {_decode(p.stderr).strip()}")


# Design: abstraction/benchmark_runner/remote_transport/design.md#sshconfighost
def parse_ssh_g(stdout, host):
    """The value of the `hostname` line of `ssh -G` output, else host."""
    for line in stdout.splitlines():
        if line.startswith("hostname "):
            value = line[len("hostname "):].strip()
            if value:
                return value
    return host


# Design: abstraction/benchmark_runner/remote_transport/design.md#sshconfighost
def ssh_hostname(host):
    """HostName of host in the operator's SSH configuration (ssh -G); host on any failure."""
    try:
        p = subprocess.run(["ssh", "-G", host], stdin=subprocess.DEVNULL, capture_output=True, timeout=10)
    except (OSError, subprocess.TimeoutExpired):
        return host
    return parse_ssh_g(_decode(p.stdout), host) if p.returncode == 0 else host


# Design: abstraction/benchmark_runner/remote_transport/design.md#transport
def open_transport(machine):
    """Pick the variant from machine.host; nothing connects yet."""
    if machine.local:
        return LocalTransport()
    return SshTransport(machine.host, machine.user, machine.ssh_key, machine.ssh_port)
