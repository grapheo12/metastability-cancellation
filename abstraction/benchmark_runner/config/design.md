# Config — Design

## BenchConfig

```
PROGRAM_PARAMS = {
  "server":    ["response_rate", "response_bytes", "cancel_mode", "cancel_check_interval_us", "seed",
                "n_worker_threads", "queue_sample_interval_ms", "initial_stall_ms",
                "workload", "window_size", "n_gc_binary_tree_size", "n_messages_per_request",
                "queue_discipline", "freeze_after_ms"],
  "load_gen":  ["num_clients", "global_rate", "request_bytes", "slo_ms", "duration_s", "seed",
                "n_waiting_requests", "retry_timeout_ms", "max_retries", "initial_stall_ms"],
  "ingress":   ["lb_policy", "ewma_beta", "cancel_rate", "duration_s", "seed", "initial_stall_ms"],
}
PLACEMENT_KEYS = ["threads", "cpu_set"]                                  # ProcessPlacement
RUNNER_KEYS = { "server":    ["listen_port", "instances", "gc_trace", "freeze_instance"] + PLACEMENT_KEYS,
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
    server_machines = validate_server_machines(doc.machines.server)   # ServerMachines: a list, in file order
    client_machine  = validate_machine(client, doc.machines.client, "machines.client")
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
    require server.gc_trace, if present, is a boolean    else ConfigError("server.gc_trace must be true or false")   # GcTrace
    require server.listen_port + server.instances - 1 <= 65535
                         else ConfigError("server.listen_port + server.instances - 1 must be <= 65535")
    check_ingress_placement(config)                           # IngressPlacement: one server machine, port collision
    check_freeze_target(config)                               # FreezeTarget
    validate run table; fill defaults
    link = run.link or empty map                              # LinkedSweep
    # swept keys: every array-valued program parameter and every array-valued SWEEPABLE_RUNNER_KEY,
    # except the linked keys (the keys of link)
    if sweep_order given: require it to be a permutation of the swept keys
    check_link(config)                                        # LinkedSweep
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
| `gc_trace` not a boolean (an array included: it cannot be swept) | `server.gc_trace must be true or false` |
| `machine` not `"client"` or `"server"` | `ingress.machine must be "client" or "server"` |
| `freeze_instance` problems | see [FreezeTarget](#freezetarget) |
| `[machines.server]` problems | see [ServerMachines](#servermachines) |
| a `[canceller]` table (from before the Ingress) | `unknown table [canceller]` |
| `run.link` problems | see [LinkedSweep](#linkedsweep) |
| `[server] queue_log_path` | `server.queue_log_path is set by the runner` (the Experiment Executor always sets it, [../experiment_executor/design.md#serverlifecycle](../experiment_executor/design.md#serverlifecycle)) |

```
function validate_machine(role, table, label):              # label: "machines.client", "machines.server" or "machines.server[<m>]"
    allowed keys: host, user, ssh_key, ssh_port, workdir, plus data_addr for the server
    host: non-empty string                                   else ConfigError("<label>.host is required")
    user, ssh_key: absent, or non-empty strings              else ConfigError("<label>.<key> must be a non-empty string")
    ssh_port: absent, or integer in 1..=65535                else ConfigError("<label>.ssh_port must be an integer in 1..=65535")
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
| `user = ""` (one server table) | ConfigError `machines.server.user must be a non-empty string` |

The names in `PROGRAM_PARAMS` are the parameter tables of [../../api.md](../../api.md). The runner checks only
names and shapes. Values are validated by the programs themselves, which fail fast on bad values
([../../api.md](../../api.md)). The executor reports that as a failed point.

## SweepExpansion

```
function expand_sweep(config):
    swept_keys = config.run.sweep_order if given
                 else [ "<section>.<key>" for section in SECTIONS
                        for key in config[section] in file order
                        if value is an array and "<section>.<key>" not in config.run.link ]
    index_lists = [ [0 .. len(config.lookup(k)) - 1] for k in swept_keys ]
    points = []
    for id, idx in enumerate(cartesian_product(index_lists)):      # last key varies fastest
        swept  = { k: config.lookup(k)[idx[i]] for i, k in enumerate(swept_keys) }
        linked = { l: config.lookup(l)[idx[swept_keys.index(d)]]     # LinkedSweep: same index as its driver
                   for l, d in config.run.link in file order }
        choose = swept ∪ linked                                       # the values this point overrides
        server_args  = [ (k, choose.get("server."+k, v)) for k, v in config.server program params ]
        loadgen_args = [ (k, choose.get("load_gen."+k, v)) for k, v in config.load_gen program params ]
        if "initial_stall_ms" not in loadgen_args and "initial_stall_ms" in server_args:   # StallWindow
            loadgen_args.append(("initial_stall_ms", value of initial_stall_ms in server_args))
        ingress_args = [ (k, choose.get("ingress."+k, v)) for k, v in config.ingress program params ]
        ingress_args.append(("backends", backend_list(config)))                 # Backends
        ingress_args.append(("num_clients", value of num_clients in loadgen_args))
        if "duration_s" not in ingress_args:
            ingress_args.append(("duration_s", value of duration_s in loadgen_args))
        if config.ingress has cancel_rate_fraction:                             # CancelRateFraction
            f = choose.get("ingress.cancel_rate_fraction", config.ingress.cancel_rate_fraction)
            ingress_args.append(("cancel_rate", derived_cancel_rate(value of global_rate in loadgen_args, f)))
        freeze = none                                                           # FreezeTarget
        if config.server has freeze_instance:
            freeze = { "instance": config.server.freeze_instance,
                       "freeze_after_ms": value of freeze_after_ms in server_args }
            remove ("freeze_after_ms", ...) from server_args
        placement = { program: { "threads": config[section].threads or none,     # ProcessPlacement
                                 "cpu_set": config[section].cpu_set or none }
                      for (program, section) in [("server", "server"), ("loadgen", "load_gen"),
                                                 ("ingress", "ingress")] }
        points.append(SweepPoint(id, swept, linked, server_args, loadgen_args, ingress_args,
                                 len(config.machines.server) × config.server.instances,   # ServerMachines
                                 server_hosts(config), config.ingress.machine, loadgen_target(config),
                                 placement, freeze))
    return points          # with no swept keys: exactly one point, id 0
```

The load generator's StallWindow must match the servers' StallGate
([../../load_generator/client/design.md#stallwindow](../../load_generator/client/design.md#stallwindow)), so a point's
`server.initial_stall_ms` (swept or not) is copied to the load generator unless `load_gen.initial_stall_ms` is set.
If neither is set, both programs use their default, 1000 ms.

| `server.initial_stall_ms` | `load_gen.initial_stall_ms` | Load generator gets |
|---|---|---|
| unset | unset | nothing (its default, 1000) |
| `500` | unset | `500` |
| `[0, 1000]` (swept) | unset | `0` on the points with `0`, `1000` on the others |
| `500` | `200` | `200` |

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

## ServerMachines

The **ServerMachines** are the machines that run the server instances. `[machines.server]` is either one table (one
server machine, as before) or an array of tables (`[[machines.server]]`), one per server machine, in file order.
`config.machines.server` is always the list, of length `M >= 1`.

```
MAX_SERVER_MACHINES = 16

function validate_server_machines(value):
    if value is a table: return [validate_machine(server, value, "machines.server")]
    if value is not an array of tables, or has 0 or more than MAX_SERVER_MACHINES elements:
        ConfigError("machines.server must be a table or an array of 1..=16 tables")
    machines = [validate_machine(server, t, "machines.server[" + decimal(m) + "]") for m, t in enumerate(value)]
    for a < b: if (machines[a].host, .user, .ssh_port, .workdir) == (machines[b].host, .user, .ssh_port, .workdir):
        ConfigError("machines.server[<a>] and machines.server[<b>] are the same machine (use server.instances)")
    return machines

function instance_machine(config, id):          # id in 0 ..= M × instances - 1
    return id div config.server.instances         # integer division

function instance_port(config, id):
    return config.server.listen_port + id mod config.server.instances

function server_hosts(config):
    return [instance_machine(config, id) for id in 0 ..= len(config.machines.server) × config.server.instances - 1]
```

- **`instances` is per machine.** Every server machine runs `[server] instances` processes, so the system has
  `M × instances` instances. They are numbered machine by machine: instance `id` runs on server machine
  `id div instances` and listens on `listen_port + id mod instances` there. With one machine, `id = k` as before.
- **The id is global.** It is the Ingress's `server_id`, the `<k>` of every `server_<k>.*` file, and an index into
  `server_hosts` (recorded in the SweepPoint), so outputs and plots need not know which machine an instance ran on.
- **Different machines only.** Two tables naming the same machine would start instances on the same ports; more
  instances on one machine are asked for with `instances`. The server machines may still be the client machine
  (the same-machine rule of setup applies per pair, [../remote_setup/design.md#machine-setup](../remote_setup/design.md#machine-setup)).

| `[machines.server]` | `instances` | Instances (id → machine, port) | `server_hosts` |
|---|---|---|---|
| one table | 2 | 0 → 0, 8080; 1 → 0, 8081 | `[0, 0]` |
| two tables | 1 | 0 → 0, 8080; 1 → 1, 8080 | `[0, 1]` |
| two tables | 2 | 0 → 0, 8080; 1 → 0, 8081; 2 → 1, 8080; 3 → 1, 8081 | `[0, 0, 1, 1]` |

| Situation | ConfigError message |
|---|---|
| `machines.server = []`, or 17 tables, or a string | `machines.server must be a table or an array of 1..=16 tables` |
| second table with `user = ""` | `machines.server[1].user must be a non-empty string` |
| two tables with the same `host` and no `user`, `ssh_port`, `workdir` | `machines.server[0] and machines.server[1] are the same machine (use server.instances)` |

## Backends

```
function backend_list(config):
    ids = 0 ..= len(config.machines.server) × config.server.instances - 1
    if config.ingress.machine == "server":        # exactly one server machine (IngressPlacement)
        return ",".join("127.0.0.1:" + decimal(instance_port(config, id)) for id in ids)
    return ",".join(config.machines.server[instance_machine(config, id)].data_addr + ":" +
                    decimal(instance_port(config, id)) for id in ids)
```

`server_id` `id` is instance `id` ([ServerMachines](#servermachines)). An Ingress on the client machine reaches each
instance at its server machine's `data_addr`; an Ingress on the server machine reaches them locally
([IngressPlacement](#ingressplacement)).

| `ingress.machine` | Server machines' `data_addr` | `listen_port` | `instances` | `backends` |
|---|---|---|---|---|
| `client` | `172.31.23.63` | 8080 | 2 | `172.31.23.63:8080,172.31.23.63:8081` |
| `server` | `172.31.23.63` | 8080 | 2 | `127.0.0.1:8080,127.0.0.1:8081` |
| `client` | `127.0.0.1` | 8080 | 1 | `127.0.0.1:8080` |
| `client` | `172.31.30.174`, `172.31.21.15` | 8080 | 1 | `172.31.30.174:8080,172.31.21.15:8080` |

## IngressPlacement

The **IngressPlacement** (`[ingress] machine`) chooses which experiment machine runs the Ingress. It is fixed for
the whole run. It decides two addresses of every SweepPoint, and which machine the Experiment Executor starts the
Ingress on and collects its outputs from.

```
function loadgen_target(config):
    host = "127.0.0.1" if config.ingress.machine == "client" else config.machines.server[0].data_addr
    return host + ":" + decimal(config.ingress.listen_port)

function check_ingress_placement(config):
    if config.ingress.machine == "server" and len(config.machines.server) > 1:
        ConfigError("ingress.machine = \"server\" needs exactly one server machine")
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
| `machine = "server"` and more than one server machine | `ingress.machine = "server" needs exactly one server machine` |

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

## LinkedSweep

A **LinkedSweep** makes one array parameter follow a swept key instead of adding a sweep dimension. `run.link` maps
each **linked key** to its **driver key**. Both arrays have the same length, and at every point the linked key takes
the element at the index of the driver's chosen value. It is used when the right value of a parameter depends on
another swept parameter, for example a retry timeout computed by the operator for each offered rate.

```
function check_link(config):
    for linked, driver in config.run.link in file order:
        if linked or driver is not a string "<section>.<param>":
            ConfigError("run.link must map \"<section>.<param>\" keys to \"<section>.<param>\" keys")
        if linked is not a program parameter whose value is an array:
            ConfigError("run.link: <linked> must be an array program parameter")
        if driver is not a swept key:                 # an array key that is not itself linked
            ConfigError("run.link: driver <driver> of <linked> must be a swept key")
        if len(config.lookup(linked)) != len(config.lookup(driver)):
            ConfigError("run.link: <linked> and <driver> must have the same number of values")
```

- A linked key is not a swept key: it is left out of the cross product, may not appear in `sweep_order` (the
  permutation check fails), and is not in `SweepPoint.swept`. Its value per point is in `SweepPoint.linked` and in
  its program's argument list at its usual (file) position.
- A linked key cannot drive another key, because a driver must be a swept key. One driver can drive several linked
  keys.
- Pairing is by **index**, not by value: if the driver's array repeats a value, each occurrence keeps its own linked
  value.
- The plots use only `swept`, so a linked key never adds a series
  ([../result_plotter/design.md#sweepplots](../result_plotter/design.md#sweepplots)).

Worked example: `[load_gen] global_rate = [60, 70]`, `retry_timeout_ms = [183.258, 240.795]`, `max_retries = [0, 1]`,
`[run] link = { "load_gen.retry_timeout_ms" = "load_gen.global_rate" }`, no `sweep_order`. The swept keys are
`load_gen.global_rate`, `load_gen.max_retries`:

| id | `swept` | `linked` | Check |
|---|---|---|---|
| 0 | rate 60, retries 0 | timeout 183.258 | rate index 0 → timeout index 0 |
| 1 | rate 60, retries 1 | timeout 183.258 | rate index 0 |
| 2 | rate 70, retries 0 | timeout 240.795 | rate index 1 |
| 3 | rate 70, retries 1 | timeout 240.795 | rate index 1 |

Four points (2 × 2), not eight. The load generator's arguments keep file order: `global_rate`, …, `retry_timeout_ms`,
`max_retries`.

Edge and error cases:

| `run.link` / values | Result |
|---|---|
| unset, or `{}` | No linked keys: the expansion is the plain cross product |
| driver `global_rate = [60, 60]`, linked `[100, 200]` | Two points; the first gets 100, the second 200 |
| linked `retry_timeout_ms = 150` (scalar) | ConfigError `run.link: load_gen.retry_timeout_ms must be an array program parameter` |
| driver `global_rate = 100` (scalar) | ConfigError `run.link: driver load_gen.global_rate of load_gen.retry_timeout_ms must be a swept key` |
| lengths 4 and 3 | ConfigError `run.link: load_gen.retry_timeout_ms and load_gen.global_rate must have the same number of values` |
| linked key listed in `sweep_order` | ConfigError `run.sweep_order must list every swept key exactly once` |
| `link = { "load_gen.retry_timeout_ms" = 5 }` | ConfigError `run.link must map "<section>.<param>" keys to "<section>.<param>" keys` |

## FreezeTarget

The server's WorkerFreeze ([../../server/worker_pool/design.md#workerfreeze](../../server/worker_pool/design.md#workerfreeze))
is an injected failure, and an experiment usually wants it in **one** server instance while the others keep working.
The runner-owned key `[server] freeze_instance` names that instance, the **FreezeTarget**. `freeze_after_ms` then
goes to that instance only, instead of to every instance like other server parameters.

```
function check_freeze_target(config):
    if "freeze_instance" not in config.server: return
    total = len(config.machines.server) × config.server.instances           # ServerMachines
    i = config.server.freeze_instance
    i is an integer and 0 <= i <= total - 1
        else ConfigError("server.freeze_instance must be an integer in 0..=<total - 1>")
    "freeze_after_ms" in config.server
        else ConfigError("server.freeze_instance needs server.freeze_after_ms")
```

- `freeze_instance` cannot be swept (the generic runner-key rule). `freeze_after_ms` is a program parameter and can
  be swept; each point's value goes to `SweepPoint.freeze`.
- Without `freeze_instance`, `freeze_after_ms` is an ordinary server parameter: it stays in `server_args`, and every
  instance freezes.
- Instance ids are the global ids of [ServerMachines](#servermachines), so the target picks a machine too.

| `[server]` keys | `server_args` has `freeze_after_ms` | `SweepPoint.freeze` |
|---|---|---|
| neither | no | none |
| `freeze_after_ms = 60000` | yes, every instance freezes | none |
| `freeze_after_ms = 60000`, `freeze_instance = 0` | no | `{instance: 0, freeze_after_ms: 60000}` |
| `freeze_after_ms = [30000, 60000]`, `freeze_instance = 1` (swept) | no | `{instance: 1, freeze_after_ms: 30000}` or `60000`, per point |
| `freeze_instance = 2` with 2 server machines × 1 instance | — | ConfigError `server.freeze_instance must be an integer in 0..=1` |
| `freeze_instance = 0` only | — | ConfigError `server.freeze_instance needs server.freeze_after_ms` |
| `freeze_instance = [0, 1]` | — | ConfigError `server.freeze_instance cannot be swept` |

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
