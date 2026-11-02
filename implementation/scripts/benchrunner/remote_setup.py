# Tooling language: Python 3.11+ (shared tool implementation/scripts)
# Source: abstraction/benchmark_runner/remote_setup/design.md (remote_setup v0.2.0)
"""SourcePin, BuildManifest, and per-machine clone/checkout/tool check/build."""

import os
import re
import subprocess
import tomllib
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass

from .transport import quote

# API: abstraction/benchmark_runner/remote_setup/api.md#constants
MANIFEST_PATH = "implementation/{lang}/benchmark_manifest.toml"
BRANCH_PREFIX = "impl/"
PROGRAMS = ("server", "loadgen", "canceller")
MANIFEST_KEYS = {"lang", "required_tools", "build", "binaries"}
OPTIONAL_MANIFEST_KEYS = {"thread_env"}


_ENV_NAME = re.compile(r"[A-Za-z_][A-Za-z0-9_]*")


class PinError(Exception):
    """The operator repository cannot be pinned (wrong branch, dirty, not pushed)."""


class SetupError(Exception):
    """A machine could not be prepared."""


# Design: abstraction/benchmark_runner/remote_setup/design.md#buildmanifest
@dataclass
class BuildManifest:
    lang: str
    required_tools: list
    build: str
    binaries: dict
    thread_env: str | None = None


@dataclass
class SourcePin:
    repo_url: str
    branch: str
    lang: str
    commit: str
    manifest: BuildManifest


@dataclass
class MachineReady:
    transport: object
    role: str
    workdir: str
    repo_dir: str
    binaries: dict
    data_addr: str | None
    thread_env: str | None = None
    cpu_pinning: bool = False


def _git(repo_root, *args):
    p = subprocess.run(["git", "-C", repo_root, *args], capture_output=True, text=True)
    return p.returncode, p.stdout.strip(), p.stderr.strip()


# Design: abstraction/benchmark_runner/remote_setup/design.md#sourcepin
def source_pin(repo_url, repo_root):
    """Pin the operator's current impl/<lang> branch and HEAD, checked against repo_url."""
    code, branch, _ = _git(repo_root, "symbolic-ref", "--short", "-q", "HEAD")
    if code != 0 or not branch:
        raise PinError("HEAD is detached; check out an impl/<lang> branch")
    if not branch.startswith(BRANCH_PREFIX):
        raise PinError(f"on branch {branch}; check out an impl/<lang> branch")
    lang = branch[len(BRANCH_PREFIX):]
    _, dirty, _ = _git(repo_root, "status", "--porcelain", "--untracked-files=no")
    if dirty:
        raise PinError("tracked files are modified or staged; commit your changes first")
    _, commit, _ = _git(repo_root, "rev-parse", "HEAD")
    code, out, err = _git(repo_root, "ls-remote", repo_url, f"refs/heads/{branch}")
    if code != 0:
        raise PinError(f"cannot read {repo_url}: {err.splitlines()[-1] if err else 'git ls-remote failed'}")
    if not out:
        raise PinError(f"push {branch} to {repo_url} first")
    remote = out.split()[0]
    if remote != commit:
        raise PinError(f"{repo_url} has {remote} for {branch}, local HEAD is {commit}; push or pull first")
    manifest = read_manifest(os.path.join(repo_root, MANIFEST_PATH.format(lang=lang)), lang)
    return SourcePin(repo_url, branch, lang, commit, manifest)


# Design: abstraction/benchmark_runner/remote_setup/design.md#buildmanifest
def read_manifest(path, lang):
    try:
        with open(path, "rb") as f:
            doc = tomllib.load(f)
    except (OSError, tomllib.TOMLDecodeError) as e:
        raise PinError(f"cannot read BuildManifest {path}: {e}") from None
    if not MANIFEST_KEYS <= set(doc) or not set(doc) <= MANIFEST_KEYS | OPTIONAL_MANIFEST_KEYS:
        raise PinError(f"{path}: keys must be lang, required_tools, build, binaries and optionally thread_env")
    if doc["lang"] != lang:
        raise PinError(f"{path}: lang is {doc['lang']!r}, branch says {lang!r}")
    tools = doc["required_tools"]
    if not isinstance(tools, list) or not all(isinstance(t, str) and t and not any(c.isspace() for c in t)
                                              for t in tools):
        raise PinError(f"{path}: required_tools must be an array of command names")
    if not isinstance(doc["build"], str) or not doc["build"].strip():
        raise PinError(f"{path}: build must be a non-empty string")
    binaries = doc["binaries"]
    if not isinstance(binaries, dict) or set(binaries) != set(PROGRAMS):
        raise PinError(f"{path}: binaries must have exactly server, loadgen, canceller")
    for name, rel in binaries.items():
        if not isinstance(rel, str) or not rel or os.path.isabs(rel) or ".." in rel.split("/"):
            raise PinError(f"{path}: binaries.{name} must be a relative path without '..'")
    thread_env = doc.get("thread_env")
    if thread_env is not None and not (isinstance(thread_env, str) and _ENV_NAME.fullmatch(thread_env)):
        raise PinError(f"{path}: thread_env must be an environment variable name")
    return BuildManifest(lang, tools, doc["build"], binaries, thread_env)


def _tail(r, n=20):
    text = r.stderr.strip() or r.stdout.strip()
    return "\n".join(text.splitlines()[-n:])


# Design: abstraction/benchmark_runner/remote_setup/design.md#machine-setup
def setup_machine(t, role, machine, pin, timeout_s):
    def check(command, message, timeout=120.0):
        r = t.run(command, timeout)
        if r.exit_status != 0:
            why = "timed out" if r.exit_status == -1 else _tail(r)
            raise SetupError(f"{role}: {message}: {why}")
        return r

    workdir = t.resolve(machine.workdir)
    repo_dir = workdir + "/repo"
    url, branch = quote(pin.repo_url), quote(pin.branch)
    check("command -v git", "git is not installed")
    if t.run(f"test -d {quote(repo_dir + '/.git')}", 30).exit_status == 0:
        check(f"git -C {quote(repo_dir)} fetch --quiet {url} {branch}", "git fetch failed", timeout_s)
    else:
        check(f"mkdir -p {quote(workdir)} && git clone --quiet {url} {quote(repo_dir)}", "git clone failed", timeout_s)
        check(f"git -C {quote(repo_dir)} fetch --quiet {url} {branch}", "git fetch failed", timeout_s)
    check(f"git -C {quote(repo_dir)} checkout --quiet --force --detach {quote(pin.commit)}", "git checkout failed")
    head = check(f"git -C {quote(repo_dir)} rev-parse HEAD", "git rev-parse failed").stdout.strip()
    if head != pin.commit:
        raise SetupError(f"{role}: checked out {head}, expected {pin.commit}")
    for tool in pin.manifest.required_tools:
        check(f"command -v {quote(tool)}", f"{tool} is not installed (the runner does not install tools)")
    impl_dir = f"{repo_dir}/implementation/{pin.lang}"
    check(f"cd {quote(impl_dir)} && {pin.manifest.build}", "build failed", timeout_s)  # build is a shell command
    binaries = {}
    for program in PROGRAMS:
        path = f"{impl_dir}/{pin.manifest.binaries[program]}"
        check(f"test -x {quote(path)}", f"{program} binary missing after build")
        binaries[program] = path
    cpu_pinning = t.run("command -v taskset", 30).exit_status == 0  # absent is not an error
    return MachineReady(t, role, workdir, repo_dir, binaries, machine.data_addr if role == "server" else None,
                        pin.manifest.thread_env, cpu_pinning)


# Design: abstraction/benchmark_runner/remote_setup/design.md#machine-setup
def setup_machines(server_t, client_t, config, pin):
    """Both machines in parallel; once if they are the same host and workdir."""
    s, c = config.machines["server"], config.machines["client"]
    timeout = config.run.setup_timeout_s
    same = (s.host, s.user, s.ssh_port, s.workdir) == (c.host, c.user, c.ssh_port, c.workdir)
    if same:
        ready = setup_machine(server_t, "server", s, pin, timeout)
        return ready, MachineReady(client_t, "client", ready.workdir, ready.repo_dir, ready.binaries, None,
                                   ready.thread_env, ready.cpu_pinning)
    with ThreadPoolExecutor(max_workers=2) as pool:
        fs = pool.submit(setup_machine, server_t, "server", s, pin, timeout)
        fc = pool.submit(setup_machine, client_t, "client", c, pin, timeout)
        return fs.result(), fc.result()
