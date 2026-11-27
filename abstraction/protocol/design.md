# Protocol — Design

## RequestId

A RequestId is an unsigned 64-bit integer:

```
bits 63..32 : client_id  (u32) index of the client, 0 ..= N-1
bits 31..0  : seq        (u32) per-client counter, starts at 0, +1 for each scheduled arrival
```

```
function make_request_id(seq, client_id):
    return (client_id << 32) | seq          # all arithmetic is on unsigned 64-bit

function split_request_id(id):
    return (id & 0xFFFFFFFF, id >> 32)      # (seq, client_id)
```

Because the client id is in the high bits and `seq` only goes up, the ids sent by one client
increase monotonically.

A RequestId in a `/cancel` is **valid** if it follows this layout. Its `seq` half need not
correspond to a request that was or will be sent.

In JSON the id is written as a **decimal string** (for example `"12884901889"`), never as a JSON
number, because values above 2^53 lose precision in many JSON libraries. The decimal form has no
leading zeros, no sign, and between 1 and 20 digits.

Worked examples:

| client_id | seq | id | check |
|---|---|---|---|
| 0 | 0 | `0` | 0 · 2^32 + 0 |
| 0 | 7 | `7` | 0 · 2^32 + 7 |
| 3 | 1 | `12884901889` | 3 · 4294967296 + 1 = 12884901888 + 1 |
| 4294967295 | 4294967295 | `18446744073709551615` | the largest u64 |

## PaddedJsonBody

`/ping` response bodies (`202` and `503`) use this exact serialization; `/ping` requests use the
[PingRequestBody](#pingrequestbody), which adds a RequestDeadline. Every response carries the server's
[ExecReport](#execreport). There is no whitespace, the keys appear in this order, and everything is ASCII:

```
{"id":"<DEC>","exec_ns":"<EX>","start_slack_ns":"<SL>","payload":"<PAD>"}
```

- `<DEC>` is the decimal RequestId described above.
- `<EX>` is the decimal ExecutionTime `exec_ns` (u64, 1 to 20 digits, no leading zeros except `0` itself).
- `<SL>` is the StartSlack `start_slack_ns` as a signed decimal (see [ExecReport](#execreport)), or empty when the
  request has no RequestDeadline.
- `<PAD>` is `k` copies of the ASCII character `x` (byte `0x78`).

Fixed overhead, counted in bytes:

| Fragment | Bytes |
|---|---|
| `{"id":"` | 7 |
| `","exec_ns":"` | 13 |
| `","start_slack_ns":"` | 20 |
| `","payload":"` | 13 |
| `"}` | 2 |
| **Total fixed** | **55** |

```
function build_body(id, exec_ns, start_slack_ns, total_bytes):   # start_slack_ns: i64 or none
    dec = decimal_string(id)
    ex  = decimal_string(exec_ns)
    sl  = "" if start_slack_ns is none else signed_decimal_string(start_slack_ns)
    k = total_bytes - 55 - length(dec) - length(ex) - length(sl)
    assert k >= 0                       # guaranteed when total_bytes >= MIN_BODY_BYTES
    return "{\"id\":\"" + dec + "\",\"exec_ns\":\"" + ex + "\",\"start_slack_ns\":\"" + sl +
           "\",\"payload\":\"" + repeat("x", k) + "\"}"
```

`signed_decimal_string(v)` writes `-` followed by the decimal digits of `|v|` when `v < 0`, and the plain digits
otherwise: no `+`, no leading zeros, `0` for zero. The longest result is `-9223372036854775808` (20 bytes).

Worked examples:

| id | `len(dec)` | exec_ns | `len(ex)` | start_slack_ns | `len(sl)` | total_bytes | k | check |
|---|---|---|---|---|---|---|---|---|
| `0` | 1 | `0` | 1 | none | 0 | 128 | 71 | 55 + 1 + 1 + 0 + 71 = 128 |
| `12884901889` | 11 | `201345678` | 9 | `4791002113` | 10 | 128 | 43 | 55 + 11 + 9 + 10 + 43 = 128 |
| `7` | 1 | `150000000` | 9 | `-35000000` | 9 | 128 | 54 | 55 + 1 + 9 + 9 + 54 = 128 |
| `18446744073709551615` (max) | 20 | `18446744073709551615` (max) | 20 | `-9223372036854775808` (min) | 20 | 115 | 0 | 55 + 20 + 20 + 20 + 0 = 115 |

## ExecReport

The **ExecReport** is what a server tells about how it ran a request, carried in every PaddedJsonBody:

| Field | Type / unit | Meaning |
|---|---|---|
| `exec_ns` | u64, ns | **ExecutionTime**: the request's time on the server minus its queueing delay, i.e. from a worker popping it to its response being built |
| `start_slack_ns` | i64, ns, or none | **StartSlack**: `deadline_unix_ns` minus the server's wall clock when a worker popped it. Positive means time was left before the RequestDeadline; negative means the deadline had already passed. None when the request had no RequestDeadline |

The server measures both ([../server/ping_handler/design.md#run_ping](../server/ping_handler/design.md#run_ping)).
The Protocol only fixes how they are written and read. `start_slack_ns` compares the client's deadline with the
server's wall clock, so any clock difference between the two machines adds to it directly.

## WorkerCount

Every `/ping` answer a server writes with a body (`202` or `503`) carries the response header
`x-worker-threads: <DEC>`, the server's `n_worker_threads`: how many `/ping` requests it runs at once. This
**WorkerCount** lets a load balancer turn a backend's queue into an expected wait
([../ingress/load_balancer/design.md](../ingress/load_balancer/design.md#loadbalancer)). It is a header, not a body
field, so `response_bytes` and the PaddedJsonBody are unchanged.

- `<DEC>` is a decimal integer in `1 ..= 2^32 - 1`, with no sign and no leading zeros.
- Other answers (`400`, `405`, `404`, `/cancel` answers, the HeartbeatStream) do not carry it.
- An Ingress does not relay it to the client.

```
function parse_worker_count(value):                  # header value or none -> u32 | none; never an error
    if value is none or empty or has a non-digit or more than 10 chars: return none
    n = parse_unsigned_decimal(value)
    if n < 1 or n > 2^32 - 1: return none
    return n
```

| Header value | `parse_worker_count` |
|---|---|
| `10` | 10 |
| `1` | 1 |
| `0` | none (below 1) |
| `4294967296` | none (above 2^32 - 1) |
| absent, empty, `ten`, `-3` | none |

## RequestDeadlineHeader

Every `/ping` request a load-generator client sends also carries its RequestDeadline as the request header
`x-request-deadline: <DL>`, the same decimal `deadline_unix_ns` as the body's `"deadline"` ([PingRequestBody](#pingrequestbody)).
This **RequestDeadlineHeader** lets a component that reads headers but not bodies (an Ingress whose load balancer
sees only headers) learn the deadline. The body stays the source of truth: a server ignores the header, and an
Ingress that reads the body does not need it.

- Every RequestCopy of a request carries the same value, as its body does.
- An Ingress relays it unchanged with the request.
- A reader parses it leniently; a missing or malformed header means "no RequestDeadline", never an error:

```
function parse_deadline_header(value):               # header value or none -> i64 | none; never an error
    if value is none: return none
    v = value with leading and trailing ASCII whitespace removed
    if v is not an optional "+" or "-" followed by 1 or more decimal digits: return none
    n = the signed decimal value of v
    if n < -2^63 or n > 2^63 - 1: return none
    return n                                         # Unix ns; 0 or less is a deadline long past
```

| Header value | `parse_deadline_header` |
|---|---|
| `1794533190000000000` | 1794533190000000000 |
| ` 17 ` | 17 (whitespace trimmed) |
| `+5` | 5 |
| `9223372036854775808` | none (above 2^63 - 1) |
| absent, empty, `soon`, `1.5e18` | none |

## PingRequestBody

A `/ping` request body carries the request's **RequestDeadline**, `deadline_unix_ns`: Unix wall-clock nanoseconds
by which the client wants the answer (its first send plus the SLO; the load-generator client sets it). A server
with the `edf` QueueDiscipline orders queued work by it; the Protocol only defines how it is written and read.
Same rules as PaddedJsonBody (no whitespace, this key order, ASCII):

```
{"id":"<DEC>","deadline":"<DL>","payload":"<PAD>"}
```

- `<DEC>` is the decimal RequestId; `<DL>` is the decimal `deadline_unix_ns` (u64, 1 to 20 digits, no leading
  zeros except `0` itself); `<PAD>` is `k` copies of `x`.

| Fragment | Bytes |
|---|---|
| `{"id":"` | 7 |
| `","deadline":"` | 14 |
| `","payload":"` | 13 |
| `"}` | 2 |
| **Total fixed** | **36** |

```
function build_request_body(id, deadline_unix_ns, total_bytes):
    dec = decimal_string(id)
    dl  = decimal_string(deadline_unix_ns)
    k = total_bytes - 36 - length(dec) - length(dl)
    assert k >= 0                       # guaranteed when total_bytes >= MIN_REQUEST_BODY_BYTES
    return "{\"id\":\"" + dec + "\",\"deadline\":\"" + dl + "\",\"payload\":\"" + repeat("x", k) + "\"}"
```

Worked examples:

| id | `len(dec)` | deadline_unix_ns | `len(dl)` | total_bytes | k | check |
|---|---|---|---|---|---|---|
| `0` | 1 | `1794533185297574000` | 19 | 128 | 72 | 36 + 1 + 19 + 72 = 128 |
| `12884901889` | 11 | `1794533185297574000` | 19 | 128 | 62 | 36 + 11 + 19 + 62 = 128 |
| `18446744073709551615` (max) | 20 | `18446744073709551615` (max) | 20 | 76 | 0 | 36 + 20 + 20 + 0 = 76 |

A body without a valid `"deadline"` (an older client, or any other JSON client) is still a valid `/ping`: its
RequestDeadline is `none` ([Parsing](#parsing)).

## Minimum body size

The longest possible `<DEC>` and `<EX>` have 20 digits each and the longest `<SL>` has 20 bytes, so a response needs
`MIN_BODY_BYTES = 55 + 20 + 20 + 20 = 115`. A request also
holds a deadline of up to 20 digits, so `MIN_REQUEST_BODY_BYTES = 36 + 20 + 20 = 76`. Any configured size at least
as large can be padded exactly for every possible id and deadline.

```
function validate_body_size(total_bytes):            # response_bytes
    if total_bytes < 115: return ConfigError("body size must be >= 115 bytes")
    return ok

function validate_request_body_size(total_bytes):    # request_bytes
    if total_bytes < 76: return ConfigError("request body size must be >= 76 bytes")
    return ok
```

## CancelBody

A `/cancel` request body is not padded:

```
{"id":"<DEC>"}
```

```
function build_cancel_body(id):
    return "{\"id\":\"" + decimal_string(id) + "\"}"
```

Its length is `9 + len(dec)` bytes (`{"id":"` is 7, `"}` is 2). For example, id `12884901889` gives
20 bytes.

## CancelReplyBody

The `202` answer to `/cancel` carries the target client's **FinishedWatermark**: the highest RequestId of that
client whose `/ping` the server has finished, with any outcome
([../server/cancel_tracker/design.md](../server/cancel_tracker/design.md)). The body is not padded:

```
{"finished":"<DEC>"}       # a watermark exists
{"finished":null}          # the client has no registry slot yet, or has finished nothing
```

```
function build_cancel_reply(finished):
    if finished is none: return "{\"finished\":null}"
    return "{\"finished\":\"" + decimal_string(finished) + "\"}"

function parse_cancel_reply(body):
    value = json_parse(body)                           # lenient, as in Parsing
    if value is not an object or "finished" not in value: return ParseError
    f = value["finished"]
    if f is null: return none
    if f is not a string, is empty, contains a non-digit or has more than 20 chars: return ParseError
    n = parse_unsigned_decimal(f)
    if n > 2^64 - 1: return ParseError
    return n                                           # other fields are ignored
```

Length: `{"finished":"` is 13 bytes and `"}` is 2, so a watermark body is `15 + len(dec)` bytes. The null body is
exactly 17 bytes.

| `finished` | Body | Bytes | Check |
|---|---|---|---|
| none | `{"finished":null}` | 17 | `{"finished":` 12 + `null` 4 + `}` 1 = 17 |
| `0` | `{"finished":"0"}` | 16 | 15 + 1 |
| `12884901893` (client 3, seq 5) | `{"finished":"12884901893"}` | 26 | 15 + 11 |
| `2^64 − 1` | `{"finished":"18446744073709551615"}` | 35 | 15 + 20 |

| Input to `parse_cancel_reply` | Result |
|---|---|
| `{"finished":"42"}` | `42` |
| `{"finished":null}` | none |
| `{"finished":42}` (a number, not a string) | ParseError |
| `{}` or an empty body | ParseError |

## Parsing

Parsing is lenient. It accepts any valid JSON, not only our exact serializations, so other clients
can talk to the server. The same function parses PaddedJsonBody and CancelBody.

```
function parse_body(body):
    value = json_parse(body)                         # standard JSON parser
    if value is not an object:                        return ParseError
    if "id" not in value or value["id"] is not string: return ParseError
    s = value["id"]
    if s is empty or contains a non-digit or has more than 20 chars: return ParseError
    n = parse_unsigned_decimal(s)
    if n > 2^64 - 1:                                   return ParseError
    return n                                          # "payload" and other fields are ignored

function parse_deadline(body):                         # -> u64 | none; never an error
    value = json_parse(body)
    if value is not an object or "deadline" not in value or value["deadline"] is not string: return none
    s = value["deadline"]
    if s is empty or contains a non-digit or has more than 20 chars: return none
    n = parse_unsigned_decimal(s)
    if n > 2^64 - 1: return none
    return n
```

```
function parse_exec_report(body):                      # -> (u64 | none, i64 | none); never an error
    value = json_parse(body)
    if value is not an object: return (none, none)
    exec = none
    if "exec_ns" in value and value["exec_ns"] is string:
        s = value["exec_ns"]
        if s is not empty and has only digits and at most 20 chars and parse_unsigned_decimal(s) <= 2^64 - 1:
            exec = parse_unsigned_decimal(s)
    slack = none
    if "start_slack_ns" in value and value["start_slack_ns"] is string:
        s = value["start_slack_ns"]                    # empty: no deadline -> none
        d = s without one leading "-"
        if d is not empty and has only digits and at most 19 chars:
            v = parse_unsigned_decimal(d), negated if s starts with "-"
            if -2^63 <= v <= 2^63 - 1: slack = v
    return (exec, slack)
```

`parse_deadline` is as lenient as `parse_body`: a missing or malformed deadline makes the request deadline-less,
not invalid. Callers parse the id first; a body whose id is invalid is rejected before its deadline matters.

| Body | `parse_body` | `parse_deadline` |
|---|---|---|
| `{"id":"7","deadline":"1000","payload":""}` | 7 | 1000 |
| `{"id":"7","payload":""}` | 7 | none |
| `{"id":"7","deadline":1000}` (number, not string) | 7 | none |
| `{"id":"7","deadline":"18446744073709551616"}` (2^64) | 7 | none |

`parse_exec_report` is just as lenient: a missing or malformed field gives none for that field only, so a body from
a server without an ExecReport is still a valid response.

| Body | `parse_exec_report` |
|---|---|
| `{"id":"7","exec_ns":"150000000","start_slack_ns":"-35000000","payload":""}` | (150000000, -35000000) |
| `{"id":"7","exec_ns":"0","start_slack_ns":"","payload":""}` | (0, none) |
| `{"id":"7","payload":""}` (no ExecReport) | (none, none) |
| `{"id":"7","exec_ns":150,"start_slack_ns":"+5"}` (number; `+` sign) | (none, none) |
| `{"id":"7","exec_ns":"1","start_slack_ns":"-9223372036854775809"}` (below -2^63) | (1, none) |

## HeartbeatLine

A **HeartbeatStream** is the body of a `200` answer to `GET /heartbeat`: a sequence of **HeartbeatLines**, written
by the server one per heartbeat, that never ends on its own. The response has `content-type: text/plain` and no
`content-length`. Each line is ASCII:

```
function build_heartbeat_line(seq):                 # seq: u64
    return "hb " + decimal(seq) + "\n"              # decimal: no sign, no leading zeros ("0" for 0)

function parse_heartbeat_line(line):                 # line: the bytes before one "\n", without it
    if line does not start with "hb ":               return ParseError
    s = line after "hb "
    if s is empty or contains a non-digit or has more than 20 chars: return ParseError
    n = parse_unsigned_decimal(s)
    if n > 2^64 - 1:                                  return ParseError
    return n
```

- `seq` counts from 0 on each stream, one per line. A reader may use gaps to see lost lines, but nothing requires it.
- Each line is sent in its own DATA frame (or frames) and flushed, so a reader sees it when it is written.
- The stream ends with RST or a closed connection, never with END_STREAM in normal operation; a reader treats any
  end as "no more heartbeats on this stream".

| Bytes | `parse_heartbeat_line` |
|---|---|
| `hb 0` | 0 |
| `hb 41` | 41 |
| `hb 18446744073709551615` | 2^64 - 1 |
| `hb 18446744073709551616` | ParseError (2^64) |
| `hb ` or `hb -1` or `ping 3` | ParseError |

## Status classification (load-generator client)

| Received for `/ping` | Meaning for the client |
|---|---|
| `202` with body whose parsed id equals the sent id | success (`ok`) |
| `503` with body whose parsed id equals the sent id | cancelled by the server through `/cancel` (`rejected`) |
| any other status (including `502` from an Ingress), unparseable body, or id mismatch | `error` |

## Status classification (load canceller)

| Received for `/cancel` | Meaning for the canceller |
|---|---|
| `202` | `accepted`. The body is a CancelReplyBody. If it fails to parse, the cancel is still `accepted` and the watermark is unknown |
| `503` | `rejected` (registry full) |
| `400` | `bad_request` |
| any other status | `error` |

## Design specific to Go

Implementation: `implementation/go` (module `testbed`, Go 1.21, `golang.org/x/net v0.17.0`).
Build and test with `CGO_ENABLED=0 go build ./...` / `CGO_ENABLED=0 go test ./...`. On recent macOS the dynamic
loader rejects cgo-linked Go 1.21 binaries (missing `LC_UUID`), so cgo is disabled.

### Go choices

- Package `internal/protocol`, file `protocol.go`. Constants are untyped Go constants named in CamelCase after the API names.
- `ParseError` is the sentinel `protocol.ErrParse`. `ParseBody` unmarshals into `map[string]json.RawMessage` (not a struct), so keys match case-sensitively as the design requires. Then it decodes `id` as a `string`. A JSON `null` decodes to `""` and is rejected by the empty check.
- `BuildBody(id, execNs, startSlackNs *int64, totalBytes)` panics if `k < 0`. This cannot happen once `ValidateBodySize` has passed. A nil `startSlackNs` is the design's none and writes an empty field; `signed_decimal_string` is `strconv.FormatInt`, which already writes no `+` and no leading zeros.
- `ParseExecReport(body) (execNs, execOK, startSlackNs, slackOK)` stands for `(u64 | none, i64 | none)`. `exec_ns` reuses `parseDecimalString`; `start_slack_ns` goes through `parseSignedDecimalString`, which strips one `-`, checks 1 to 19 ASCII digits by hand, parses them as a u64 and range-checks against `2^63 - 1` (positive) or `2^63` (negative, where `int64(-u)` wraps to exactly `-2^63`). `TestParseExecReportWorkedExamples` checks the design's table plus `2^63 - 1` and non-JSON input.
- Call sites that only need a valid response body (tests sending a `/ping` with a PaddedJsonBody) pass `exec_ns = 0` and a nil slack.
- `BuildCancelReply(finished, ok)` and `ParseCancelReply(body) (finished, ok, err)` stand for `u64 | none`: `ok == false` is none. `ParseCancelReply` detects `null` by the raw JSON value `null`. The digit-string checks are shared with `ParseBody` in the helper `parseDecimalString`.
- `BuildRequestBody` mirrors `BuildBody` with the 36-byte overhead and panics if `k < 0`, which `ValidateRequestBodySize` rules out. `ParseDeadline(body) (deadline, ok)` stands for `u64 | none` (`ok == false` is none) and reuses `parseDecimalString`; it unmarshals the body separately from `ParseBody`, so the server parses a `/ping` body twice (small bodies, accepted cost).
- WorkerCount: constant `WorkerThreadsHeader`; `ParseWorkerCount(value string) (uint32, bool)` checks 1 to 10 ASCII digits by hand, then `strconv.ParseUint` and the range `1 ..= 2^32-1`. An absent header is the empty string (Go's `Header.Get`), which gives none. `TestParseWorkerCount` covers the design's table.
- HeartbeatLine: `BuildHeartbeatLine(seq)` uses `strconv.FormatUint`; `ParseHeartbeatLine(line)` checks the `hb ` prefix and 1 to 20 ASCII digits by hand, then `strconv.ParseUint(…, 10, 64)`, whose range error rejects 2^64. Both return `ErrParse` for every rejection. `TestHeartbeatLine` checks the worked examples.
- Tests: `protocol_test.go` checks every worked example (including the CancelReplyBody byte lengths) the parse rejection cases, and the PingRequestBody examples and `parse_deadline` table.

- RequestDeadlineHeader: the constant `RequestDeadlineHeader` and `ParseDeadlineHeader(value) (int64, bool)`: `strings.Trim` of the ASCII whitespace set, a digit check on what follows an optional sign, then `strconv.ParseInt(v, 10, 64)` for the range. `TestParseDeadlineHeader` covers the worked examples plus the i64 maximum.

### Not traced

| Design subsection | Reason |
|---|---|
| [Status classification (load-generator client)](#status-classification-load-generator-client) | realized by the Envoy implementation on this branch (the load generator's wrapper, see Design specific to Envoy) |
| [Status classification (load canceller)](#status-classification-load-canceller) | not realized on `impl/envoy`: the Ingress rejects `cancel_rate > 0` (no load canceller) |

On `impl/envoy` only the Go server's packages are copied; the load generator and the Ingress are Envoy and Nighthawk.

### Traceability

Generated from the `// Design:` tags in the code by `implementation/spec_tools/tracegen.py` (see `AGENTS.md`). Each link points at the first code line after the tag.
Regenerate from the repository root with `python3 implementation/spec_tools/tracegen.py --lang Go --ext .go --comment // --write`.

<!-- trace:begin -->
| Design subsection | Implementation (file:line) |
|---|---|
| [RequestId](#requestid) | [protocol.go:44](../../implementation/go/internal/protocol/protocol.go#L44), [protocol.go:51](../../implementation/go/internal/protocol/protocol.go#L51), [protocol_test.go:10](../../implementation/go/internal/protocol/protocol_test.go#L10) |
| [PaddedJsonBody](#paddedjsonbody) | [protocol.go:59](../../implementation/go/internal/protocol/protocol.go#L59), [protocol.go:67](../../implementation/go/internal/protocol/protocol.go#L67), [protocol_test.go:32](../../implementation/go/internal/protocol/protocol_test.go#L32) |
| [ExecReport](#execreport) | [protocol.go:246](../../implementation/go/internal/protocol/protocol.go#L246), [protocol_test.go:63](../../implementation/go/internal/protocol/protocol_test.go#L63) |
| [WorkerCount](#workercount) | [protocol.go:304](../../implementation/go/internal/protocol/protocol.go#L304), [protocol_test.go:232](../../implementation/go/internal/protocol/protocol_test.go#L232) |
| [RequestDeadlineHeader](#requestdeadlineheader) | [protocol.go:357](../../implementation/go/internal/protocol/protocol.go#L357), [protocol_test.go:246](../../implementation/go/internal/protocol/protocol_test.go#L246) |
| [PingRequestBody](#pingrequestbody) | [protocol.go:110](../../implementation/go/internal/protocol/protocol.go#L110), [protocol_test.go:159](../../implementation/go/internal/protocol/protocol_test.go#L159) |
| [Minimum body size](#minimum-body-size) | [protocol.go:96](../../implementation/go/internal/protocol/protocol.go#L96), [protocol.go:133](../../implementation/go/internal/protocol/protocol.go#L133), [protocol_test.go:96](../../implementation/go/internal/protocol/protocol_test.go#L96), [protocol_test.go:182](../../implementation/go/internal/protocol/protocol_test.go#L182) |
| [CancelBody](#cancelbody) | [protocol.go:143](../../implementation/go/internal/protocol/protocol.go#L143), [protocol_test.go:88](../../implementation/go/internal/protocol/protocol_test.go#L88) |
| [CancelReplyBody](#cancelreplybody) | [protocol.go:151](../../implementation/go/internal/protocol/protocol.go#L151), [protocol.go:162](../../implementation/go/internal/protocol/protocol.go#L162), [protocol_test.go:119](../../implementation/go/internal/protocol/protocol_test.go#L119), [protocol_test.go:144](../../implementation/go/internal/protocol/protocol_test.go#L144) |
| [Parsing](#parsing) | [protocol.go:184](../../implementation/go/internal/protocol/protocol.go#L184), [protocol.go:208](../../implementation/go/internal/protocol/protocol.go#L208), [protocol.go:225](../../implementation/go/internal/protocol/protocol.go#L225), [protocol.go:246](../../implementation/go/internal/protocol/protocol.go#L246), [protocol.go:266](../../implementation/go/internal/protocol/protocol.go#L266), [protocol_test.go:63](../../implementation/go/internal/protocol/protocol_test.go#L63), [protocol_test.go:103](../../implementation/go/internal/protocol/protocol_test.go#L103), [protocol_test.go:189](../../implementation/go/internal/protocol/protocol_test.go#L189) |
| [HeartbeatLine](#heartbeatline) | [protocol.go:323](../../implementation/go/internal/protocol/protocol.go#L323), [protocol.go:331](../../implementation/go/internal/protocol/protocol.go#L331), [protocol_test.go:215](../../implementation/go/internal/protocol/protocol_test.go#L215) |
| [Status classification (load-generator client)](#status-classification-load-generator-client) | not traced |
| [Status classification (load canceller)](#status-classification-load-canceller) | not traced |
<!-- trace:end -->

## Design specific to Envoy

Implementation: `implementation/envoy`: vendored Envoy (`envoy/`) and Nighthawk (`nighthawk/`) with the testbed's
changes, recorded in [VENDORED.md](../../implementation/envoy/VENDORED.md) ([AGENTS.md](../../AGENTS.md#vendored-code)),
built in Docker with Bazel by `implementation/envoy/build.sh` (C++ tests: `bazel test` on the targets VENDORED.md
names); Python 3 wrappers `bin/ingress.py`, `bin/loadgen.py` and `wrappers/convert.py` (tests: `cd
implementation/envoy/wrappers && python3 -B -m unittest test_convert`). Only code written or changed for the testbed
carries `Design:` tags; unchanged upstream code does not.

### Envoy choices

- PingRequestBody, RequestId and Minimum body size: Nighthawk's `ping` RequestSource (new,
  `ping_request_source_plugin_impl.cc`). `BuildPingRequestBody` writes the body with `absl::StrCat`; the RequestId is
  `client_id << 32 | seq`, with `seq` from 0 per Nighthawk worker (one worker per client); the factory refuses a
  `request_bytes` that cannot hold the longest id and deadline (76 bytes).
- RequestDeadlineHeader: the RequestSource adds `x-request-deadline` with the same decimal as the body's `deadline`.
  The `peak_ewma_deadline` balancer reads it with `absl::SimpleAtoi` into an `int64_t`, which trims whitespace and
  accepts a sign, as `parse_deadline_header` does.
- ExecReport and Parsing: Nighthawk's per-request log (new, `request_log.cc`) copies `id`, `exec_ns` and
  `start_slack_ns` out of the request and response bodies by key lookup (`stringField`), a lenient parse that never
  fails; the `testbed_anxious` filter parses `exec_ns` with `parseExecNs`.
- WorkerCount: the Envoy route removes `x-worker-threads` from every response (`response_headers_to_remove` in
  `bin/ingress.py`), so an Ingress never relays it.
- Status classification (load-generator client): `convert.request_outcome` maps each Nighthawk per-request log line
  (result and status) to the client Outcome.

### Not traced

| Design subsection | Reason |
|---|---|
| [PaddedJsonBody](#paddedjsonbody) | realized by the Go server copy on this branch (Design specific to Go) |
| [CancelBody](#cancelbody) | not realized on Envoy: the Ingress rejects `cancel_rate > 0` (`cancel_rate <value> is not supported by this implementation`), so no `/cancel` is sent |
| [CancelReplyBody](#cancelreplybody) | as CancelBody |
| [HeartbeatLine](#heartbeatline) | realized by the Go server copy; the Envoy Ingress rejects `heartbeat_timeout_ms > 0` |
| [Status classification (load canceller)](#status-classification-load-canceller) | as CancelBody |

### Traceability

Generated from the `Design:` tags in the testbed's code under `implementation/envoy` by `implementation/spec_tools/tracegen.py` (see `AGENTS.md`). Each link points at the first code line after the tag.
Regenerate from the repository root with `python3 implementation/spec_tools/tracegen.py --lang Envoy --impl implementation/envoy --ext .cc --ext .h --comment // --ext .py:# --write`.

<!-- trace:begin -->
| Design subsection | Implementation (file:line) |
|---|---|
| [RequestId](#requestid) | [ping_request_source_plugin_impl.cc:46](../../implementation/envoy/nighthawk/source/request_source/ping_request_source_plugin_impl.cc#L46) |
| [PaddedJsonBody](#paddedjsonbody) | not traced |
| [ExecReport](#execreport) | [filter.cc:23](../../implementation/envoy/envoy/contrib/peak_ewma/anxious/source/filter.cc#L23), [request_log.cc:91](../../implementation/envoy/nighthawk/source/client/request_log.cc#L91), [ping_request_source_plugin_test.cc:51](../../implementation/envoy/nighthawk/test/request_source/ping_request_source_plugin_test.cc#L51) |
| [WorkerCount](#workercount) | [ingress.py:229](../../implementation/envoy/bin/ingress.py#L229) |
| [RequestDeadlineHeader](#requestdeadlineheader) | [deadline_lb.cc:152](../../implementation/envoy/envoy/contrib/peak_ewma/load_balancing_policies/source/deadline_lb.cc#L152), [ping_request_source_plugin_impl.cc:62](../../implementation/envoy/nighthawk/source/request_source/ping_request_source_plugin_impl.cc#L62), [ping_request_source_plugin_test.cc:34](../../implementation/envoy/nighthawk/test/request_source/ping_request_source_plugin_test.cc#L34) |
| [PingRequestBody](#pingrequestbody) | [ping_request_source_plugin_impl.cc:23](../../implementation/envoy/nighthawk/source/request_source/ping_request_source_plugin_impl.cc#L23), [ping_request_source_plugin_impl.cc:27](../../implementation/envoy/nighthawk/source/request_source/ping_request_source_plugin_impl.cc#L27), [ping_request_source_plugin_test.cc:18](../../implementation/envoy/nighthawk/test/request_source/ping_request_source_plugin_test.cc#L18) |
| [Minimum body size](#minimum-body-size) | [ping_request_source_plugin_impl.cc:75](../../implementation/envoy/nighthawk/source/request_source/ping_request_source_plugin_impl.cc#L75), [ping_request_source_plugin_test.cc:27](../../implementation/envoy/nighthawk/test/request_source/ping_request_source_plugin_test.cc#L27) |
| [CancelBody](#cancelbody) | not traced |
| [CancelReplyBody](#cancelreplybody) | not traced |
| [Parsing](#parsing) | [filter.cc:23](../../implementation/envoy/envoy/contrib/peak_ewma/anxious/source/filter.cc#L23), [anxious_tracker_test.cc:171](../../implementation/envoy/envoy/contrib/peak_ewma/anxious/test/anxious_tracker_test.cc#L171), [request_log.cc:64](../../implementation/envoy/nighthawk/source/client/request_log.cc#L64), [request_log.cc:79](../../implementation/envoy/nighthawk/source/client/request_log.cc#L79), [ping_request_source_plugin_test.cc:51](../../implementation/envoy/nighthawk/test/request_source/ping_request_source_plugin_test.cc#L51) |
| [HeartbeatLine](#heartbeatline) | not traced |
| [Status classification (load-generator client)](#status-classification-load-generator-client) | [convert.py:340](../../implementation/envoy/wrappers/convert.py#L340) |
| [Status classification (load canceller)](#status-classification-load-canceller) | not traced |
<!-- trace:end -->
