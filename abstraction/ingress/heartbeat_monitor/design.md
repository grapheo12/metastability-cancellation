# Heartbeat Monitor — Design

`B` is the number of backends. `monotonic_now_ns()` is the Ingress's monotonic clock
([../design.md#time-base](../design.md#time-base)). `timeout_ns = heartbeat_timeout_ms * 1_000_000`.

## HeartbeatReader

```
function monitor_start(config, start_ns):
    if config.heartbeat_timeout_ms == 0: return none
    m = { last_hb_ns: array[B] of atomic u64 = start_ns, live: array[B] of atomic boolean = true,
          log: [ LivenessRecord(start_ns, s, true) for s in 0 ..= B-1 ], stop: false }
    for s in 0 ..= B-1: spawn reader(m, config, s)
    spawn checker(m, timeout_ns)
    return m

procedure reader(m, config, s):
    while not m.stop:
        conn = open_h2c_connection(config.backends[s], timeout = UPSTREAM_DIAL_TIMEOUT_MS) or none on failure
        if conn is not none:
            stream = send GET /heartbeat on conn                  # no body
            if stream's status is STATUS_STREAM_OK:
                for each line of the stream's body, split on "\n", until the stream ends or m.stop:
                    if protocol.parse_heartbeat_line(line) is a u64:
                        m.last_hb_ns[s].store(monotonic_now_ns())  # a malformed line is ignored
            close conn                                            # errors ignored
        if not m.stop: wait HEARTBEAT_REDIAL_MS, ending early on m.stop
```

- **Own connection.** Each reader dials its own connection, like the LoadCanceller's
  ([../load_canceller/design.md#startup](../load_canceller/design.md#startup)). It never carries a `/ping`, so the
  server never binds it to a client, and a slow `/ping` connection never delays a heartbeat.
- **What counts.** Only a complete, well-formed HeartbeatLine refreshes `last_hb_ns`. Connecting, headers, a
  non-`200` status or a malformed line do not.
- **Redial.** When the stream ends (the server stopped, the connection broke) or a dial fails, the reader waits
  `HEARTBEAT_REDIAL_MS` and tries again, until `monitor_stop`. A backend that comes back is read again.
- **A silent stream** (a frozen server keeps the stream open but writes nothing) is never ended by the reader:
  BackendLiveness turns it dead by age alone.

## BackendLiveness

```
procedure checker(m, timeout_ns):
    while not m.stop:
        wait HEARTBEAT_CHECK_MS, ending early on m.stop
        now = monotonic_now_ns()
        for s in 0 ..= B-1:
            alive = (now - m.last_hb_ns[s].load()) < timeout_ns           # unsigned; last_hb_ns <= now
            if alive != m.live[s].load():
                m.live[s].store(alive)
                m.log.append(LivenessRecord(now, s, alive))               # only the checker appends

function is_live(m, s):
    return true if m is none else m.live[s].load()

function monitor_stop(m):
    m.stop = true; wake every reader and the checker; close the readers' connections
    wait until every reader and the checker have returned
    return m.log
```

- **Start.** Every backend starts live, with `last_hb_ns = start_ns`, so a backend has `heartbeat_timeout_ms` to
  send its first line.
- **Detection delay.** A backend that stops beating at time `t` (its last line) turns dead at the first check at or
  after `t + timeout`, so between `timeout` and `timeout + HEARTBEAT_CHECK_MS` after its last line. With the
  server's `heartbeat_interval_ms = 500` and `heartbeat_timeout_ms = 1000`, that is two missed heartbeats.
- **Readmission.** The first HeartbeatLine after that makes it live again at the next check.
- **One writer.** Only the checker writes `live` and the log, so the log needs no lock, and its records are in time
  order. Readers write only their own `last_hb_ns[s]`.

Worked example, `heartbeat_timeout_ms = 1000`, heartbeats every 500 ms from backend 0 until its last line at
60100 ms, checks at multiples of 50 ms, times in ms on the monitor's clock:

| Time | Event | `live[0]` | Check |
|---|---|---|---|
| 0 | `monitor_start` | true (logged) | start |
| 60100 | last line from backend 0 | true | `last_hb = 60100` |
| 61050 | check | true | 61050 - 60100 = 950 < 1000 |
| 61100 | check | false (logged) | 1000, not < 1000 |
| 75000 | backend 0 beats again | false | not a check time |
| 75000 | check (same instant, after the line) | true (logged) | 0 < 1000 |
| — | `heartbeat_timeout_ms = 0` | `is_live` always true | monitor is `none` |

## Failure and edge cases

| Situation | Behavior |
|---|---|
| Backend down at Ingress start | Dials fail every `HEARTBEAT_REDIAL_MS`; the backend turns dead after `heartbeat_timeout_ms` |
| Backend answers `/heartbeat` with `404` (older server) | No line ever counts; it turns dead after `heartbeat_timeout_ms`. Leave the monitor off for such servers |
| Stream ends mid-line | The partial line is dropped |
| Shutdown while a reader is dialing | `monitor_stop` closes the connection; the reader sees `stop` and returns |
| Every backend dead | Only the Load Balancer's choice changes ([../load_balancer/design.md#lbpolicy](../load_balancer/design.md#lbpolicy)) |
