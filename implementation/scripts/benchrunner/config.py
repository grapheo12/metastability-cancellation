# Tooling language: Python 3.11+ (shared tool implementation/scripts)
# Source: abstraction/benchmark_runner/config/design.md (config v0.1.0)
"""BenchConfig loading and validation, SweepExpansion, argument formatting."""

import datetime
import itertools
import os
import subprocess
import tomllib
from dataclasses import dataclass

# Design: abstraction/benchmark_runner/config/design.md#benchconfig
PROGRAM_PARAMS = {
    "server": ["response_rate", "response_bytes", "cancel_mode", "cancel_check_interval_us", "seed"],
    "load_gen": ["num_clients", "global_rate", "request_bytes", "slo_ms", "duration_s", "seed"],
    "canceller": ["cancel_rate", "duration_s", "seed"],
}
RUNNER_KEYS = {"server": ["listen_port"], "load_gen": [], "canceller": ["enabled"]}
FORBIDDEN = {
    "server": ["listen_addr"],
    "load_gen": ["target", "output_path"],
    "canceller": ["target", "num_clients", "output_path"],
}
SECTIONS = ("server", "load_gen", "canceller")
TOP_LEVEL = {"repo", "machines", "server", "load_gen", "canceller", "run"}
REQUIRED_LOADGEN = ("num_clients", "duration_s", "slo_ms")


class ConfigError(Exception):
    """A one-line problem with the config file."""


@dataclass
class Machine:
    role: str
    host: str
    user: str | None
    ssh_key: str | None
    ssh_port: int
    workdir: str
    data_addr: str | None

    @property
    def local(self):
        return self.host == "local"


@dataclass
class RunOptions:
    output_dir: str
    sweep_order: list | None
    cooldown_s: float
    on_failure: str
    server_ready_timeout_s: float
    stop_timeout_s: float
    client_timeout_margin_s: float
    setup_timeout_s: float


@dataclass
class BenchConfig:
    repo_url: str
    machines: dict  # role -> Machine
    params: dict  # section -> {program parameter: scalar or list}, file order kept
    listen_port: int
    canceller_enabled: bool
    run: RunOptions

    def swept_keys(self):
        if self.run.sweep_order is not None:
            return list(self.run.sweep_order)
        return [f"{s}.{k}" for s in SECTIONS for k, v in self.params[s].items() if isinstance(v, list)]

    def lookup(self, key):
        section, name = key.split(".", 1)
        return self.params[section][name]


def _is_scalar(v):
    return isinstance(v, (str, int, float, bool))


def _is_int(v):
    return isinstance(v, int) and not isinstance(v, bool)


def _is_number(v):
    return isinstance(v, (int, float)) and not isinstance(v, bool)


def _origin_url():
    r = subprocess.run(["git", "remote", "get-url", "origin"], capture_output=True, text=True)
    if r.returncode != 0:
        return None
    return r.stdout.strip() or None


def _check_keys(table, allowed, where):
    if not isinstance(table, dict):
        raise ConfigError(f"[{where}] must be a table")
    for k in table:
        if k not in allowed:
            raise ConfigError(f"unknown key {where}.{k}")


def _machine(role, t):
    _check_keys(t, {"host", "user", "ssh_key", "ssh_port", "workdir"} | ({"data_addr"} if role == "server" else set()),
                f"machines.{role}")
    host = t.get("host")
    if not isinstance(host, str) or not host:
        raise ConfigError(f"machines.{role}.host is required")
    local = host == "local"
    user, key = t.get("user"), t.get("ssh_key")
    if not local and not (isinstance(user, str) and user):
        raise ConfigError(f"machines.{role}.user is required")
    if not local and not (isinstance(key, str) and key):
        raise ConfigError(f"machines.{role}.ssh_key is required")
    port = t.get("ssh_port", 22)
    if not _is_int(port) or not 1 <= port <= 65535:
        raise ConfigError(f"machines.{role}.ssh_port must be an integer in 1..=65535")
    workdir = t.get("workdir", "~/cancellation-bench")
    if not isinstance(workdir, str) or not workdir:
        raise ConfigError(f"machines.{role}.workdir must be a non-empty string")
    data_addr = None
    if role == "server":
        data_addr = t.get("data_addr", "127.0.0.1" if local else host)
        if not isinstance(data_addr, str) or not data_addr:
            raise ConfigError("machines.server.data_addr must be a non-empty string")
    return Machine(role, host, user, os.path.expanduser(key) if key else None, port, workdir, data_addr)


def _positive(run, key, default, allow_zero=False):
    v = run.get(key, default)
    if not _is_number(v) or v < 0 or (v == 0 and not allow_zero):
        raise ConfigError(f"run.{key} must be a number {'>= 0' if allow_zero else '> 0'}")
    return float(v)


# Design: abstraction/benchmark_runner/config/design.md#benchconfig
def load_config(path, runner_dir, now=None):
    """Read and validate the TOML file at path. runner_dir is the default results base."""
    try:
        with open(path, "rb") as f:
            doc = tomllib.load(f)
    except (OSError, tomllib.TOMLDecodeError) as e:
        raise ConfigError(f"cannot read {path}: {e}") from None
    for k in doc:
        if k not in TOP_LEVEL:
            raise ConfigError(f"unknown table [{k}]")

    repo = doc.get("repo", {})
    _check_keys(repo, {"url"}, "repo")
    repo_url = repo.get("url") or _origin_url()
    if not isinstance(repo_url, str) or not repo_url:
        raise ConfigError("[repo] url is required (no origin remote)")

    machines = doc.get("machines")
    if not isinstance(machines, dict) or set(machines) != {"server", "client"}:
        raise ConfigError("[machines.server] and [machines.client] are both required, and nothing else")
    machines = {role: _machine(role, machines[role]) for role in ("server", "client")}

    params, listen_port, enabled = {}, 8080, True
    for section in SECTIONS:
        table = doc.get(section, {})
        if not isinstance(table, dict):
            raise ConfigError(f"[{section}] must be a table")
        params[section] = {}
        for key, value in table.items():
            name = f"{section}.{key}"
            if key in FORBIDDEN[section]:
                raise ConfigError(f"{name} is set by the runner")
            if key in RUNNER_KEYS[section]:
                if isinstance(value, list):
                    raise ConfigError(f"{name} cannot be swept")
                if key == "listen_port":
                    if not _is_int(value) or not 1 <= value <= 65535:
                        raise ConfigError(f"{name} must be an integer in 1..=65535")
                    listen_port = value
                elif key == "enabled":
                    if not isinstance(value, bool):
                        raise ConfigError(f"{name} must be true or false")
                    enabled = value
            elif key in PROGRAM_PARAMS[section]:
                if isinstance(value, list):
                    if not value or not all(_is_scalar(v) for v in value):
                        raise ConfigError(f"{name} must be a scalar or a non-empty array of scalars")
                elif not _is_scalar(value):
                    raise ConfigError(f"{name} must be a scalar or a non-empty array of scalars")
                params[section][key] = value
            else:
                raise ConfigError(f"unknown key {name}")
    for key in REQUIRED_LOADGEN:  # the runner itself needs these
        if key not in params["load_gen"]:
            raise ConfigError(f"load_gen.{key} is required")

    run = doc.get("run", {})
    _check_keys(run, {"output_dir", "sweep_order", "cooldown_s", "on_failure", "server_ready_timeout_s",
                      "stop_timeout_s", "client_timeout_margin_s", "setup_timeout_s"}, "run")
    now = now or datetime.datetime.now(datetime.timezone.utc)
    output_dir = run.get("output_dir", os.path.join(runner_dir, "results", now.strftime("%Y%m%dT%H%M%SZ")))
    if not isinstance(output_dir, str) or not output_dir:
        raise ConfigError("run.output_dir must be a non-empty string")
    on_failure = run.get("on_failure", "continue")
    if on_failure not in ("continue", "stop"):
        raise ConfigError('run.on_failure must be "continue" or "stop"')
    sweep_order = run.get("sweep_order")
    if sweep_order is not None and not (isinstance(sweep_order, list) and all(isinstance(k, str) for k in sweep_order)):
        raise ConfigError("run.sweep_order must be an array of strings")
    options = RunOptions(
        output_dir=os.path.abspath(os.path.expanduser(output_dir)),
        sweep_order=sweep_order,
        cooldown_s=_positive(run, "cooldown_s", 2.0, allow_zero=True),
        on_failure=on_failure,
        server_ready_timeout_s=_positive(run, "server_ready_timeout_s", 30.0),
        stop_timeout_s=_positive(run, "stop_timeout_s", 30.0),
        client_timeout_margin_s=_positive(run, "client_timeout_margin_s", 60.0),
        setup_timeout_s=_positive(run, "setup_timeout_s", 900.0),
    )
    config = BenchConfig(repo_url, machines, params, listen_port, enabled, options)
    if sweep_order is not None:
        file_order = [f"{s}.{k}" for s in SECTIONS for k, v in params[s].items() if isinstance(v, list)]
        if sorted(sweep_order) != sorted(file_order) or len(set(sweep_order)) != len(sweep_order):
            raise ConfigError("run.sweep_order must list every swept key exactly once")
    return config


@dataclass
class SweepPoint:
    id: int
    swept: dict
    server_args: list
    loadgen_args: list
    canceller_args: list | None

    def to_json(self):
        return {
            "id": self.id,
            "swept": self.swept,
            "server_args": [[k, v] for k, v in self.server_args],
            "loadgen_args": [[k, v] for k, v in self.loadgen_args],
            "canceller_args": None if self.canceller_args is None else [[k, v] for k, v in self.canceller_args],
        }

    def arg(self, program, name):
        args = {"server": self.server_args, "loadgen": self.loadgen_args, "canceller": self.canceller_args or []}
        return dict(args[program]).get(name)


# Design: abstraction/benchmark_runner/config/design.md#sweepexpansion
def expand_sweep(config):
    """Cross product of the swept keys; the last key varies fastest."""
    keys = config.swept_keys()
    points = []
    for pid, combo in enumerate(itertools.product(*[config.lookup(k) for k in keys])):
        choose = dict(zip(keys, combo))

        def args(section):
            return [(k, choose.get(f"{section}.{k}", v)) for k, v in config.params[section].items()]

        server_args, loadgen_args = args("server"), args("load_gen")
        canceller_args = None
        if config.canceller_enabled:
            canceller_args = args("canceller")
            canceller_args.append(("num_clients", dict(loadgen_args)["num_clients"]))
            if "duration_s" not in dict(canceller_args):
                canceller_args.append(("duration_s", dict(loadgen_args)["duration_s"]))
        points.append(SweepPoint(pid, choose, server_args, loadgen_args, canceller_args))
    return points


# Design: abstraction/benchmark_runner/config/design.md#argument-formatting
def format_arg(value):
    """How a scalar is written on a command line."""
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, int):
        return str(value)
    if isinstance(value, float):
        return repr(value)  # shortest string that round-trips
    return value


def arg_words(pairs):
    """[(name, value)] -> ["--name", "value", ...]."""
    words = []
    for name, value in pairs:
        words += [f"--{name}", format_arg(value)]
    return words
