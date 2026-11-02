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
```

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
