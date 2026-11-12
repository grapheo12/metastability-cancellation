# Experiment Executor — Design

Remote paths per point: `RD = <workdir>/runs/<run_id>/point_<id, 4 digits>` on each machine. `run_id` is the last
path component of the output directory. Pid files live in each machine's workdir: `server_<k>.pid` for server
instance `k` on the server machine, `ingress.pid` on the Ingress's machine. `I = point.server_instances`.
`ingress_host = server if point.ingress_machine == "server" else client` is the machine that runs the Ingress
([../config/design.md#ingressplacement](../config/design.md#ingressplacement)).

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
        for m in [server, client]: stop_ingress(m, config.run.stop_timeout_s)   # leftovers, either placement
        stop_servers(server, MAX_INSTANCES, config.run.stop_timeout_s)
        start_servers(point, config, server)                             # ServerLifecycle
        start_ingress(point, config, ingress_host)                       # IngressLifecycle
        ok = client_run(point, config, client)                           # ClientRun
        if not ok: reason = "client run failed or timed out"
    except failure f: reason = f
    finally:                                                             # reverse start order
        if stop_ingress(ingress_host, config.run.stop_timeout_s) is FAILED and reason is none:
            reason = "ingress did not stop on SIGINT"
        if stop_servers(server, I, config.run.stop_timeout_s) is FAILED and reason is none:
            reason = "server did not stop on SIGINT"
        collect_outputs(point, config, server, client, ingress_host, pdir)   # always, best effort
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
        args  = ["--listen_addr", "0.0.0.0:" + (config.server.listen_port + k),
                 "--queue_log_path", RD_server + "/server_" + k + ".queue.csv"]        # QueueLengthLog
                + args(point.server_args)
        words = placement_prefix("server", point, server) + gc_trace_prefix(config, server)    # GcEvents
                + [server.binaries.server] + args
        start_program(server, words, RD_server + "/server_" + k + ".out",
                      <server workdir>/server_<k>.pid, config.run.server_ready_timeout_s)

function stop_servers(server, count, stop_timeout_s):
    outcome = ok
    for k in 0 ..= count-1:
        if stop_program(server, <server workdir>/server_<k>.pid, stop_timeout_s) is FAILED: outcome = FAILED
    return outcome
```

Every instance always writes its QueueLengthLog
([../../server/api.md#queuelengthlog-output](../../server/api.md#queuelengthlog-output)) to its own file in the
point's remote directory; `queue_log_path` is runner-owned, so the config cannot change it.

All instances get the same program parameters, including `seed`. Each one's per-connection ServiceTime streams
therefore start from the same seeds; the requests each receives differ, so their samples are not correlated in any
way that matters for the measurements.

## IngressLifecycle

```
function start_ingress(point, config, m):                        # m = ingress_host
    args  = ["--listen_addr", "0.0.0.0:" + config.ingress.listen_port] + args(point.ingress_args)
            + ["--output_path", RD_m + "/ingress"]
    words = placement_prefix("ingress", point, m) + [m.binaries.ingress] + args
    start_program(m, words, RD_m + "/ingress.log", <m workdir>/ingress.pid, config.run.server_ready_timeout_s)

function stop_ingress(m, stop_timeout_s):
    return stop_program(m, <m workdir>/ingress.pid, stop_timeout_s)
```

The Ingress dials no backend until the first `/ping` arrives, so it can be started before the servers are known to
be reachable from its machine; a backend it cannot reach shows up as `502`s and `bad_gateway` counts, and the load
generator's `error` outcomes. Likewise, if the client machine cannot reach an Ingress placed on the server machine,
the load generator fails to connect and the point fails.

## ClientRun

Only the load generator runs as a foreground client program. It runs in the background of one shell on the client
machine with its pid written to a file, so a timed-out run can be cleaned up. It sends to `point.loadgen_target`,
the Ingress as seen from the client machine:

```
function client_run(point, config, client):
    target = point.loadgen_target                              # 127.0.0.1:<port> or <data_addr>:<port>
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

function write_placement(point, server, client, ingress_host, pdir):
    doc = { "server":  placement_prefix("server", point, server),       # the same for every instance
            "loadgen": placement_prefix("loadgen", point, client),
            "ingress": placement_prefix("ingress", point, ingress_host) }
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

## GcEvents

With `[server] gc_trace = true`, every server instance runs with the BuildManifest's GcTraceHook switched on, and its
GcTrace lines, which land in `server_<k>.out` with the rest of its output, are turned into **GcEvents** on the wall
clock. The run flow has already checked that the hook exists
([../design.md#run-flow](../design.md#run-flow)).

```
function gc_trace_prefix(config, server):                   # -> words
    if not config.server.gc_trace: return []
    return ["env"] + server.gc_trace_env           # after any ProcessPlacement prefix; env commands chain

function write_gc_events(point, k, pdir, regex):
    out = pdir/server_<k>.out
    if out does not exist: print("point <id>: server_<k>.out missing (no GcEvents)"); return
    start = none; events = []
    for each line of out, in order:
        if start is none and the line is "process_start_unix_ns " + decimal digits:
            start = that integer                                          # the server's ProcessStart line
        else if regex matches at the start of the line:
            events.append(m)
    if start is none: print("point <id>: server_<k>.out has no process_start_unix_ns line (no GcEvents)"); return
    if events is empty: print("point <id>: server_<k>.out has no GcTrace lines"); 
    write pdir/server_<k>.gc.csv:
        header "t_unix_ns,cycle,stw_start_ms,mark_ms,stw_end_ms"
        one row per event, in file order:
            t_unix_ns = start + seconds_to_ns(m.start_s), then m.cycle, m.stw_start_ms, m.mark_ms, m.stw_end_ms
            (the three durations copied exactly as written in the line)

function seconds_to_ns(s):                                  # s: decimal string "I" or "I.F"; exact, no float
    (i, f) = split s at "."; f = "" if absent
    f = (f + "000000000")[0 ..= 8]                          # keep 9 digits, truncating any further ones
    return decimal(i) * 1_000_000_000 + decimal(f)
```

- **Shared clock.** `start_s` counts from the server's process start. The ProcessStart line gives that start on
  the wall clock, so `t_unix_ns` is a wall-clock time and can be compared with the Ingress's `run_start_unix_ns`
  ([../../ingress/api.md#output_pathsummaryjson](../../ingress/api.md#output_pathsummaryjson)). Both are read on the
  same machine when the Ingress runs there; across machines the difference also contains their clock offset.
- **Never fails the point.** A missing start line, no GcTrace lines, or a missing `.out` file only prints one
  warning; the plots then have no GC overlay for that instance. The file is written even with no events (header
  only), so "no GC ran" and "no trace" can be told apart.
- **Exact arithmetic.** `seconds_to_ns` works on the decimal string, so `@4.507s` gives exactly `4_507_000_000` ns.

Worked example, ProcessStart `1700000000000000000`:

| Line | `start_s` | `t_unix_ns` | Check |
|---|---|---|---|
| `gc 1 @0.012s 1%: 0.018+5.1+0.042 ms clock, …` | `0.012` | 1_700_000_000_012_000_000 | + 12_000_000 |
| `gc 2 @4.507s 3%: 0.020+12+0.050 ms clock, …` | `4.507` | 1_700_000_004_507_000_000 | + 4_507_000_000 |
| `gc 3 @10s 2%: 0.01+1+0.02 ms clock, …` | `10` | 1_700_000_010_000_000_000 | no fraction |
| `listening on 0.0.0.0:8080` | — | not an event | does not match |

## Point outputs

```
function collect_outputs(point, config, server, client, ingress_host, pdir):
    for name in [loadgen.requests.csv, loadgen.summary.json, loadgen.log]:
        fetch(client.transport, RD_client/name, pdir/name)
    for name in [ingress.cancels.csv, ingress.forwards.csv, ingress.summary.json, ingress.log]:
        fetch(ingress_host.transport, RD_ingress_host/name, pdir/name)
    if pdir/ingress.forwards.csv does not exist:                 # ForwardLog
        print("point <id>: ingress.forwards.csv missing (left out of the forward timeline plots)")
    counters = []
    for k in 0 ..= I-1:
        fetch(server.transport, RD/server_<k>.out, pdir/server_<k>.out)
        fetch(server.transport, RD/server_<k>.queue.csv, pdir/server_<k>.queue.csv)     # QueueLengthLog
        if pdir/server_<k>.queue.csv does not exist:
            print("point <id>: server_<k>.queue.csv missing (left out of queue_length)")
        if config.server.gc_trace: write_gc_events(point, k, pdir, server.gc_trace_regex)   # GcEvents
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
remain for inspection. The QueueLengthLog files are not required: a missing one prints one warning line and
never fails the point; the Result Plotter leaves that point out of the `queue_length` figure. The same holds for
`ingress.forwards.csv` and the forward timeline plots. Every fetch is best effort (`missing` is fine), so a failed point still keeps whatever
evidence exists.

| Instances | Counters (before, during, completed) | `server_counters.json` |
|---|---|---|
| 2 | (10, 1, 5) and (7, 0, 4) | (17, 1, 9) |
| 2 | (10, 1, 5) and missing | not written; point fails |

## Design specific to Python

Shared tool `implementation/scripts/` (tooling language Python 3.11+; see `AGENTS.md` "Shared tooling"). Identical on
every `impl/<lang>` branch. Plotting needs `plotly` (`python3 -m pip install -r implementation/scripts/requirements.txt`).
Tests: `python3 -B -m unittest discover -s implementation/scripts/tests -t implementation/scripts`.

### Python choices

- `run_point` uses `try / except / finally`. `finally` always stops the Ingress, then the servers, and collects outputs. `KeyboardInterrupt` is recorded as reason `interrupted` and re-raised after the `failed` marker is written.
- Waiting uses `time.monotonic()` deadlines and `time.sleep(POLL_INTERVAL_S)`.
- `params.json`, `placement.json` and the `server_counters*.json` files are written with `json.dump(..., indent=2)`. `placement.json` is written right after `params.json`, before any remote command, so even a point that fails early records its placement.
- `ingress_host` is chosen once in `run_point` from `point.ingress_machine`; points read from older `params.json` files without it default to the client machine. `collect_outputs` takes the Ingress's machine and remote directory as extra arguments.
- GcEvents: `gc_trace_prefix(config, server)` adds `env` plus `MachineReady.gc_trace_env` after the ProcessPlacement prefix in `start_servers`. `collect_outputs` takes `gc_trace=` (from `BenchConfig.gc_trace`) and calls `write_gc_events`, which reads `server_<k>.out` line by line, takes the first `process_start_unix_ns` line (`re.fullmatch`) and every line the hook's regex matches with `re.match` (anchored at the start), and writes the CSV with the three durations copied as matched. `seconds_to_ns` uses `str.partition` and integer arithmetic only. Tests: `GcEventsTest` (worked example, warnings, prefix) and the server-start test.
- `collect_outputs` fetches `ingress.forwards.csv` in the same loop as the other Ingress outputs and prints the missing-file warning right after it; `tests/test_executor.py` checks the warning and the fetch.
- BackgroundProgram is `start_program(m, name, words, log, pid_file, ready_timeout_s)` and `stop_program(m, pid_file, stop_timeout_s)`; `start_servers`, `stop_servers`, `start_ingress` and `stop_ingress` are thin wrappers.
- Leftover cleanup (`stop_leftover_servers`) lists the existing `server_*.pid` files with one `ls` instead of probing all `MAX_INSTANCES` paths, which would cost one SSH round trip each; only indices below `MAX_INSTANCES` are stopped, as specified.
- - `placement_prefix` returns plain words. They go through the same `join` (POSIX quoting) as the rest of the command line, so `GOMAXPROCS=4` and `0-3` are passed unquoted and unchanged.

### Traceability

Generated from the `# Design:` tags in the code by `implementation/spec_tools/tracegen.py` (see `AGENTS.md`). Each link points at the first code line after the tag.
Regenerate from the repository root with `python3 implementation/spec_tools/tracegen.py --lang Python --impl implementation/scripts --ext .py --comment '#' --write`.

<!-- trace:begin -->
| Design subsection | Implementation (file:line) |
|---|---|
| [run_point](#run_point) | [executor.py:48](../../../implementation/scripts/benchrunner/executor.py#L48), [executor.py:167](../../../implementation/scripts/benchrunner/executor.py#L167) |
| [BackgroundProgram](#backgroundprogram) | [executor.py:102](../../../implementation/scripts/benchrunner/executor.py#L102), [executor.py:120](../../../implementation/scripts/benchrunner/executor.py#L120) |
| [ServerLifecycle](#serverlifecycle) | [executor.py:148](../../../implementation/scripts/benchrunner/executor.py#L148), [executor.py:159](../../../implementation/scripts/benchrunner/executor.py#L159), [test_executor.py:218](../../../implementation/scripts/tests/test_executor.py#L218) |
| [IngressLifecycle](#ingresslifecycle) | [executor.py:57](../../../implementation/scripts/benchrunner/executor.py#L57), [executor.py:178](../../../implementation/scripts/benchrunner/executor.py#L178), [executor.py:187](../../../implementation/scripts/benchrunner/executor.py#L187) |
| [ClientRun](#clientrun) | [executor.py:192](../../../implementation/scripts/benchrunner/executor.py#L192), [test_executor.py:59](../../../implementation/scripts/tests/test_executor.py#L59) |
| [ProcessPlacement](#processplacement) | [executor.py:212](../../../implementation/scripts/benchrunner/executor.py#L212), [executor.py:223](../../../implementation/scripts/benchrunner/executor.py#L223), [test_executor.py:29](../../../implementation/scripts/tests/test_executor.py#L29), [test_executor.py:39](../../../implementation/scripts/tests/test_executor.py#L39) |
| [GcEvents](#gcevents) | [executor.py:232](../../../implementation/scripts/benchrunner/executor.py#L232), [executor.py:240](../../../implementation/scripts/benchrunner/executor.py#L240), [executor.py:251](../../../implementation/scripts/benchrunner/executor.py#L251), [test_executor.py:160](../../../implementation/scripts/tests/test_executor.py#L160), [test_executor.py:168](../../../implementation/scripts/tests/test_executor.py#L168), [test_executor.py:187](../../../implementation/scripts/tests/test_executor.py#L187), [test_executor.py:207](../../../implementation/scripts/tests/test_executor.py#L207) |
| [Point outputs](#point-outputs) | [executor.py:285](../../../implementation/scripts/benchrunner/executor.py#L285), [executor.py:331](../../../implementation/scripts/benchrunner/executor.py#L331), [test_executor.py:85](../../../implementation/scripts/tests/test_executor.py#L85), [test_executor.py:108](../../../implementation/scripts/tests/test_executor.py#L108), [test_executor.py:133](../../../implementation/scripts/tests/test_executor.py#L133) |
<!-- trace:end -->
