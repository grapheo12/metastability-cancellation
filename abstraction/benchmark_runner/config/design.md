# Config — Design

## BenchConfig

```
PROGRAM_PARAMS = {
  "server":    ["response_rate", "response_bytes", "cancel_mode", "cancel_check_interval_us", "seed",
                "n_worker_threads", "queue_sample_interval_ms"],
  "load_gen":  ["num_clients", "global_rate", "request_bytes", "slo_ms", "duration_s", "seed"],
  "ingress":   ["lb_policy", "cancel_rate", "duration_s", "seed"],
}
PLACEMENT_KEYS = ["threads", "cpu_set"]                                  # ProcessPlacement
RUNNER_KEYS = { "server":    ["listen_port", "instances"] + PLACEMENT_KEYS,
                "load_gen":  PLACEMENT_KEYS,
                "ingress":   ["machine", "listen_port", "cancel_rate_fraction"] + PLACEMENT_KEYS }
SWEEPABLE_RUNNER_KEYS = ["ingress.cancel_rate_fraction"]                 # CancelRateFraction
FORBIDDEN   = { "server": ["listen_addr", "queue_log_path"], "load_gen": ["target", "output_path"],
                "ingress": ["listen_addr", "backends", "num_clients", "output_path"] }
SECTIONS    = [server, load_gen, ingress]

function load_config(path):
    doc = parse_toml(path)                                  # ConfigError on syntax error
    allowed tables: repo, machines (with exactly server, client), server, load_gen, ingress, run
    repo_url = doc.repo.url if present else git_origin_url_of_operator_repo()
    if repo_url is none: ConfigError("[repo] url is required (no origin remote)")
    for m in [server, client]: validate_machine(m)
    for section in SECTIONS:
        for key, value in doc[section] in file order:
            if key in FORBIDDEN[section]: ConfigError("<section>.<key> is set by the runner")
            if key in RUNNER_KEYS[section]:
                if value is an array and "<section>.<key>" not in SWEEPABLE_RUNNER_KEYS:
                    ConfigError("<section>.<key> cannot be swept")
                check type and range (of every element, for an array)           # see api.md
            else if key in PROGRAM_PARAMS[section]:
                if value is an array: require non-empty, every element a scalar
                else: require a scalar
            else: ConfigError("unknown key <section>.<key>")
    for key in ["num_clients", "duration_s", "slo_ms"]:          # the runner itself needs these
        if key not in doc.load_gen: ConfigError("load_gen.<key> is required")
    check_cancel_rate_fraction(doc)                              # CancelRateFraction
    require server.listen_port + server.instances - 1 <= 65535
                         else ConfigError("server.listen_port + server.instances - 1 must be <= 65535")
    check_ingress_placement(config)                           # IngressPlacement: port collision
    validate run table; fill defaults
    if sweep_order given: require it to be a permutation of the swept keys
    # swept keys: every array-valued program parameter and every array-valued SWEEPABLE_RUNNER_KEY
    return BenchConfig
```

Error messages for the runner keys added with ProcessPlacement and CancelRateFraction:

| Situation | ConfigError message |
|---|---|
| `threads` not an integer, or `< 1` | `<section>.threads must be an integer >= 1` |
| `cpu_set` not a string matching the CPU list pattern | `<section>.cpu_set must be a CPU list like "0-3" or "0,2,4-5"` |
| `cancel_rate_fraction` (or an element) not a number, or `<= 0` | `ingress.cancel_rate_fraction must be a number > 0` |
| `cancel_rate_fraction` is an empty array | `ingress.cancel_rate_fraction must be a scalar or a non-empty array of scalars` |
| `instances` not an integer in `1..=64` | `server.instances must be an integer in 1..=64` |
| `machine` not `"client"` or `"server"` | `ingress.machine must be "client" or "server"` |
| a `[canceller]` table (from before the Ingress) | `unknown table [canceller]` |
| `[server] queue_log_path` | `server.queue_log_path is set by the runner` (the Experiment Executor always sets it, [../experiment_executor/design.md#serverlifecycle](../experiment_executor/design.md#serverlifecycle)) |

```
function validate_machine(role, table):
    allowed keys: host, user, ssh_key, ssh_port, workdir, plus data_addr for the server
    host: non-empty string                                   else ConfigError("machines.<role>.host is required")
    user, ssh_key: absent, or non-empty strings              else ConfigError("machines.<role>.<key> must be a non-empty string")
    ssh_port: absent, or integer in 1..=65535                else ConfigError("machines.<role>.ssh_port must be an integer in 1..=65535")
    workdir: non-empty string, default "~/cancellation-bench"
    if role == server:
        data_addr = table.data_addr if present
                    else "127.0.0.1" if host == "local"
                    else remote_transport.ssh_hostname(host)    # resolves a ~/.ssh/config alias
    return Machine(role, host, user or none, expand_operator_home(ssh_key) or none, ssh_port or none, workdir, data_addr)
```

Unset SSH keys stay unset (none). The transport then leaves them to the operator's SSH configuration
([../remote_transport/design.md#sshconfighost](../remote_transport/design.md#sshconfighost)). The same-machine rule
of setup compares `(host, user, ssh_port, workdir)` with unset values compared as none.

| Machine table | Result |
|---|---|
| `host = "metafail-exp-1"` only (server) | user, key and port none; `data_addr` = `HostName` from `ssh -G`, e.g. `18.144.18.190` |
| `host = "metafail-exp-1"`, `data_addr = "172.31.23.63"` | `data_addr = "172.31.23.63"` (explicit wins) |
| `host = "local"` | `data_addr = "127.0.0.1"`; SSH keys ignored |
| `user = ""` | ConfigError `machines.server.user must be a non-empty string` |

The names in `PROGRAM_PARAMS` are the parameter tables of [../../api.md](../../api.md). The runner checks only
names and shapes. Values are validated by the programs themselves, which fail fast on bad values
([../../api.md](../../api.md)). The executor reports that as a failed point.

## SweepExpansion

```
function expand_sweep(config):
    swept_keys = config.run.sweep_order if given
                 else [ "<section>.<key>" for section in SECTIONS
                        for key in config[section] in file order if value is an array ]
    value_lists = [ config.lookup(k) for k in swept_keys ]
    points = []
    for id, combo in enumerate(cartesian_product(value_lists)):    # last key varies fastest
        choose = { k: combo[i] for i, k in enumerate(swept_keys) }
        server_args  = [ (k, choose.get("server."+k, v)) for k, v in config.server program params ]
        loadgen_args = [ (k, choose.get("load_gen."+k, v)) for k, v in config.load_gen program params ]
        ingress_args = [ (k, choose.get("ingress."+k, v)) for k, v in config.ingress program params ]
        ingress_args.append(("backends", backend_list(config)))                 # Backends
        ingress_args.append(("num_clients", value of num_clients in loadgen_args))
        if "duration_s" not in ingress_args:
            ingress_args.append(("duration_s", value of duration_s in loadgen_args))
        if config.ingress has cancel_rate_fraction:                             # CancelRateFraction
            f = choose.get("ingress.cancel_rate_fraction", config.ingress.cancel_rate_fraction)
            ingress_args.append(("cancel_rate", derived_cancel_rate(value of global_rate in loadgen_args, f)))
        placement = { program: { "threads": config[section].threads or none,     # ProcessPlacement
                                 "cpu_set": config[section].cpu_set or none }
                      for (program, section) in [("server", "server"), ("loadgen", "load_gen"),
                                                 ("ingress", "ingress")] }
        points.append(SweepPoint(id, ordered(choose by swept_keys), server_args, loadgen_args, ingress_args,
                                 config.server.instances, config.ingress.machine, loadgen_target(config),
                                 placement))
    return points          # with no swept keys: exactly one point, id 0
```

Worked example. The config has `[server] cancel_mode = ["ignore", "cooperative"]` and
`[load_gen] global_rate = [1000, 2000]`, and no `sweep_order`:

| id | `server.cancel_mode` | `load_gen.global_rate` |
|---|---|---|
| 0 | ignore | 1000 |
| 1 | ignore | 2000 |
| 2 | cooperative | 1000 |
| 3 | cooperative | 2000 |

With `sweep_order = ["load_gen.global_rate", "server.cancel_mode"]`, the rows are (1000, ignore),
(1000, cooperative), (2000, ignore), (2000, cooperative). The first key in sweep order is also the x-axis of the
plots ([../result_plotter/design.md#sweepplots](../result_plotter/design.md#sweepplots)).

`num_clients` is required by the load generator, and the Ingress always receives the same value, so its
InFlightMap has exactly one slot per load-generator client at every point.

## Backends

```
function backend_list(config):
    host = "127.0.0.1" if config.ingress.machine == "server" else config.machines.server.data_addr
    return ",".join(host + ":" + decimal(config.server.listen_port + k)
                    for k in 0 ..= config.server.instances - 1)
```

`server_id` `k` is instance `k`. An Ingress on the client machine reaches the servers at the server machine's
`data_addr`; an Ingress on the server machine reaches them locally ([IngressPlacement](#ingressplacement)).

| `ingress.machine` | `data_addr` | `listen_port` | `instances` | `backends` |
|---|---|---|---|---|
| `client` | `172.31.23.63` | 8080 | 2 | `172.31.23.63:8080,172.31.23.63:8081` |
| `server` | `172.31.23.63` | 8080 | 2 | `127.0.0.1:8080,127.0.0.1:8081` |
| `client` | `127.0.0.1` | 8080 | 1 | `127.0.0.1:8080` |

## IngressPlacement

The **IngressPlacement** (`[ingress] machine`) chooses which experiment machine runs the Ingress. It is fixed for
the whole run. It decides two addresses of every SweepPoint, and which machine the Experiment Executor starts the
Ingress on and collects its outputs from.

```
function loadgen_target(config):
    host = "127.0.0.1" if config.ingress.machine == "client" else config.machines.server.data_addr
    return host + ":" + decimal(config.ingress.listen_port)

function check_ingress_placement(config):
    if config.ingress.machine == "server" and
       config.server.listen_port <= config.ingress.listen_port <= config.server.listen_port + config.server.instances - 1:
        ConfigError("ingress.listen_port collides with a server instance port")
```

| `ingress.machine` | Ingress runs on | Load generator `--target` | Ingress `backends` | Ports the client machine must reach |
|---|---|---|---|---|
| `client` (default) | client machine | `127.0.0.1:<ingress listen_port>` | `data_addr:<listen_port + k>` | every server instance's port |
| `server` | server machine, next to the servers | `<data_addr>:<ingress listen_port>` | `127.0.0.1:<listen_port + k>` | the Ingress's port only |

With `server`, the Ingress shares the server machine's CPUs with the server instances; ProcessPlacement
(`threads`, `cpu_set`) can keep them apart. The ingress port must not collide with a server instance's port
(`listen_port ..= listen_port + instances - 1`) when both are on the server machine:

| Situation | ConfigError message |
|---|---|
| `machine = "server"` and `ingress.listen_port` in the server ports | `ingress.listen_port collides with a server instance port` |

## ProcessPlacement

A program's **ProcessPlacement** is its pair (`threads`, `cpu_set`) from its section of the config. Both are fixed
for the whole run: they describe the machine split, not a parameter under study. Config only validates and records
them in every SweepPoint's `placement`. It never turns them into program arguments. The Experiment Executor applies
them when it starts each program, and the run flow ([../design.md#run-flow](../design.md#run-flow)) checks that the
implementation can honour `threads`.

**`threads` is not `n_worker_threads`.** `[server] threads` is a ProcessPlacement key: the language runtime's
worker-thread count, fixed for the run and never a program argument. `[server] n_worker_threads` is a server
program parameter ([../../api.md#server-parameters](../../api.md#server-parameters)): how many `/ping`s the server's
WorkerPool runs at once. It is passed as `--n_worker_threads` and can be swept like any program parameter. Config
does not relate the two. For the workers to burn in parallel the server needs at least `n_worker_threads + 1`
threads and CPUs ([../../server/worker_pool/design.md#concurrency-requirement](../../server/worker_pool/design.md#concurrency-requirement)),
which the operator arranges with `threads` and `cpu_set`.

| `[server]` keys | `server_args` of each point | `placement.server` |
|---|---|---|
| `n_worker_threads = [1, 4]`, `threads = 8` | point 0: `n_worker_threads 1`; point 1: `n_worker_threads 4` (plus the other server parameters) | `{"threads": 8, "cpu_set": none}` for both |
| `threads = 2` only | no `n_worker_threads` argument: the server's default `1` applies | `{"threads": 2, "cpu_set": none}` |

The CPU list pattern is `^[0-9]+(-[0-9]+)?(,[0-9]+(-[0-9]+)?)*$`, the list format of `taskset -c`. Config does not
check that the CPUs exist or that ranges are ascending. The pinning tool rejects bad lists when the program starts,
and the point then fails.

| Config | `placement` of the point |
|---|---|
| `[server] threads = 4, cpu_set = "0-3"`, nothing in the others | `{"server": {"threads": 4, "cpu_set": "0-3"}, "loadgen": {"threads": none, "cpu_set": none}, "ingress": {"threads": none, "cpu_set": none}}` |
| nothing set anywhere | all six values none |

## CancelRateFraction

**CancelRateFraction** sets the Ingress's cancel rate relative to the load generator's offered rate of the same point,
so a sweep over `global_rate` keeps the cancel load proportional.

```
function check_cancel_rate_fraction(doc):
    if "cancel_rate_fraction" not in doc.ingress: return
    if "cancel_rate" in doc.ingress:
        ConfigError("set ingress.cancel_rate or ingress.cancel_rate_fraction, not both")
    if "global_rate" not in doc.load_gen:
        ConfigError("load_gen.global_rate is required with ingress.cancel_rate_fraction")
    if any value of doc.load_gen.global_rate (the scalar, or each array element) is not a number:
        ConfigError("load_gen.global_rate must be numeric with ingress.cancel_rate_fraction")

function derived_cancel_rate(global_rate, fraction):
    return float(global_rate) * float(fraction)     # IEEE 754 double multiply, round to nearest even
```

The result is always a float, so `format_arg` writes it as the shortest decimal that parses back to the same double
([Argument formatting](#argument-formatting)). It is appended to `ingress_args` as `cancel_rate`, after
`backends`, `num_clients` and the defaulted `duration_s`. The fraction itself is never passed to any program. When it is an
array it is a swept key named `ingress.cancel_rate_fraction`, at its position in the `[ingress]` table (file
order) or in `sweep_order`.

Worked example: `[load_gen] global_rate = [10, 700]`, `[ingress] cancel_rate_fraction = [0.01, 0.75]`, no
`sweep_order`:

| id | `load_gen.global_rate` | `ingress.cancel_rate_fraction` | `cancel_rate` passed | Check |
|---|---|---|---|---|
| 0 | 10 | 0.01 | `0.1` | 10 × 0.01 = 0.1 |
| 1 | 10 | 0.75 | `7.5` | 10 × 0.75 = 7.5 |
| 2 | 700 | 0.01 | `7.0` | 700 × 0.01 = 7.0 |
| 3 | 700 | 0.75 | `525.0` | 700 × 0.75 = 525.0 |

Boundary cases:

| `global_rate` | fraction | `cancel_rate` passed | Note |
|---|---|---|---|
| 3 | 0.1 | `0.30000000000000004` | The double product is written as is. It is not rounded to the decimal the operator had in mind |
| 1000 | 1.0 | `1000.0` | Fraction 1 gives the same rate as the load generator |
| 10 | 2.5 | `25.0` | Fractions above 1 are allowed |

## Argument formatting

```
function format_arg(value):
    boolean -> "true" / "false"
    integer -> decimal, no separators
    float   -> shortest decimal string that parses back to the same value (e.g. 0.5, 2000.0 -> "2000.0")
    string  -> as is
```

Each argument is passed as two words, `--<name>` and `format_arg(value)`. Quoting for the remote shell is the
Transport's job ([../remote_transport/design.md#command-quoting](../remote_transport/design.md#command-quoting)).

## Design specific to Python

Shared tool `implementation/scripts/` (tooling language Python 3.11+; see `AGENTS.md` "Shared tooling"). Identical on
every `impl/<lang>` branch. Plotting needs `matplotlib` (`python3 -m pip install -r implementation/scripts/requirements.txt`).
Tests: `python3 -B -m unittest discover -s implementation/scripts/tests -t implementation/scripts`.

### Python choices

- TOML is parsed with the standard library's `tomllib`, which keeps table key order (needed for the file order of the sweep).
- `BenchConfig`, `Machine`, `RunOptions` and `SweepPoint` are `dataclasses`. Program parameters are plain `dict`s in insertion order.
- Integers exclude `bool` (Python's `bool` is a subclass of `int`). `format_arg` checks `bool` before `int`, and writes floats with `repr`, which is the shortest round-trip form.
- `itertools.product` gives the cross product with the last key varying fastest.
- `validate_machine` is `_machine`. Unset `user`, `ssh_key` and `ssh_port` are `None` in `Machine`. The default `data_addr` calls `transport.ssh_hostname` only when the server table has no `data_addr`, so an explicit address never runs `ssh -G`.
- The CPU list pattern is a compiled `re` pattern used with `fullmatch`. `BenchConfig.placement` is keyed by section (`server`, `load_gen`, `ingress`) and `SweepPoint.placement` by program (`server`, `loadgen`, `ingress`), so each point holds its own copy.
- `cancel_rate_fraction` is kept outside the program parameter dicts, in `BenchConfig.cancel_rate_fraction`, so it is never passed to a program. `lookup("ingress.cancel_rate_fraction")` returns it. The swept keys in file order are collected while parsing (`BenchConfig.file_swept`), so the fraction keeps its place among the `[ingress]` keys.
- `listen_port` and `instances` of `[server]` and `listen_port` of `[ingress]` are `BenchConfig.listen_port`, `.instances` and `.ingress_port`. `backend_list` builds `backends` from the server machine's `data_addr`, or `127.0.0.1` when `ingress_machine` is `server`; `loadgen_target` is computed once per run and stored in every SweepPoint.

### Traceability

Generated from the `# Design:` tags in the code by `implementation/spec_tools/tracegen.py` (see `AGENTS.md`). Each link points at the first code line after the tag.
Regenerate from the repository root with `python3 implementation/spec_tools/tracegen.py --lang Python --impl implementation/scripts --ext .py --comment '#' --write`.

<!-- trace:begin -->
| Design subsection | Implementation (file:line) |
|---|---|
| [BenchConfig](#benchconfig) | [config.py:16](../../../implementation/scripts/benchrunner/config.py#L16), [config.py:129](../../../implementation/scripts/benchrunner/config.py#L129), [config.py:166](../../../implementation/scripts/benchrunner/config.py#L166), [test_config.py:102](../../../implementation/scripts/tests/test_config.py#L102), [test_config.py:116](../../../implementation/scripts/tests/test_config.py#L116), [test_config.py:172](../../../implementation/scripts/tests/test_config.py#L172) |
| [SweepExpansion](#sweepexpansion) | [config.py:354](../../../implementation/scripts/benchrunner/config.py#L354), [test_config.py:47](../../../implementation/scripts/tests/test_config.py#L47), [test_config.py:59](../../../implementation/scripts/tests/test_config.py#L59) |
| [Backends](#backends) | [config.py:299](../../../implementation/scripts/benchrunner/config.py#L299), [test_config.py:66](../../../implementation/scripts/tests/test_config.py#L66) |
| [IngressPlacement](#ingressplacement) | [config.py:210](../../../implementation/scripts/benchrunner/config.py#L210), [config.py:306](../../../implementation/scripts/benchrunner/config.py#L306), [config.py:313](../../../implementation/scripts/benchrunner/config.py#L313), [test_config.py:81](../../../implementation/scripts/tests/test_config.py#L81) |
| [ProcessPlacement](#processplacement) | [config.py:23](../../../implementation/scripts/benchrunner/config.py#L23), [config.py:219](../../../implementation/scripts/benchrunner/config.py#L219), [config.py:375](../../../implementation/scripts/benchrunner/config.py#L375), [test_config.py:180](../../../implementation/scripts/tests/test_config.py#L180), [test_config.py:197](../../../implementation/scripts/tests/test_config.py#L197) |
| [CancelRateFraction](#cancelratefraction) | [config.py:30](../../../implementation/scripts/benchrunner/config.py#L30), [config.py:228](../../../implementation/scripts/benchrunner/config.py#L228), [config.py:286](../../../implementation/scripts/benchrunner/config.py#L286), [config.py:319](../../../implementation/scripts/benchrunner/config.py#L319), [config.py:371](../../../implementation/scripts/benchrunner/config.py#L371), [test_config.py:144](../../../implementation/scripts/tests/test_config.py#L144), [test_config.py:157](../../../implementation/scripts/tests/test_config.py#L157) |
| [Argument formatting](#argument-formatting) | [config.py:382](../../../implementation/scripts/benchrunner/config.py#L382), [test_config.py:139](../../../implementation/scripts/tests/test_config.py#L139) |
<!-- trace:end -->
