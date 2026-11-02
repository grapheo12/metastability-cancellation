# Experiment Executor — Design

Remote paths per point: `RD = <workdir>/runs/<run_id>/point_<id, 4 digits>` on each machine. `run_id` is the last
path component of the output directory. Pid files live in each machine's workdir: `server_<k>.pid` for server
instance `k` on the server machine, `ingress.pid` on the client machine. `I = point.server_instances`.

## run_point

```
function run_point(point, config, server, client, run_id):
    pdir = <output_dir>/points/point_<id>
    if exists(pdir/done): return SKIPPED
    empty pdir (delete any previous partial attempt), then write pdir/params.json (the SweepPoint)
    write pdir/placement.json (see ProcessPlacement)
    reason = none
    try:
        for m in [server, client]: run(m.transport, "mkdir -p " + quote(RD_m))
        stop_ingress(client, config.run.stop_timeout_s)                  # leftovers from a crash
        stop_servers(server, MAX_INSTANCES, config.run.stop_timeout_s)
        start_servers(point, config, server)                             # ServerLifecycle
        start_ingress(point, config, client)                             # IngressLifecycle
        ok = client_run(point, config, client)                           # ClientRun
        if not ok: reason = "client run failed or timed out"
    except failure f: reason = f
    finally:                                                             # reverse start order
        if stop_ingress(client, config.run.stop_timeout_s) is FAILED and reason is none:
            reason = "ingress did not stop on SIGINT"
        if stop_servers(server, I, config.run.stop_timeout_s) is FAILED and reason is none:
            reason = "server did not stop on SIGINT"
        collect_outputs(point, config, server, client, pdir)             # always, best effort
    if reason is none: reason = validate_outputs(point, pdir)             # see Point outputs
    if reason is none: write pdir/done (empty); return DONE
    write pdir/failed containing reason; return FAILED(reason)
```

The Ingress is stopped before the servers, so its last forwards and its LoadCanceller's last `/cancel` responses
finish while the backends are still up, and every server prints its CancelCounters after all `/cancel`s it will
ever get. Leftover cleanup stops up to `MAX_INSTANCES` server pid files, so a crash with more instances than the
current point is also cleaned up.

## BackgroundProgram

The servers and the Ingress share one start/stop procedure. Both print `READY_LINE` once they accept connections and
exit `0` on SIGINT after printing or writing their outputs.

```
function start_program(m, words, log, pid_file, ready_timeout_s):
    pid = start_background(m.transport, "exec " + join(quote(w) for w in words), log)
    run(m.transport, "echo " + pid + " > " + quote(pid_file))
    deadline = now + ready_timeout_s
    loop:
        if run(m.transport, "grep -q " + quote(READY_LINE) + " " + quote(log)).exit_status == 0: return
        if not alive(m.transport, pid): fail("<program> exited during startup: " + last 20 lines of log)
        if now >= deadline: fail("<program> not ready after <timeout> s")
        sleep(POLL_INTERVAL_S)

function stop_program(m, pid_file, stop_timeout_s):
    r = run(m.transport, "cat " + quote(pid_file))
    if r.exit_status != 0: return ok                                  # nothing recorded
    pid = r.stdout.strip()
    if alive(m.transport, pid):
        signal(m.transport, pid, "INT")
        wait until not alive(m.transport, pid), polling every POLL_INTERVAL_S, at most stop_timeout_s
        if still alive: signal(m.transport, pid, "KILL"); outcome = FAILED
    run(m.transport, "rm -f " + quote(pid_file))
    return outcome (ok unless KILL was needed)
```

`exec` makes the background pid the program itself, so SIGINT reaches it
([../remote_transport/design.md#background-processes](../remote_transport/design.md#background-processes)). The
placement prefix keeps this true, because `env` and `taskset` both replace themselves with the program they run
(see [ProcessPlacement](#processplacement)).

## ServerLifecycle

```
function start_servers(point, config, server):
    for k in 0 ..= I-1:                                                   # in order; each waits for readiness
        args  = ["--listen_addr", "0.0.0.0:" + (config.server.listen_port + k)] + args(point.server_args)
        words = placement_prefix("server", point, server) + [server.binaries.server] + args
        start_program(server, words, RD_server + "/server_" + k + ".out",
                      <server workdir>/server_<k>.pid, config.run.server_ready_timeout_s)

function stop_servers(server, count, stop_timeout_s):
    outcome = ok
    for k in 0 ..= count-1:
        if stop_program(server, <server workdir>/server_<k>.pid, stop_timeout_s) is FAILED: outcome = FAILED
    return outcome
```

All instances get the same program parameters, including `seed`. Each one's per-connection ServiceTime streams
therefore start from the same seeds; the requests each receives differ, so their samples are not correlated in any
way that matters for the measurements.

## IngressLifecycle

```
function start_ingress(point, config, client):
    args  = ["--listen_addr", "0.0.0.0:" + config.ingress.listen_port] + args(point.ingress_args)
            + ["--output_path", RD_client + "/ingress"]
    words = placement_prefix("ingress", point, client) + [client.binaries.ingress] + args
    start_program(client, words, RD_client + "/ingress.log", <client workdir>/ingress.pid,
                  config.run.server_ready_timeout_s)

function stop_ingress(client, stop_timeout_s):
    return stop_program(client, <client workdir>/ingress.pid, stop_timeout_s)
```

The Ingress dials no backend until the first `/ping` arrives, so it can be started before the servers are known to
be reachable from the client machine; a backend it cannot reach shows up as `502`s and `bad_gateway` counts, and the
load generator's `error` outcomes.

## ClientRun

Only the load generator runs as a foreground client program now. It runs in the background of one shell on the
client machine with its pid written to a file, so a timed-out run can be cleaned up:

```
function client_run(point, config, client):
    target = "127.0.0.1:" + config.ingress.listen_port
    lg = placement_prefix("loadgen", point, client) +
         [client.binaries.loadgen, "--target", target] + args(point.loadgen_args) + ["--output_path", RD + "/loadgen"]
    script lines:
        cd RD || exit 97
        <lg words> > RD/loadgen.log 2>&1 &
        LP=$!; echo $LP > RD/loadgen.pid
        wait $LP; L=$?
        echo "loadgen_exit=$L"
        [ "$L" -eq 0 ]
    timeout = lg duration_s + lg slo_ms / 1000 + config.run.client_timeout_margin_s
    r = run(client.transport, script joined with newlines, timeout)
    if r.exit_status == -1:                                   # timed out: kill what is left
        run(client.transport, "[ -f RD/loadgen.pid ] && kill -s KILL $(cat RD/loadgen.pid); true")
    return r.exit_status == 0
```

The LoadCanceller's schedule starts at the Ingress's first `/ping`, so it lines up with the load generator
without any coordination ([../../ingress/design.md#time-base](../../ingress/design.md#time-base)). Its sending phase
ends `duration_s` after that, and the Ingress is stopped only after the load generator has exited.

## ProcessPlacement

Each program starts behind a prefix of words built from its ProcessPlacement
([../config/design.md#processplacement](../config/design.md#processplacement)) and the facts its machine reported at
setup ([../remote_setup/api.md#machineready](../remote_setup/api.md#machineready)):

```
function placement_prefix(program, point, ready):          # program: "server" | "loadgen" | "ingress"
    p = point.placement[program]
    words = []
    if p.threads is not none:
        words += ["env", ready.thread_env + "=" + decimal(p.threads)]   # thread_env is set: the run flow checked it
    if p.cpu_set is not none and ready.cpu_pinning:
        words += ["taskset", "-c", p.cpu_set]
    return words                                            # empty when nothing is set

function write_placement(point, server, client, pdir):
    doc = { "server":  placement_prefix("server", point, server),       # the same for every instance
            "loadgen": placement_prefix("loadgen", point, client),
            "ingress": placement_prefix("ingress", point, client) }
    write pdir/placement.json = doc                         # the prefixes actually used, as word lists
```

`env` comes before `taskset`, so the thread count is set and then the CPUs are pinned. Both programs `exec` the
rest of the line. So the pid of a backgrounded or `exec`ed line is the pid of the program itself, and signals and
`wait` behave exactly as without a prefix.

| `threads` | `cpu_set` | `cpu_pinning` | Prefix (with `thread_env = GOMAXPROCS`) |
|---|---|---|---|
| none | none | any | (empty) |
| 4 | none | any | `env GOMAXPROCS=4` |
| none | `"4-7"` | true | `taskset -c 4-7` |
| 4 | `"0-3"` | true | `env GOMAXPROCS=4 taskset -c 0-3` |
| 1 | `"8"` | false | `env GOMAXPROCS=1` (pinning dropped; the run flow has already warned) |

## Point outputs

```
function collect_outputs(point, config, server, client, pdir):
    for name in [loadgen.requests.csv, loadgen.summary.json, loadgen.log,
                 ingress.cancels.csv, ingress.summary.json, ingress.log]:
        fetch(client.transport, RD/name, pdir/name)
    counters = []
    for k in 0 ..= I-1:
        fetch(server.transport, RD/server_<k>.out, pdir/server_<k>.out)
        line = last line of pdir/server_<k>.out that starts with COUNTERS_PREFIX
        if line exists:
            c = line parsed as JSON (the three CancelCounters keys)
            write pdir/server_counters_<k>.json = c; counters.append(c)
    if len(counters) == I:                                   # every instance reported
        write pdir/server_counters.json = { key: sum of c[key] over counters, for each of the three keys }

function validate_outputs(point, pdir):
    require pdir/loadgen.summary.json            else "loadgen summary missing"
    require pdir/ingress.summary.json            else "ingress summary missing"
    require pdir/server_counters.json            else "server CancelCounters line missing (instance <k>)"
    return none
```

`server_counters.json` keeps its meaning (the system's CancelCounters) by summing the instances; per-instance files
remain for inspection. Every fetch is best effort (`missing` is fine), so a failed point still keeps whatever
evidence exists.

| Instances | Counters (before, during, completed) | `server_counters.json` |
|---|---|---|
| 2 | (10, 1, 5) and (7, 0, 4) | (17, 1, 9) |
| 2 | (10, 1, 5) and missing | not written; point fails |

## Design specific to Python

Shared tool `implementation/scripts/` (tooling language Python 3.11+; see `AGENTS.md` "Shared tooling"). Identical on
every `impl/<lang>` branch. Plotting needs `matplotlib` (`python3 -m pip install -r implementation/scripts/requirements.txt`).
Tests: `python3 -B -m unittest discover -s implementation/scripts/tests -t implementation/scripts`.

### Python choices

- `run_point` uses `try / except / finally`. `finally` always stops the server and collects outputs. `KeyboardInterrupt` is recorded as reason `interrupted` and re-raised after the `failed` marker is written.
- Waiting uses `time.monotonic()` deadlines and `time.sleep(POLL_INTERVAL_S)`.
- `params.json`, `placement.json` and `server_counters.json` are written with `json.dump(..., indent=2)`. `placement.json` is written right after `params.json`, before any remote command, so even a point that fails early records its placement.
- `placement_prefix` returns plain words. They go through the same `join` (POSIX quoting) as the rest of the command line, so `GOMAXPROCS=4` and `0-3` are passed unquoted and unchanged.

### Traceability

Generated from the `# Design:` tags in the code by `implementation/spec_tools/tracegen.py` (see `AGENTS.md`). Each link points at the first code line after the tag.
Regenerate from the repository root with `python3 implementation/spec_tools/tracegen.py --lang Python --impl implementation/scripts --ext .py --comment '#' --write`.

<!-- trace:begin -->
| Design subsection | Implementation (file:line) |
|---|---|
| [run_point](#run_point) | [executor.py:47](../../../implementation/scripts/benchrunner/executor.py#L47) |
| [BackgroundProgram](#backgroundprogram) |  |
| [ServerLifecycle](#serverlifecycle) | [executor.py:95](../../../implementation/scripts/benchrunner/executor.py#L95), [executor.py:115](../../../implementation/scripts/benchrunner/executor.py#L115) |
| [IngressLifecycle](#ingresslifecycle) |  |
| [ClientRun](#clientrun) | [executor.py:135](../../../implementation/scripts/benchrunner/executor.py#L135), [test_executor.py:60](../../../implementation/scripts/tests/test_executor.py#L60) |
| [ProcessPlacement](#processplacement) | [executor.py:165](../../../implementation/scripts/benchrunner/executor.py#L165), [executor.py:176](../../../implementation/scripts/benchrunner/executor.py#L176), [test_executor.py:27](../../../implementation/scripts/tests/test_executor.py#L27), [test_executor.py:37](../../../implementation/scripts/tests/test_executor.py#L37) |
| [Point outputs](#point-outputs) | [executor.py:185](../../../implementation/scripts/benchrunner/executor.py#L185), [executor.py:206](../../../implementation/scripts/benchrunner/executor.py#L206) |
<!-- trace:end -->
