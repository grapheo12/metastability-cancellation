# Experiment Executor — Design

Remote paths per point: `RD = <workdir>/runs/<run_id>/point_<id, 4 digits>` on each machine. `run_id` is the last
path component of the output directory. The server pid file is `<server workdir>/server.pid`.

## run_point

```
function run_point(point, config, server, client, run_id):
    pdir = <output_dir>/points/point_<id>
    if exists(pdir/done): return SKIPPED
    empty pdir (delete any previous partial attempt), then write pdir/params.json (the SweepPoint)
    reason = none
    try:
        for m in [server, client]: run(m.transport, "mkdir -p " + quote(RD_m))
        stop_server(server, config.run.stop_timeout_s)                  # leftover from a crash
        start_server(point, config, server)                             # ServerLifecycle
        ok = client_run(point, config, server, client)                  # ClientRun
        if not ok: reason = "client run failed or timed out"
    except failure f: reason = f
    finally:
        if stop_server(server, config.run.stop_timeout_s) is FAILED and reason is none:
            reason = "server did not stop on SIGINT"
        collect_outputs(point, config, server, client, pdir)            # always, best effort
    if reason is none: reason = validate_outputs(point, pdir)            # see Point outputs
    if reason is none: write pdir/done (empty); return DONE
    write pdir/failed containing reason; return FAILED(reason)
```

## ServerLifecycle

```
function start_server(point, config, server):
    log  = RD_server + "/server.out"
    args = ["--listen_addr", "0.0.0.0:" + listen_port] + flatten([("--" + k, format_arg(v)) for k, v in point.server_args])
    pid  = start_background(server.transport, "exec " + join(quote(w) for w in [server.binaries.server] + args), log)
    run(server.transport, "echo " + pid + " > " + quote(<workdir>/server.pid))
    deadline = now + config.run.server_ready_timeout_s
    loop:
        if run(server.transport, "grep -q " + quote(READY_LINE) + " " + quote(log)).exit_status == 0: return
        if not alive(server.transport, pid): fail("server exited during startup: " + last 20 lines of log)
        if now >= deadline: fail("server not ready after <timeout> s")
        sleep(POLL_INTERVAL_S)

function stop_server(server, stop_timeout_s):
    r = run(server.transport, "cat " + quote(<workdir>/server.pid))
    if r.exit_status != 0: return ok                                  # no server recorded
    pid = r.stdout.strip()
    if alive(server.transport, pid):
        signal(server.transport, pid, "INT")                                  # server prints CancelCounters and exits 0
        wait until not alive(server.transport, pid), polling every POLL_INTERVAL_S, at most stop_timeout_s
        if still alive: signal(server.transport, pid, "KILL"); outcome = FAILED
    run(server.transport, "rm -f " + quote(<workdir>/server.pid))
    return outcome (ok unless KILL was needed)
```

`exec` makes the background pid the server process itself, so SIGINT reaches the server
([../remote_transport/design.md#background-processes](../remote_transport/design.md#background-processes)).

## ClientRun

All client programs run in one shell on the client machine. Each program runs in the background with its pid
written to a file, so a timed-out run can be cleaned up:

```
function client_run(point, config, server, client):
    target = server.data_addr + ":" + listen_port
    lg = [client.binaries.loadgen, "--target", target] + args(point.loadgen_args) + ["--output_path", RD + "/loadgen"]
    script lines:
        cd RD || exit 97
        CP=; C=0
        if canceller enabled:
            <canceller words: binary, --target target, args(point.canceller_args), --output_path RD/canceller>
                > RD/canceller.log 2>&1 &
            CP=$!; echo $CP > RD/canceller.pid
        <lg words> > RD/loadgen.log 2>&1 &
        LP=$!; echo $LP > RD/loadgen.pid
        wait $LP; L=$?
        if canceller enabled: wait $CP; C=$?
        echo "loadgen_exit=$L canceller_exit=$C"
        [ "$L" -eq 0 ] && [ "$C" -eq 0 ]
    timeout = max(lg duration_s + lg slo_ms / 1000,
                  canceller duration_s + CANCELLER_DRAIN_S if enabled else 0) + config.run.client_timeout_margin_s
    r = run(client.transport, script joined with newlines, timeout)
    if r.exit_status == -1:                                   # timed out: kill what is left
        run(client.transport, "for f in RD/loadgen.pid RD/canceller.pid; do [ -f $f ] && kill -s KILL $(cat $f); done; true")
    return r.exit_status == 0
```

The load generator and the canceller start within milliseconds of each other. That is enough because their
schedules are independent by design ([../../design.md#time-bounds-of-a-run](../../design.md#time-bounds-of-a-run)).

## Point outputs

```
function collect_outputs(point, config, server, client, pdir):
    fetch(client.transport, RD/loadgen.requests.csv, pdir/loadgen.requests.csv)
    fetch(client.transport, RD/loadgen.summary.json, pdir/loadgen.summary.json)
    fetch(client.transport, RD/loadgen.log, pdir/loadgen.log)
    if canceller enabled: same for canceller.cancels.csv, canceller.summary.json, canceller.log
    fetch(server.transport, RD/server.out, pdir/server.out)
    line = last line of pdir/server.out that starts with COUNTERS_PREFIX
    if line exists: write pdir/server_counters.json = line parsed as JSON (same three keys)

function validate_outputs(point, pdir):
    require pdir/loadgen.summary.json            else "loadgen summary missing"
    if canceller enabled: require pdir/canceller.summary.json   else "canceller summary missing"
    require pdir/server_counters.json            else "server CancelCounters line missing"
    return none
```

Every fetch is best effort (`missing` is fine), so a failed point still keeps whatever evidence exists.

## Design specific to Python

Shared tool `implementation/scripts/` (tooling language Python 3.11+; see `AGENTS.md` "Shared tooling"). Identical on
every `impl/<lang>` branch. Plotting needs `matplotlib` (`python3 -m pip install -r implementation/scripts/requirements.txt`).
Tests: `python3 -B -m unittest discover -s implementation/scripts/tests -t implementation/scripts`.

### Python choices

- `run_point` uses `try / except / finally`. `finally` always stops the server and collects outputs. `KeyboardInterrupt` is recorded as reason `interrupted` and re-raised after the `failed` marker is written.
- Waiting uses `time.monotonic()` deadlines and `time.sleep(POLL_INTERVAL_S)`.
- `params.json` and `server_counters.json` are written with `json.dump(..., indent=2)`.

### Traceability

Generated from the `# Design:` tags in the code by `implementation/spec_tools/tracegen.py` (see `AGENTS.md`). Each link points at the first code line after the tag.
Regenerate from the repository root with `python3 implementation/spec_tools/tracegen.py --lang Python --impl implementation/scripts --ext .py --comment '#' --write`.

<!-- trace:begin -->
| Design subsection | Implementation (file:line) |
|---|---|
| [run_point](#run_point) | [executor.py:47](../../../implementation/scripts/benchrunner/executor.py#L47) |
| [ServerLifecycle](#serverlifecycle) | [executor.py:94](../../../implementation/scripts/benchrunner/executor.py#L94), [executor.py:113](../../../implementation/scripts/benchrunner/executor.py#L113) |
| [ClientRun](#clientrun) | [executor.py:133](../../../implementation/scripts/benchrunner/executor.py#L133) |
| [Point outputs](#point-outputs) | [executor.py:161](../../../implementation/scripts/benchrunner/executor.py#L161), [executor.py:182](../../../implementation/scripts/benchrunner/executor.py#L182) |
<!-- trace:end -->
