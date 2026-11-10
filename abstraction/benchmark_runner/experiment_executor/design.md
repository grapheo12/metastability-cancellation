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
        words = placement_prefix("server", point, server) + [server.binaries.server] + args
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

## Point outputs

```
function collect_outputs(point, config, server, client, ingress_host, pdir):
    for name in [loadgen.requests.csv, loadgen.summary.json, loadgen.log]:
        fetch(client.transport, RD_client/name, pdir/name)
    for name in [ingress.cancels.csv, ingress.summary.json, ingress.log]:
        fetch(ingress_host.transport, RD_ingress_host/name, pdir/name)
    counters = []
    for k in 0 ..= I-1:
        fetch(server.transport, RD/server_<k>.out, pdir/server_<k>.out)
        fetch(server.transport, RD/server_<k>.queue.csv, pdir/server_<k>.queue.csv)     # QueueLengthLog
        if pdir/server_<k>.queue.csv does not exist:
            print("point <id>: server_<k>.queue.csv missing (left out of queue_length.png)")
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
never fails the point; the Result Plotter leaves that point out of `queue_length.png`. Every fetch is best effort (`missing` is fine), so a failed point still keeps whatever
evidence exists.

| Instances | Counters (before, during, completed) | `server_counters.json` |
|---|---|---|
| 2 | (10, 1, 5) and (7, 0, 4) | (17, 1, 9) |
| 2 | (10, 1, 5) and missing | not written; point fails |
