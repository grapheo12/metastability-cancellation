# Config — Design

## BenchConfig

```
PROGRAM_PARAMS = {
  "server":    ["response_rate", "response_bytes", "cancel_mode", "cancel_check_interval_us", "seed"],
  "load_gen":  ["num_clients", "global_rate", "request_bytes", "slo_ms", "duration_s", "seed"],
  "canceller": ["cancel_rate", "duration_s", "seed"],
}
RUNNER_KEYS = { "server": ["listen_port"], "load_gen": [], "canceller": ["enabled"] }
FORBIDDEN   = { "server": ["listen_addr"], "load_gen": ["target", "output_path"],
                "canceller": ["target", "num_clients", "output_path"] }

function load_config(path):
    doc = parse_toml(path)                                  # ConfigError on syntax error
    allowed tables: repo, machines (with exactly server, client), server, load_gen, canceller, run
    repo_url = doc.repo.url if present else git_origin_url_of_operator_repo()
    if repo_url is none: ConfigError("[repo] url is required (no origin remote)")
    for m in [server, client]: validate machine table (types, ranges, required keys, defaults)
    for section in [server, load_gen, canceller]:
        for key, value in doc[section] in file order:
            if key in FORBIDDEN[section]: ConfigError("<section>.<key> is set by the runner")
            if key in RUNNER_KEYS[section]:
                if value is an array: ConfigError("<section>.<key> cannot be swept")
                check type and range
            else if key in PROGRAM_PARAMS[section]:
                if value is an array: require non-empty, every element a scalar
                else: require a scalar
            else: ConfigError("unknown key <section>.<key>")
    for key in ["num_clients", "duration_s", "slo_ms"]:          # the runner itself needs these
        if key not in doc.load_gen: ConfigError("load_gen.<key> is required")
    validate run table; fill defaults
    if sweep_order given: require it to be a permutation of the swept keys
    return BenchConfig
```

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
        else:
            canceller_args = none
        points.append(SweepPoint(id, ordered(choose by swept_keys), server_args, loadgen_args, canceller_args))
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
