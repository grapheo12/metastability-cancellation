# Ingress — Design

## Structure

```mermaid
flowchart LR
  C[Load-generator clients] -->|"h2c, POST /ping"| FE[Frontend: one handler per stream]
  FE -->|pick| LB[Load Balancer: lb_policy + BackendStats]
  FE -->|"publish / clear"| IM[(In-Flight Map)]
  FE -->|"forward, wait"| UP[Upstream Pool: conn per client x backend]
  LC[Load Canceller: single sender] -->|snapshot| IM
  LC -->|"DELETE /cancel, own conn per backend"| S0[server 0]
  UP --> S0
  UP --> S1[server 1 ...]
  LC --> S1
```

| Subcomponent | Responsibility | Spec |
|---|---|---|
| In-Flight Map | Lock-free `client_id → InFlightEntry` of the forwards in progress | [inflight_map/description.md](inflight_map/description.md) |
| Load Balancer | LbPolicy (`pick`), BackendStats (`finish`) | [load_balancer/description.md](load_balancer/description.md) |
| Upstream Pool | One h2c connection per (client, backend), dialed once, dropped on connection error | [upstream_pool/description.md](upstream_pool/description.md) |
| Load Canceller | CancelSchedule from the first `/ping`, InFlightVictimChoice, `/cancel` on its own connection per backend | [load_canceller/description.md](load_canceller/description.md) |
| Frontend | Client connections, ForwardTask per `/ping`, reset propagation, StartOnce | [frontend/description.md](frontend/description.md) |

## Shared state

One of each per process, created at startup and never replaced:

| State | Owner | Shared with |
|---|---|---|
| `config: IngressConfig` | Startup | everyone, read only |
| `inflight: InFlightMap`, with client ids bounded by `num_clients` | In-Flight Map | Frontend (publish, clear), Load Canceller (snapshot) |
| `stats: array[B] of BackendStats` | Load Balancer | Frontend (outcomes), Shutdown (summary) |
| `run_start` | set once, by the first `/ping` | Load Canceller (time base) |

`B` is the number of backends.

## Configuration validation

```
function validate(params):
    listen_addr parses as host:port                         else error
    backends = params.backends split on ","; each element trimmed
    1 <= len(backends) <= MAX_BACKENDS                      else error
    every backend parses as host:port                       else error "backend <i> must be host:port"
    1 <= num_clients <= MAX_CLIENTS                         else error
    lb_policy in {round_robin, least_outstanding, power_of_two}  else error
    cancel_rate >= 0 and finite                             else error
    if cancel_rate > 0: duration_s > 0 and finite           else error "duration_s is required with cancel_rate > 0"
    seed parses as u64                                      else error
    return IngressConfig(..., duration_ns = round(duration_s * 1e9) if cancel_rate > 0)
```

Duplicate backends are allowed: they are separate `server_id`s that happen to share an address.

## Concurrency model

- **Frontend:** every `/ping` stream is handled by its own coroutine or thread, which blocks while the backend
  works. A reader for each client connection keeps reading frames while forwards are in progress, so a client's
  `RST_STREAM` is seen at once and passed upstream.
- **Load Canceller:** one sender activity. Its `/cancel` responses are collected by the reader of its own
  connections, never by the sender.
- **Lock-free paths:** the In-Flight Map and the BackendStats counters use only atomic operations. The Upstream
  Pool may lock while it creates a connection, but never while a request is in progress on one.
- If the implementation uses cooperative coroutines on one thread, nothing in the Ingress may busy-wait: every
  wait is on network I/O, a timer, or a lock held only briefly.

## Time base

`now()` is a monotonic clock. `run_start` is the monotonic time at which the Frontend receives the **first**
`/ping` whose body parses. The Load Canceller's schedule is measured from `run_start`, so it starts with the load
generator's traffic, not with the Ingress process. `run_end_s` in the summary is `(time of shutdown - run_start)`
in seconds, or `0` if no `/ping` was ever received.

## Shutdown

```
procedure shutdown():                                  # on interrupt or termination signal
    stop accepting; send GOAWAY on every client connection
    deadline = now() + INGRESS_DRAIN_TIMEOUT_MS * 1e6
    wait until no forward is in progress (sum of stats[*].outstanding == 0) or now() >= deadline
    stop the Load Canceller if it is still sending, then run its drain
    close every upstream and canceller connection           # errors ignored
    write <output_path>.cancels.csv and <output_path>.summary.json
    exit 0
```

Forwards still in progress at the deadline are abandoned: their client streams are reset and they are counted in
neither `ok` nor `rejected`. The load generator finishes before the operator stops the Ingress, so in a normal run
nothing is in progress at shutdown.

## Why one upstream connection per client and backend

The server binds each connection to the client id of its first `/ping`
([../server/description.md](../server/description.md)). Giving each (client, backend) pair its own connection
keeps that rule true behind the Ingress without changing the server. A shared connection would mix client ids and
get `400`s. It also keeps each client's ServiceTime draws on that client's own connection stream, as if the client
had connected directly.

## Design specific to Go

Implementation: `implementation/go` (module `testbed`, Go 1.21, `golang.org/x/net v0.17.0`).
Build and test with `CGO_ENABLED=0 go build ./...` / `CGO_ENABLED=0 go test ./...`. On recent macOS the dynamic
loader rejects cgo-linked Go 1.21 binaries (missing `LC_UUID`), so cgo is disabled.

### Go choices

- One Go package `internal/ingress` for the whole subtree, one file per subcomponent: `config.go` (this node's parameters and validation), `inflight.go`, `balancer.go`, `upstream.go`, `canceller.go`, `frontend.go`, and `ingress.go` (lifecycle and outputs). One package avoids import cycles: the Load Canceller reads the In-Flight Map and the Frontend starts the Load Canceller. The program entry point is `cmd/ingress/main.go`.
- `backends` is split on `,` and each element trimmed with `strings.TrimSpace`; host:port is checked with `net.SplitHostPort`.
- `num_clients` is parsed with `strconv.ParseUint(..., 10, 32)`, bounded by `MaxClients`, and passed to `NewInFlightMap` as the exclusive client-id bound rather than a required storage size.
- `shutdown` is `(*Ingress).Shutdown`: `http.Server.Shutdown` with a 100 ms bound sends GOAWAY on every client connection (as in the server), then it waits on the BackendStats `outstanding` sum, stops the canceller, closes upstream and client connections, and writes both outputs. `cmd/ingress/main.go` calls it on SIGINT/SIGTERM and exits `0`, or `1` if writing fails.
- The summary JSON is the `Summary` struct, marshalled with `json.MarshalIndent`, in API field order.

### Traceability

Generated from the `// Design:` tags in the code by `implementation/spec_tools/tracegen.py` (see `AGENTS.md`). Each link points at the first code line after the tag.
Regenerate from the repository root with `python3 implementation/spec_tools/tracegen.py --lang Go --ext .go --comment // --write`.

<!-- trace:begin -->
| Design subsection | Implementation (file:line) |
|---|---|
| [Structure](#structure) | [ingress.go:47](../../implementation/go/internal/ingress/ingress.go#L47) |
| [Shared state](#shared-state) | [ingress.go:22](../../implementation/go/internal/ingress/ingress.go#L22) |
| [Configuration validation](#configuration-validation) | [config.go:68](../../implementation/go/internal/ingress/config.go#L68) |
| [Concurrency model](#concurrency-model) | [ingress.go:77](../../implementation/go/internal/ingress/ingress.go#L77) |
| [Time base](#time-base) | [frontend.go:124](../../implementation/go/internal/ingress/frontend.go#L124) |
| [Shutdown](#shutdown) | [ingress.go:111](../../implementation/go/internal/ingress/ingress.go#L111), [ingress_test.go:75](../../implementation/go/internal/ingress/ingress_test.go#L75) |
| [Why one upstream connection per client and backend](#why-one-upstream-connection-per-client-and-backend) | [upstream.go:29](../../implementation/go/internal/ingress/upstream.go#L29) |
<!-- trace:end -->
