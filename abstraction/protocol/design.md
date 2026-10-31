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

`/ping` request and response bodies use the same exact serialization. There is no whitespace, the
keys appear in this order, and everything is ASCII:

```
{"id":"<DEC>","payload":"<PAD>"}
```

- `<DEC>` is the decimal RequestId described above.
- `<PAD>` is `k` copies of the ASCII character `x` (byte `0x78`).

Fixed overhead, counted in bytes:

| Fragment | Bytes |
|---|---|
| `{"id":"` | 7 |
| `","payload":"` | 13 |
| `"}` | 2 |
| **Total fixed** | **22** |

```
function build_body(id, total_bytes):
    dec = decimal_string(id)
    k = total_bytes - 22 - length(dec)
    assert k >= 0                       # guaranteed when total_bytes >= MIN_BODY_BYTES
    return "{\"id\":\"" + dec + "\",\"payload\":\"" + repeat("x", k) + "\"}"
```

Worked examples:

| id | `len(dec)` | total_bytes | k | check |
|---|---|---|---|---|
| `0` | 1 | 128 | 105 | 22 + 1 + 105 = 128 |
| `12884901889` | 11 | 128 | 95 | 22 + 11 + 95 = 128 |
| `18446744073709551615` (max) | 20 | 42 | 0 | 22 + 20 + 0 = 42 |

## Minimum body size

The longest possible `<DEC>` has 20 digits, so `MIN_BODY_BYTES = 22 + 20 = 42`. Any configured size
of at least 42 can be padded exactly for every possible id.

```
function validate_body_size(total_bytes):
    if total_bytes < 42: return ConfigError("body size must be >= 42 bytes")
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
```

## Status classification (load-generator client)

| Received for `/ping` | Meaning for the client |
|---|---|
| `202` with body whose parsed id equals the sent id | success (`ok`) |
| `503` with body whose parsed id equals the sent id | cancelled by the server through `/cancel` (`rejected`) |
| any other status, unparseable body, or id mismatch | `error` |

## Status classification (load canceller)

| Received for `/cancel` | Meaning for the canceller |
|---|---|
| `202` | `accepted` |
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
- `BuildBody` panics if `k < 0`. This cannot happen once `ValidateBodySize` has passed.
- Tests: `protocol_test.go` checks every worked example and the parse rejection cases.

### Traceability

Generated from the `// Design:` tags in the code by `implementation/tracegen/tracegen.py` (see `AGENTS.md`). Each link points at the first code line after the tag.
Regenerate from the repository root with `python3 implementation/tracegen/tracegen.py --lang Go --ext .go --comment // --write`.

<!-- trace:begin -->
| Design subsection | Implementation (file:line) |
|---|---|
| [RequestId](#requestid) | [protocol.go:35](../../implementation/go/internal/protocol/protocol.go#L35), [protocol.go:42](../../implementation/go/internal/protocol/protocol.go#L42), [protocol_test.go:7](../../implementation/go/internal/protocol/protocol_test.go#L7) |
| [PaddedJsonBody](#paddedjsonbody) | [protocol.go:49](../../implementation/go/internal/protocol/protocol.go#L49), [protocol.go:54](../../implementation/go/internal/protocol/protocol.go#L54), [protocol_test.go:29](../../implementation/go/internal/protocol/protocol_test.go#L29) |
| [Minimum body size](#minimum-body-size) | [protocol.go:74](../../implementation/go/internal/protocol/protocol.go#L74), [protocol_test.go:58](../../implementation/go/internal/protocol/protocol_test.go#L58) |
| [CancelBody](#cancelbody) | [protocol.go:84](../../implementation/go/internal/protocol/protocol.go#L84), [protocol_test.go:50](../../implementation/go/internal/protocol/protocol_test.go#L50) |
| [Parsing](#parsing) | [protocol.go:92](../../implementation/go/internal/protocol/protocol.go#L92), [protocol_test.go:65](../../implementation/go/internal/protocol/protocol_test.go#L65) |
| [Status classification (load-generator client)](#status-classification-load-generator-client) | [client.go:118](../../implementation/go/internal/loadgen/client.go#L118) |
| [Status classification (load canceller)](#status-classification-load-canceller) | [canceller.go:280](../../implementation/go/internal/canceller/canceller.go#L280) |
<!-- trace:end -->
