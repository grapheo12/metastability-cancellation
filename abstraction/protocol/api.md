# Protocol — API

## Constants

| Name | Value | Meaning |
|---|---|---|
| `PING_PATH` | `/ping` | Only route |
| `PING_METHOD` | `POST` | Only allowed method |
| `CONTENT_TYPE_JSON` | `application/json` | Content type of request and response bodies |
| `STATUS_OK` | `202` | Request processed |
| `STATUS_REJECTED` | `503` | Reserved: request refused. Never sent in v1 |
| `STATUS_BAD_REQUEST` | `400` | Body could not be parsed. Empty body |
| `STATUS_NOT_FOUND` | `404` | Path other than `/ping`. Empty body |
| `STATUS_METHOD_NOT_ALLOWED` | `405` | `/ping` with a method other than `POST`. Empty body |
| `RST_CODE_CANCEL` | `0x8` | HTTP/2 error code the client uses in `RST_STREAM` to cancel |
| `MIN_BODY_BYTES` | `42` | Smallest allowed `request_bytes` / `response_bytes` |

## Functions

### `make_request_id(seq: u32, client_id: u32) -> u64`
Returns `(seq << 32) | client_id`. See [design.md#requestid](design.md#requestid).

### `split_request_id(id: u64) -> (seq: u32, client_id: u32)`
Inverse of `make_request_id`.

### `build_body(id: u64, total_bytes: integer) -> bytes`
Returns a PaddedJsonBody whose length is exactly `total_bytes`. Precondition:
`total_bytes >= MIN_BODY_BYTES`. Used for both requests and responses. See
[design.md#paddedjsonbody](design.md#paddedjsonbody).

### `parse_body(body: bytes) -> u64 | ParseError`
Pulls the RequestId out of a request or response body. See [design.md#parsing](design.md#parsing).

### `validate_body_size(total_bytes: integer) -> ok | ConfigError`
Returns `ConfigError` if `total_bytes < MIN_BODY_BYTES`. Both programs call this at startup.

## Request headers (client → server)

| Header | Value |
|---|---|
| `:method` | `POST` |
| `:path` | `/ping` |
| `:scheme` | `http` |
| `:authority` | the `target` parameter |
| `content-type` | `application/json` |
| `content-length` | decimal byte length of the body |

## Response headers (server → client)

| Header | Value |
|---|---|
| `:status` | `202` (or `400`/`404`/`405`. `503` is reserved) |
| `content-type` | `application/json` (only when the body is non-empty) |
| `content-length` | decimal byte length of the body (`0` when empty) |
