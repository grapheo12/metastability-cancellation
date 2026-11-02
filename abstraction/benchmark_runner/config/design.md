# Config — Design

## BenchConfig

```
PROGRAM_PARAMS = {
  "server":    ["response_rate", "response_bytes", "cancel_mode", "cancel_check_interval_us", "seed"],
  "load_gen":  ["num_clients", "global_rate", "request_bytes", "slo_ms", "duration_s", "seed"],
  "canceller": ["cancel_rate", "incr_rand_max", "duration_s", "seed"],
}
PLACEMENT_KEYS = ["threads", "cpu_set"]                                  # ProcessPlacement
RUNNER_KEYS = { "server":    ["listen_port"] + PLACEMENT_KEYS,
                "load_gen":  PLACEMENT_KEYS,
                "canceller": ["enabled", "cancel_rate_fraction"] + PLACEMENT_KEYS }
SWEEPABLE_RUNNER_KEYS = ["canceller.cancel_rate_fraction"]               # CancelRateFraction
FORBIDDEN   = { "server": ["listen_addr"], "load_gen": ["target", "output_path"],
                "canceller": ["target", "num_clients", "output_path"] }

function load_config(path):
    doc = parse_toml(path)                                  # ConfigError on syntax error
    allowed tables: repo, machines (with exactly server, client), server, load_gen, canceller, run
    repo_url = doc.repo.url if present else git_origin_url_of_operator_repo()
    if repo_url is none: ConfigError("[repo] url is required (no origin remote)")
    for m in [server, client]: validate_machine(m)
    for section in [server, load_gen, canceller]:
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
| `cancel_rate_fraction` (or an element) not a number, or `<= 0` | `canceller.cancel_rate_fraction must be a number > 0` |
| `cancel_rate_fraction` is an empty array | `canceller.cancel_rate_fraction must be a scalar or a non-empty array of scalars` |

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
                 else [ "<section>.<key>" for section in [server, load_gen, canceller]
                        for key in config[section] in file order if value is an array ]
    value_lists = [ config.lookup(k) for k in swept_keys ]
    points = []
    for id, combo in enumerate(cartesian_product(value_lists)):    # last key varies fastest
        choose = { k: combo[i] for i, k in enumerate(swept_keys) }
        server_args  = [ (k, choose.get("server."+k, v)) for k, v in config.server program params ]
        loadgen_args = [ (k, choose.get("load_gen."+k, v)) for k, v in config.load_gen program params ]
        if config.canceller.enabled:
            canceller_args = [ (k, choose.get("canceller."+k, v)) for k, v in config.canceller program params ]
            canceller_args.append(("num_clients", value of num_clients in loadgen_args))
            if "duration_s" not in canceller_args:
                canceller_args.append(("duration_s", value of duration_s in loadgen_args))
            if config.canceller has cancel_rate_fraction:                       # CancelRateFraction
                f = choose.get("canceller.cancel_rate_fraction", config.canceller.cancel_rate_fraction)
                canceller_args.append(("cancel_rate", derived_cancel_rate(value of global_rate in loadgen_args, f)))
        else:
            canceller_args = none
        placement = { program: { "threads": config[section].threads or none,     # ProcessPlacement
                                 "cpu_set": config[section].cpu_set or none }
                      for (program, section) in [("server", "server"), ("loadgen", "load_gen"),
                                                 ("canceller", "canceller")] }
        points.append(SweepPoint(id, ordered(choose by swept_keys), server_args, loadgen_args, canceller_args,
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

`num_clients` is required by the load generator, and the canceller always receives the same value, so the
canceller's client ids match the load generator's clients at every point.

## ProcessPlacement

A program's **ProcessPlacement** is its pair (`threads`, `cpu_set`) from its section of the config. Both are fixed
for the whole run: they describe the machine split, not a parameter under study. Config only validates and records
them in every SweepPoint's `placement`. It never turns them into program arguments. The Experiment Executor applies
them when it starts each program, and the run flow ([../design.md#run-flow](../design.md#run-flow)) checks that the
implementation can honour `threads`.

The CPU list pattern is `^[0-9]+(-[0-9]+)?(,[0-9]+(-[0-9]+)?)*$`, the list format of `taskset -c`. Config does not
check that the CPUs exist or that ranges are ascending. The pinning tool rejects bad lists when the program starts,
and the point then fails.

| Config | `placement` of the point |
|---|---|
| `[server] threads = 4, cpu_set = "0-3"`, nothing in the others | `{"server": {"threads": 4, "cpu_set": "0-3"}, "loadgen": {"threads": none, "cpu_set": none}, "canceller": {"threads": none, "cpu_set": none}}` |
| nothing set anywhere | all six values none |

## CancelRateFraction

**CancelRateFraction** sets the canceller's rate relative to the load generator's offered rate of the same point,
so a sweep over `global_rate` keeps the cancel load proportional.

```
function check_cancel_rate_fraction(doc):
    if "cancel_rate_fraction" not in doc.canceller: return
    if "cancel_rate" in doc.canceller:
        ConfigError("set canceller.cancel_rate or canceller.cancel_rate_fraction, not both")
    if doc.canceller.enabled is false:
        ConfigError("canceller.cancel_rate_fraction needs the canceller enabled")
    if "global_rate" not in doc.load_gen:
        ConfigError("load_gen.global_rate is required with canceller.cancel_rate_fraction")
    if any value of doc.load_gen.global_rate (the scalar, or each array element) is not a number:
        ConfigError("load_gen.global_rate must be numeric with canceller.cancel_rate_fraction")

function derived_cancel_rate(global_rate, fraction):
    return float(global_rate) * float(fraction)     # IEEE 754 double multiply, round to nearest even
```

The result is always a float, so `format_arg` writes it as the shortest decimal that parses back to the same double
([Argument formatting](#argument-formatting)). It is appended to `canceller_args` as `cancel_rate`, after
`num_clients` and the defaulted `duration_s`. The fraction itself is never passed to any program. When it is an
array it is a swept key named `canceller.cancel_rate_fraction`, at its position in the `[canceller]` table (file
order) or in `sweep_order`.

Worked example: `[load_gen] global_rate = [10, 700]`, `[canceller] cancel_rate_fraction = [0.01, 0.75]`, no
`sweep_order`:

| id | `load_gen.global_rate` | `canceller.cancel_rate_fraction` | `cancel_rate` passed | Check |
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
- The CPU list pattern is a compiled `re` pattern used with `fullmatch`. `BenchConfig.placement` is keyed by section (`server`, `load_gen`, `canceller`) and `SweepPoint.placement` by program (`server`, `loadgen`, `canceller`), so each point holds its own copy.
- `cancel_rate_fraction` is kept outside the program parameter dicts, in `BenchConfig.cancel_rate_fraction`, so it is never passed to a program. `lookup("canceller.cancel_rate_fraction")` returns it. The swept keys in file order are collected while parsing (`BenchConfig.file_swept`), so the fraction keeps its place among the `[canceller]` keys.

### Traceability

Generated from the `# Design:` tags in the code by `implementation/spec_tools/tracegen.py` (see `AGENTS.md`). Each link points at the first code line after the tag.
Regenerate from the repository root with `python3 implementation/spec_tools/tracegen.py --lang Python --impl implementation/scripts --ext .py --comment '#' --write`.

<!-- trace:begin -->
| Design subsection | Implementation (file:line) |
|---|---|
| [BenchConfig](#benchconfig) | [config.py:16](../../../implementation/scripts/benchrunner/config.py#L16), [config.py:125](../../../implementation/scripts/benchrunner/config.py#L125), [config.py:162](../../../implementation/scripts/benchrunner/config.py#L162), [test_config.py:72](../../../implementation/scripts/tests/test_config.py#L72), [test_config.py:86](../../../implementation/scripts/tests/test_config.py#L86), [test_config.py:144](../../../implementation/scripts/tests/test_config.py#L144) |
| [SweepExpansion](#sweepexpansion) | [config.py:314](../../../implementation/scripts/benchrunner/config.py#L314), [test_config.py:47](../../../implementation/scripts/tests/test_config.py#L47), [test_config.py:57](../../../implementation/scripts/tests/test_config.py#L57) |
| [ProcessPlacement](#processplacement) | [config.py:22](../../../implementation/scripts/benchrunner/config.py#L22), [config.py:207](../../../implementation/scripts/benchrunner/config.py#L207), [config.py:336](../../../implementation/scripts/benchrunner/config.py#L336), [test_config.py:150](../../../implementation/scripts/tests/test_config.py#L150) |
| [CancelRateFraction](#cancelratefraction) | [config.py:29](../../../implementation/scripts/benchrunner/config.py#L29), [config.py:216](../../../implementation/scripts/benchrunner/config.py#L216), [config.py:270](../../../implementation/scripts/benchrunner/config.py#L270), [config.py:285](../../../implementation/scripts/benchrunner/config.py#L285), [config.py:332](../../../implementation/scripts/benchrunner/config.py#L332), [test_config.py:114](../../../implementation/scripts/tests/test_config.py#L114), [test_config.py:127](../../../implementation/scripts/tests/test_config.py#L127) |
| [Argument formatting](#argument-formatting) | [config.py:342](../../../implementation/scripts/benchrunner/config.py#L342), [test_config.py:109](../../../implementation/scripts/tests/test_config.py#L109) |
<!-- trace:end -->
