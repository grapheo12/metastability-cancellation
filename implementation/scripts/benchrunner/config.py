# Tooling language: Python 3.11+ (shared tool implementation/scripts)
# Source: abstraction/benchmark_runner/config/design.md (config v0.29.0)
"""BenchConfig loading and validation, SweepExpansion, argument formatting."""

import datetime
import itertools
import json
import math
import os
import re
import subprocess
import tomllib
from dataclasses import dataclass

from . import transport

# Design: abstraction/benchmark_runner/config/design.md#benchconfig
PROGRAM_PARAMS = {
    "server": ["response_rate", "response_bytes", "cancel_mode", "cancel_check_interval_us", "seed",
               "n_worker_threads", "queue_sample_interval_ms", "initial_stall_ms", "heartbeat_interval_ms",
               "workload", "window_size", "n_gc_binary_tree_size", "n_messages_per_request",
               "queue_discipline", "freeze_after_ms", "reject_past_deadline"],
    "load_gen": ["num_clients", "global_rate", "request_bytes", "slo_ms", "duration_s", "seed",
                 "n_waiting_requests", "retry_timeout_ms", "max_retries", "initial_stall_ms",
                 "slo_cancel", "drain_timeout_ms", "arrival_rng"],
    "ingress": ["lb_policy", "ewma_beta", "cancel_rate", "duration_s", "seed", "initial_stall_ms",
                "heartbeat_timeout_ms", "lb_filter", "lb_retry_policy", "lb_max_retries",
                "lb_retry_timeout_ms", "lb_retry_anxiety_ratio", "lb_retry_late_factor", "forward_mode",
                "lb_filter_margin"],
}
# Design: abstraction/benchmark_runner/config/design.md#processplacement
PLACEMENT_KEYS = ["threads", "cpu_set"]
RUNNER_KEYS = {
    "server": ["listen_port", "instances", "gc_trace", "freeze_instance",
               "scenario", "scenario_by_instance"] + PLACEMENT_KEYS,
    "load_gen": PLACEMENT_KEYS,
    "ingress": ["machine", "listen_port", "cancel_rate_fraction"] + PLACEMENT_KEYS,
}
# Design: abstraction/benchmark_runner/config/design.md#cancelratefraction
SWEEPABLE_RUNNER_KEYS = ["ingress.cancel_rate_fraction"]
CPU_LIST = re.compile(r"[0-9]+(-[0-9]+)?(,[0-9]+(-[0-9]+)?)*")
PROGRAM_OF = {"server": "server", "load_gen": "loadgen", "ingress": "ingress"}
MAX_INSTANCES = 64
MAX_SERVER_MACHINES = 16  # ServerMachines
FORBIDDEN = {
    "server": ["listen_addr", "queue_log_path", "service_scenario", "scenario_log_path"],
    "load_gen": ["target", "output_path"],
    "ingress": ["listen_addr", "backends", "num_clients", "output_path"],
}
SECTIONS = ("server", "load_gen", "ingress")
TOP_LEVEL = {"repo", "machines", "server", "load_gen", "ingress", "run", "scenarios"}
# Design: abstraction/benchmark_runner/config/design.md#scenarioassignment
SCENARIO_KEYS = ("states", "schedule", "markov")
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
    link: dict = None  # LinkedSweep: linked key -> driver key, file order; {} when unset


@dataclass
class BenchConfig:
    repo_url: str
    machines: dict  # "server" -> list of Machine (ServerMachines, file order), "client" -> Machine
    params: dict  # section -> {program parameter: scalar or list}, file order kept
    listen_port: int  # the k-th instance on a server machine listens on listen_port + k
    instances: int  # per server machine
    ingress_port: int
    ingress_machine: str  # IngressPlacement: "client" | "server"
    run: RunOptions
    placement: dict  # section -> {"threads": int | None, "cpu_set": str | None}
    cancel_rate_fraction: object = None  # number, list of numbers, or None (CancelRateFraction)
    file_swept: list = None  # swept keys in file order, program parameters and sweepable runner keys
    gc_trace: bool = False  # [server] gc_trace: run the servers with the BuildManifest's GcTraceHook
    freeze_instance: object = None  # [server] freeze_instance (FreezeTarget): instance id, or None
    scenarios: dict = None  # [scenarios.<name>] tables, file order; {} when absent (ScenarioAssignment)
    scenario: str | None = None  # [server] scenario: every instance's scenario name, or None
    scenario_by_instance: dict = None  # [server] scenario_by_instance: "<instance id>" -> name; {} when unset

    def swept_keys(self):
        if self.run.sweep_order is not None:
            return list(self.run.sweep_order)
        # Design: abstraction/benchmark_runner/config/design.md#linkedsweep
        return [k for k in (self.file_swept or []) if k not in (self.run.link or {})]

    def total_instances(self):
        return len(self.machines["server"]) * self.instances

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
def _machine(role, t, label=None):
    """validate_machine: unset SSH keys stay None and come from the operator's ~/.ssh/config."""
    label = label or f"machines.{role}"
    _check_keys(t, {"host", "user", "ssh_key", "ssh_port", "workdir"} | ({"data_addr"} if role == "server" else set()),
                label)
    host = t.get("host")
    if not isinstance(host, str) or not host:
        raise ConfigError(f"{label}.host is required")
    local = host == "local"
    for key in ("user", "ssh_key"):
        if key in t and not (isinstance(t[key], str) and t[key]):
            raise ConfigError(f"{label}.{key} must be a non-empty string")
    user, key = t.get("user"), t.get("ssh_key")
    port = t.get("ssh_port")
    if port is not None and (not _is_int(port) or not 1 <= port <= 65535):
        raise ConfigError(f"{label}.ssh_port must be an integer in 1..=65535")
    workdir = t.get("workdir", "~/cancellation-bench")
    if not isinstance(workdir, str) or not workdir:
        raise ConfigError(f"{label}.workdir must be a non-empty string")
    data_addr = None
    if role == "server":
        if "data_addr" in t:
            data_addr = t["data_addr"]
        else:  # an alias only the operator's SSH config knows is resolved to its HostName
            data_addr = "127.0.0.1" if local else transport.ssh_hostname(host)
        if not isinstance(data_addr, str) or not data_addr:
            raise ConfigError(f"{label}.data_addr must be a non-empty string")
    return Machine(role, host, user, os.path.expanduser(key) if key else None, port, workdir, data_addr)


# Design: abstraction/benchmark_runner/config/design.md#servermachines
def _server_machines(value):
    """validate_server_machines: one table, or an array of 1..=16 tables naming distinct machines."""
    if isinstance(value, dict):
        return [_machine("server", value, "machines.server")]
    if (not isinstance(value, list) or not 1 <= len(value) <= MAX_SERVER_MACHINES
            or not all(isinstance(t, dict) for t in value)):
        raise ConfigError("machines.server must be a table or an array of 1..=16 tables")
    machines = [_machine("server", t, f"machines.server[{m}]") for m, t in enumerate(value)]
    for a, b in itertools.combinations(range(len(machines)), 2):
        ma, mb = machines[a], machines[b]
        if (ma.host, ma.user, ma.ssh_port, ma.workdir) == (mb.host, mb.user, mb.ssh_port, mb.workdir):
            raise ConfigError(f"machines.server[{a}] and machines.server[{b}] are the same machine "
                              "(use server.instances)")
    return machines


# Design: abstraction/benchmark_runner/config/design.md#servermachines
def instance_machine(config, sid):
    return sid // config.instances


def instance_port(config, sid):
    return config.listen_port + sid % config.instances


def server_hosts(config):
    return [instance_machine(config, sid) for sid in range(config.total_instances())]


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
    machines = {"server": _server_machines(machines["server"]), "client": _machine("client", machines["client"])}

    params, fraction, file_swept = {}, None, []
    ports = {"server": 8080, "ingress": 9090}
    instances = 1
    gc_trace = False
    freeze_instance = None
    scenario, scenario_by = None, {}
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
                # Design: abstraction/benchmark_runner/config/design.md#benchconfig
                if key == "gc_trace":
                    if not isinstance(value, bool):  # an array too: it cannot be swept
                        raise ConfigError(f"{name} must be true or false")
                    gc_trace = value
                    continue
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
                elif key == "freeze_instance":  # FreezeTarget; range checked by check_freeze_target
                    freeze_instance = value
                # Design: abstraction/benchmark_runner/config/design.md#scenarioassignment
                elif key == "scenario":  # checked by check_scenario_assignment
                    scenario = value
                elif key == "scenario_by_instance":
                    scenario_by = value
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
    check_ingress_placement(ingress_machine, ports, instances, len(machines["server"]))
    check_freeze_target(freeze_instance, params["server"], len(machines["server"]) * instances)
    scenarios = check_scenario_assignment(doc.get("scenarios", {}), scenario, scenario_by, params["server"],
                                          len(machines["server"]) * instances)

    run = doc.get("run", {})
    _check_keys(run, {"output_dir", "sweep_order", "cooldown_s", "on_failure", "server_ready_timeout_s",
                      "stop_timeout_s", "client_timeout_margin_s", "setup_timeout_s", "link"}, "run")
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
        link=_link_table(run.get("link", {})),
    )
    config = BenchConfig(repo_url, machines, params, ports["server"], instances, ports["ingress"], ingress_machine,
                         options, placement, fraction, file_swept, gc_trace, freeze_instance,
                         scenarios, scenario, dict(scenario_by))
    swept = [k for k in file_swept if k not in options.link]
    if sweep_order is not None:
        if sorted(sweep_order) != sorted(swept) or len(set(sweep_order)) != len(sweep_order):
            raise ConfigError("run.sweep_order must list every swept key exactly once")
    check_link(config, swept)
    return config


# Design: abstraction/benchmark_runner/config/design.md#linkedsweep
def _link_table(link):
    if not isinstance(link, dict) or not all(isinstance(k, str) and isinstance(v, str) and "." in k and "." in v
                                             for k, v in link.items()):
        raise ConfigError('run.link must map "<section>.<param>" keys to "<section>.<param>" keys')
    return dict(link)


# Design: abstraction/benchmark_runner/config/design.md#linkedsweep
def check_link(config, swept):
    for linked, driver in config.run.link.items():
        section, name = linked.split(".", 1)
        if section not in SECTIONS or not isinstance(config.params[section].get(name), list):
            raise ConfigError(f"run.link: {linked} must be an array program parameter")
        if driver not in swept:  # an array key that is not itself linked
            raise ConfigError(f"run.link: driver {driver} of {linked} must be a swept key")
        if len(config.lookup(linked)) != len(config.lookup(driver)):
            raise ConfigError(f"run.link: {linked} and {driver} must have the same number of values")


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
    def host(sid):  # an Ingress on the (only) server machine reaches the instances locally
        if config.ingress_machine == "server":
            return "127.0.0.1"
        return config.machines["server"][instance_machine(config, sid)].data_addr
    return ",".join(f"{host(sid)}:{instance_port(config, sid)}" for sid in range(config.total_instances()))


# Design: abstraction/benchmark_runner/config/design.md#ingressplacement
def loadgen_target(config):
    """The Ingress as seen from the client machine."""
    host = "127.0.0.1" if config.ingress_machine == "client" else config.machines["server"][0].data_addr
    return f"{host}:{config.ingress_port}"


# Design: abstraction/benchmark_runner/config/design.md#ingressplacement
def check_ingress_placement(ingress_machine, ports, instances, n_server_machines=1):
    if ingress_machine == "server" and n_server_machines > 1:
        raise ConfigError('ingress.machine = "server" needs exactly one server machine')
    if ingress_machine == "server" and ports["server"] <= ports["ingress"] <= ports["server"] + instances - 1:
        raise ConfigError("ingress.listen_port collides with a server instance port")


# Design: abstraction/benchmark_runner/config/design.md#freezetarget
def check_freeze_target(freeze_instance, server_params, total):
    if freeze_instance is None:
        return
    if not _is_int(freeze_instance) or not 0 <= freeze_instance <= total - 1:
        raise ConfigError(f"server.freeze_instance must be an integer in 0..={total - 1}")
    if "freeze_after_ms" not in server_params:
        raise ConfigError("server.freeze_instance needs server.freeze_after_ms")


# Design: abstraction/benchmark_runner/config/design.md#cancelratefraction
def _scenario_value_ok(v):
    """Only tables, arrays, strings, integers, finite floats and booleans (JSON can carry them)."""
    if isinstance(v, dict):
        return all(_scenario_value_ok(x) for x in v.values())
    if isinstance(v, list):
        return all(_scenario_value_ok(x) for x in v)
    if isinstance(v, float):
        return math.isfinite(v)
    return isinstance(v, (str, int, bool))


# Design: abstraction/benchmark_runner/config/design.md#scenarioassignment
def check_scenario_assignment(scen, default, by, server_params, total):
    """Names and shapes of [scenarios] and the ScenarioAssignment keys; returns the [scenarios] tables."""
    if not isinstance(scen, dict):
        raise ConfigError("[scenarios] must be a table")
    for name, t in scen.items():
        if not isinstance(t, dict):
            raise ConfigError(f"scenarios.{name} must be a table")
        for key in t:
            if key == "stream":
                raise ConfigError(f"scenarios.{name}.stream is set by the runner")
            if key not in SCENARIO_KEYS:
                raise ConfigError(f"unknown key scenarios.{name}.{key}")
        if "states" not in t:
            raise ConfigError(f"scenarios.{name}.states is required")
        if ("schedule" in t) == ("markov" in t):
            raise ConfigError(f"scenarios.{name} needs exactly one of schedule and markov")
        if not _scenario_value_ok(t):
            raise ConfigError(f"scenarios.{name} may hold only tables, arrays, strings, numbers and booleans")
    if default is not None and not (isinstance(default, str) and default in scen):
        raise ConfigError("server.scenario must name a [scenarios] table")
    if not isinstance(by, dict):
        raise ConfigError('server.scenario_by_instance must be a table of "<instance>" = "<scenario>"')
    for k, name in by.items():
        if not (k == "0" or re.fullmatch(r"[1-9][0-9]*", k)) or int(k) > total - 1:
            raise ConfigError(f"server.scenario_by_instance: instance {k} must be in 0..={total - 1}")
        if not (isinstance(name, str) and name in scen):
            raise ConfigError(f"server.scenario_by_instance: {name} must name a [scenarios] table")
    if default is not None or by:
        w = server_params.get("workload")
        if w is None or not all(v == "sleep_constant" for v in (w if isinstance(w, list) else [w])):
            raise ConfigError('server.scenario needs server.workload = "sleep_constant"')
    return scen


# Design: abstraction/benchmark_runner/config/design.md#scenarioassignment
def scenario_assignment(config):
    """Per instance id: {"name", "service_scenario"} (compact JSON, stream = the id), or None."""
    out = []
    by = config.scenario_by_instance or {}
    for k in range(config.total_instances()):
        name = by.get(str(k), config.scenario)
        if name is None:
            out.append(None)
            continue
        obj = dict(config.scenarios[name])  # file order
        obj["stream"] = k
        out.append({"name": name, "service_scenario": json.dumps(obj, separators=(",", ":"))})
    return out


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
    linked: dict = None  # LinkedSweep: linked key -> this point's value; {} when unused
    server_hosts: list = None  # ServerMachines: server machine index of each instance id; None = all on machine 0
    freeze: dict = None  # FreezeTarget: {"instance", "freeze_after_ms"}, or None
    scenarios: list = None  # ScenarioAssignment: per instance {"name", "service_scenario"} or None

    def hosts(self):
        return self.server_hosts if self.server_hosts is not None else [0] * self.server_instances

    def to_json(self):
        return {
            "id": self.id,
            "swept": self.swept,
            "linked": self.linked or {},
            "server_args": [[k, v] for k, v in self.server_args],
            "loadgen_args": [[k, v] for k, v in self.loadgen_args],
            "ingress_args": [[k, v] for k, v in self.ingress_args],
            "server_instances": self.server_instances,
            "server_hosts": self.hosts(),
            "ingress_machine": self.ingress_machine,
            "loadgen_target": self.loadgen_target,
            "placement": self.placement,
            "freeze": self.freeze,
            "scenarios": self.scenarios if self.scenarios is not None else [None] * self.server_instances,
        }

    def arg(self, program, name):
        args = {"server": self.server_args, "loadgen": self.loadgen_args, "ingress": self.ingress_args}
        return dict(args[program]).get(name)


# Design: abstraction/benchmark_runner/config/design.md#sweepexpansion
def expand_sweep(config):
    """Cross product of the swept keys; the last key varies fastest."""
    keys = config.swept_keys()
    points = []
    for pid, idx in enumerate(itertools.product(*[range(len(config.lookup(k))) for k in keys])):
        swept = {k: config.lookup(k)[i] for k, i in zip(keys, idx)}
        # LinkedSweep: a linked key takes the element at its driver's index.
        # Design: abstraction/benchmark_runner/config/design.md#linkedsweep
        linked = {l: config.lookup(l)[idx[keys.index(d)]] for l, d in config.run.link.items()}
        choose = {**swept, **linked}

        def args(section):
            return [(k, choose.get(f"{section}.{k}", v)) for k, v in config.params[section].items()]

        server_args, loadgen_args = args("server"), args("load_gen")
        # StallWindow: the load generator's window follows the point's server StallGate.
        # Design: abstraction/benchmark_runner/config/design.md#sweepexpansion
        if "initial_stall_ms" not in dict(loadgen_args) and "initial_stall_ms" in dict(server_args):
            loadgen_args.append(("initial_stall_ms", dict(server_args)["initial_stall_ms"]))
        ingress_args = args("ingress")
        ingress_args.append(("backends", backend_list(config)))
        ingress_args.append(("num_clients", dict(loadgen_args)["num_clients"]))
        if "duration_s" not in dict(ingress_args):
            ingress_args.append(("duration_s", dict(loadgen_args)["duration_s"]))
        # Design: abstraction/benchmark_runner/config/design.md#cancelratefraction
        if config.cancel_rate_fraction is not None:
            f = choose.get("ingress.cancel_rate_fraction", config.cancel_rate_fraction)
            ingress_args.append(("cancel_rate", derived_cancel_rate(dict(loadgen_args)["global_rate"], f)))
        # Design: abstraction/benchmark_runner/config/design.md#freezetarget
        freeze = None
        if config.freeze_instance is not None:
            freeze = {"instance": config.freeze_instance, "freeze_after_ms": dict(server_args)["freeze_after_ms"]}
            server_args = [(k, v) for k, v in server_args if k != "freeze_after_ms"]
        scenarios = scenario_assignment(config)  # ScenarioAssignment: the same at every point
        # Design: abstraction/benchmark_runner/config/design.md#processplacement
        placement = {PROGRAM_OF[section]: dict(config.placement[section]) for section in SECTIONS}
        # Design: abstraction/benchmark_runner/config/design.md#servermachines
        points.append(SweepPoint(pid, swept, server_args, loadgen_args, ingress_args, config.total_instances(),
                                 config.ingress_machine, loadgen_target(config), placement, linked,
                                 server_hosts(config), freeze, scenarios))
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
