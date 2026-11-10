# Tooling language: Python 3.11+ (shared tool implementation/scripts)
# Source: abstraction/benchmark_runner/config/design.md (config v0.6.0)
"""BenchConfig loading and validation, SweepExpansion, argument formatting."""

import datetime
import itertools
import os
import re
import subprocess
import tomllib
from dataclasses import dataclass

from . import transport

# Design: abstraction/benchmark_runner/config/design.md#benchconfig
PROGRAM_PARAMS = {
    "server": ["response_rate", "response_bytes", "cancel_mode", "cancel_check_interval_us", "seed",
               "n_worker_threads", "queue_sample_interval_ms"],
    "load_gen": ["num_clients", "global_rate", "request_bytes", "slo_ms", "duration_s", "seed"],
    "ingress": ["lb_policy", "cancel_rate", "duration_s", "seed"],
}
# Design: abstraction/benchmark_runner/config/design.md#processplacement
PLACEMENT_KEYS = ["threads", "cpu_set"]
RUNNER_KEYS = {
    "server": ["listen_port", "instances"] + PLACEMENT_KEYS,
    "load_gen": PLACEMENT_KEYS,
    "ingress": ["machine", "listen_port", "cancel_rate_fraction"] + PLACEMENT_KEYS,
}
# Design: abstraction/benchmark_runner/config/design.md#cancelratefraction
SWEEPABLE_RUNNER_KEYS = ["ingress.cancel_rate_fraction"]
CPU_LIST = re.compile(r"[0-9]+(-[0-9]+)?(,[0-9]+(-[0-9]+)?)*")
PROGRAM_OF = {"server": "server", "load_gen": "loadgen", "ingress": "ingress"}
MAX_INSTANCES = 64
FORBIDDEN = {
    "server": ["listen_addr", "queue_log_path"],
    "load_gen": ["target", "output_path"],
    "ingress": ["listen_addr", "backends", "num_clients", "output_path"],
}
SECTIONS = ("server", "load_gen", "ingress")
TOP_LEVEL = {"repo", "machines", "server", "load_gen", "ingress", "run"}
REQUIRED_LOADGEN = ("num_clients", "duration_s", "slo_ms")


class ConfigError(Exception):
    """A one-line problem with the config file."""


@dataclass
class Machine:
    role: str
    host: str
    user: str | None  # None: from the operator's ~/.ssh/config
    ssh_key: str | None
    ssh_port: int | None
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
    listen_port: int  # server instance k listens on listen_port + k
    instances: int
    ingress_port: int
    ingress_machine: str  # IngressPlacement: "client" | "server"
    run: RunOptions
    placement: dict  # section -> {"threads": int | None, "cpu_set": str | None}
    cancel_rate_fraction: object = None  # number, list of numbers, or None (CancelRateFraction)
    file_swept: list = None  # swept keys in file order, program parameters and sweepable runner keys

    def swept_keys(self):
        if self.run.sweep_order is not None:
            return list(self.run.sweep_order)
        return list(self.file_swept or [])

    def lookup(self, key):
        if key == "ingress.cancel_rate_fraction":
            return self.cancel_rate_fraction
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


# Design: abstraction/benchmark_runner/config/design.md#benchconfig
def _machine(role, t):
    """validate_machine: unset SSH keys stay None and come from the operator's ~/.ssh/config."""
    _check_keys(t, {"host", "user", "ssh_key", "ssh_port", "workdir"} | ({"data_addr"} if role == "server" else set()),
                f"machines.{role}")
    host = t.get("host")
    if not isinstance(host, str) or not host:
        raise ConfigError(f"machines.{role}.host is required")
    local = host == "local"
    for key in ("user", "ssh_key"):
        if key in t and not (isinstance(t[key], str) and t[key]):
            raise ConfigError(f"machines.{role}.{key} must be a non-empty string")
    user, key = t.get("user"), t.get("ssh_key")
    port = t.get("ssh_port")
    if port is not None and (not _is_int(port) or not 1 <= port <= 65535):
        raise ConfigError(f"machines.{role}.ssh_port must be an integer in 1..=65535")
    workdir = t.get("workdir", "~/cancellation-bench")
    if not isinstance(workdir, str) or not workdir:
        raise ConfigError(f"machines.{role}.workdir must be a non-empty string")
    data_addr = None
    if role == "server":
        if "data_addr" in t:
            data_addr = t["data_addr"]
        else:  # an alias only the operator's SSH config knows is resolved to its HostName
            data_addr = "127.0.0.1" if local else transport.ssh_hostname(host)
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

    params, fraction, file_swept = {}, None, []
    ports = {"server": 8080, "ingress": 9090}
    instances = 1
    ingress_machine = "client"
    placement = {section: {"threads": None, "cpu_set": None} for section in SECTIONS}
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
                if isinstance(value, list) and name not in SWEEPABLE_RUNNER_KEYS:
                    raise ConfigError(f"{name} cannot be swept")
                if key == "listen_port":
                    if not _is_int(value) or not 1 <= value <= 65535:
                        raise ConfigError(f"{name} must be an integer in 1..=65535")
                    ports[section] = value
                # Design: abstraction/benchmark_runner/config/design.md#ingressplacement
                elif key == "machine":
                    if value not in ("client", "server"):
                        raise ConfigError(f'{name} must be "client" or "server"')
                    ingress_machine = value
                elif key == "instances":
                    if not _is_int(value) or not 1 <= value <= MAX_INSTANCES:
                        raise ConfigError(f"{name} must be an integer in 1..=64")
                    instances = value
                # Design: abstraction/benchmark_runner/config/design.md#processplacement
                elif key == "threads":
                    if not _is_int(value) or value < 1:
                        raise ConfigError(f"{name} must be an integer >= 1")
                    placement[section]["threads"] = value
                elif key == "cpu_set":
                    if not isinstance(value, str) or not CPU_LIST.fullmatch(value):
                        raise ConfigError(f'{name} must be a CPU list like "0-3" or "0,2,4-5"')
                    placement[section]["cpu_set"] = value
                # Design: abstraction/benchmark_runner/config/design.md#cancelratefraction
                elif key == "cancel_rate_fraction":
                    if isinstance(value, list) and not value:
                        raise ConfigError(f"{name} must be a scalar or a non-empty array of scalars")
                    if not all(_is_number(v) and v > 0 for v in (value if isinstance(value, list) else [value])):
                        raise ConfigError(f"{name} must be a number > 0")
                    fraction = value
                    if isinstance(value, list):
                        file_swept.append(name)
            elif key in PROGRAM_PARAMS[section]:
                if isinstance(value, list):
                    if not value or not all(_is_scalar(v) for v in value):
                        raise ConfigError(f"{name} must be a scalar or a non-empty array of scalars")
                    file_swept.append(name)
                elif not _is_scalar(value):
                    raise ConfigError(f"{name} must be a scalar or a non-empty array of scalars")
                params[section][key] = value
            else:
                raise ConfigError(f"unknown key {name}")
    for key in REQUIRED_LOADGEN:  # the runner itself needs these
        if key not in params["load_gen"]:
            raise ConfigError(f"load_gen.{key} is required")
    check_cancel_rate_fraction(params, fraction)
    if ports["server"] + instances - 1 > 65535:
        raise ConfigError("server.listen_port + server.instances - 1 must be <= 65535")
    check_ingress_placement(ingress_machine, ports, instances)

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
    config = BenchConfig(repo_url, machines, params, ports["server"], instances, ports["ingress"], ingress_machine,
                         options, placement, fraction, file_swept)
    if sweep_order is not None:
        if sorted(sweep_order) != sorted(file_swept) or len(set(sweep_order)) != len(sweep_order):
            raise ConfigError("run.sweep_order must list every swept key exactly once")
    return config


# Design: abstraction/benchmark_runner/config/design.md#cancelratefraction
def check_cancel_rate_fraction(params, fraction):
    if fraction is None:
        return
    if "cancel_rate" in params["ingress"]:
        raise ConfigError("set ingress.cancel_rate or ingress.cancel_rate_fraction, not both")
    if "global_rate" not in params["load_gen"]:
        raise ConfigError("load_gen.global_rate is required with ingress.cancel_rate_fraction")
    rate = params["load_gen"]["global_rate"]
    if not all(_is_number(v) for v in (rate if isinstance(rate, list) else [rate])):
        raise ConfigError("load_gen.global_rate must be numeric with ingress.cancel_rate_fraction")


# Design: abstraction/benchmark_runner/config/design.md#backends
def backend_list(config):
    """host:port of every server instance as the Ingress sees it, in server_id order."""
    host = "127.0.0.1" if config.ingress_machine == "server" else config.machines["server"].data_addr
    return ",".join(f"{host}:{config.listen_port + k}" for k in range(config.instances))


# Design: abstraction/benchmark_runner/config/design.md#ingressplacement
def loadgen_target(config):
    """The Ingress as seen from the client machine."""
    host = "127.0.0.1" if config.ingress_machine == "client" else config.machines["server"].data_addr
    return f"{host}:{config.ingress_port}"


# Design: abstraction/benchmark_runner/config/design.md#ingressplacement
def check_ingress_placement(ingress_machine, ports, instances):
    if ingress_machine == "server" and ports["server"] <= ports["ingress"] <= ports["server"] + instances - 1:
        raise ConfigError("ingress.listen_port collides with a server instance port")


# Design: abstraction/benchmark_runner/config/design.md#cancelratefraction
def derived_cancel_rate(global_rate, fraction):
    return float(global_rate) * float(fraction)  # IEEE 754 double, round to nearest even


@dataclass
class SweepPoint:
    id: int
    swept: dict
    server_args: list
    loadgen_args: list
    ingress_args: list
    server_instances: int = 1
    ingress_machine: str = "client"
    loadgen_target: str = ""
    placement: dict = None  # program -> {"threads", "cpu_set"} (ProcessPlacement)

    def to_json(self):
        return {
            "id": self.id,
            "swept": self.swept,
            "server_args": [[k, v] for k, v in self.server_args],
            "loadgen_args": [[k, v] for k, v in self.loadgen_args],
            "ingress_args": [[k, v] for k, v in self.ingress_args],
            "server_instances": self.server_instances,
            "ingress_machine": self.ingress_machine,
            "loadgen_target": self.loadgen_target,
            "placement": self.placement,
        }

    def arg(self, program, name):
        args = {"server": self.server_args, "loadgen": self.loadgen_args, "ingress": self.ingress_args}
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
        ingress_args = args("ingress")
        ingress_args.append(("backends", backend_list(config)))
        ingress_args.append(("num_clients", dict(loadgen_args)["num_clients"]))
        if "duration_s" not in dict(ingress_args):
            ingress_args.append(("duration_s", dict(loadgen_args)["duration_s"]))
        # Design: abstraction/benchmark_runner/config/design.md#cancelratefraction
        if config.cancel_rate_fraction is not None:
            f = choose.get("ingress.cancel_rate_fraction", config.cancel_rate_fraction)
            ingress_args.append(("cancel_rate", derived_cancel_rate(dict(loadgen_args)["global_rate"], f)))
        # Design: abstraction/benchmark_runner/config/design.md#processplacement
        placement = {PROGRAM_OF[section]: dict(config.placement[section]) for section in SECTIONS}
        points.append(SweepPoint(pid, choose, server_args, loadgen_args, ingress_args, config.instances,
                                 config.ingress_machine, loadgen_target(config), placement))
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
