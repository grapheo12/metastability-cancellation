# Protocol — Design

## RequestId

A RequestId is an unsigned 64-bit integer:

```
bits 63..32 : seq        (u32) per-client counter, starts at 0, +1 for each scheduled arrival
bits 31..0  : client_id  (u32) index of the client, 0 ..= N-1
```

```
function make_request_id(seq, client_id):
    return (seq << 32) | client_id          # all arithmetic is on unsigned 64-bit

function split_request_id(id):
    return (id >> 32, id & 0xFFFFFFFF)
```

In JSON the id is written as a **decimal string** (for example `"4294967299"`), never as a JSON
number, because values above 2^53 lose precision in many JSON libraries. The decimal form has no
leading zeros, no sign, and between 1 and 20 digits.

Worked example: `seq = 1`, `client_id = 3` gives `id = 4294967296 + 3 = 4294967299`.

## PaddedJsonBody

Request and response bodies use the same exact serialization. There is no whitespace, the keys
appear in this order, and everything is ASCII:

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
| `4294967299` | 10 | 128 | 96 | 22 + 10 + 96 = 128 |
| `18446744073709551615` (max) | 20 | 42 | 0 | 22 + 20 + 0 = 42 |

## Minimum body size

The longest possible `<DEC>` has 20 digits, so `MIN_BODY_BYTES = 22 + 20 = 42`. Any configured size
of at least 42 can be padded exactly for every possible id.

```
function validate_body_size(total_bytes):
    if total_bytes < 42: return ConfigError("body size must be >= 42 bytes")
    return ok
```

## Parsing

Parsing is lenient. It accepts any valid JSON, not only our exact serialization, so other clients
can talk to the server.

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

## Status classification (client side)

| Received | Meaning for the client |
|---|---|
| `202` with body whose parsed id equals the sent id | success (`ok`) |
| `503` with body whose parsed id equals the sent id | rejected (`rejected`) |
| any other status, unparseable body, or id mismatch | `error` |
