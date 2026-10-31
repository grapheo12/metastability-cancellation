# Protocol — API

## Constants

| Name | Value | Meaning | Defined in |
|---|---|---|---|
| `PING_PATH` | `/ping` | Work route | [../api.md#wire-contract-ping](../api.md#wire-contract-ping) |
| `PING_METHOD` | `POST` | Only allowed method on `/ping` | [../api.md#wire-contract-ping](../api.md#wire-contract-ping) |
| `CANCEL_PATH` | `/cancel` | Cancellation route | [../api.md#wire-contract-cancel](../api.md#wire-contract-cancel) |
| `CANCEL_METHOD` | `DELETE` | Only allowed method on `/cancel` | [../api.md#wire-contract-cancel](../api.md#wire-contract-cancel) |
| `CONTENT_TYPE_JSON` | `application/json` | Content type of every non-empty body | [design.md#paddedjsonbody](design.md#paddedjsonbody), [design.md#cancelbody](design.md#cancelbody) |
| `STATUS_OK` | `202` | `/ping`: work completed. `/cancel`: id accepted | [design.md#status-classification-load-generator-client](design.md#status-classification-load-generator-client), [design.md#status-classification-load-canceller](design.md#status-classification-load-canceller) |
| `STATUS_REJECTED` | `503` | `/ping`: work cancelled by `/cancel` (padded body). `/cancel`: registry full (empty body) | [design.md#status-classification-load-generator-client](design.md#status-classification-load-generator-client), [design.md#status-classification-load-canceller](design.md#status-classification-load-canceller) |
| `STATUS_BAD_REQUEST` | `400` | Body could not be parsed, or a `/ping` client id differs from the connection's. Empty body | [design.md#parsing](design.md#parsing), [design.md#status-classification-load-canceller](design.md#status-classification-load-canceller) |
| `STATUS_NOT_FOUND` | `404` | Path other than `/ping` and `/cancel`. Empty body | [../api.md#wire-contract-ping](../api.md#wire-contract-ping) |
| `STATUS_METHOD_NOT_ALLOWED` | `405` | Wrong method for the path. Empty body | [../api.md#wire-contract-ping](../api.md#wire-contract-ping), [../api.md#wire-contract-cancel](../api.md#wire-contract-cancel) |
| `RST_CODE_CANCEL` | `0x8` | HTTP/2 error code the client uses in `RST_STREAM` to cancel | [../load_generator/client/design.md#slocancellation](../load_generator/client/design.md#slocancellation) |
| `MIN_BODY_BYTES` | `42` | Smallest allowed `request_bytes` / `response_bytes` | [design.md#minimum-body-size](design.md#minimum-body-size) |

## Functions

### `make_request_id(seq: u32, client_id: u32) -> u64`
Returns `(client_id << 32) | seq`. See [design.md#requestid](design.md#requestid).

### `split_request_id(id: u64) -> (seq: u32, client_id: u32)`
Inverse of `make_request_id`: `client_id = id >> 32`, `seq = id & 0xFFFFFFFF`. See
[design.md#requestid](design.md#requestid).

### `build_body(id: u64, total_bytes: integer) -> bytes`
Returns a PaddedJsonBody whose length is exactly `total_bytes`. Precondition:
`total_bytes >= MIN_BODY_BYTES`. Used for `/ping` requests and responses (both `202` and `503`). See
[design.md#paddedjsonbody](design.md#paddedjsonbody).

### `build_cancel_body(id: u64) -> bytes`
Returns the CancelBody `{"id":"<dec>"}`. See [design.md#cancelbody](design.md#cancelbody).

### `parse_body(body: bytes) -> u64 | ParseError`
Pulls the RequestId out of a PaddedJsonBody or a CancelBody. See [design.md#parsing](design.md#parsing).

### `validate_body_size(total_bytes: integer) -> ok | ConfigError`
Returns `ConfigError` if `total_bytes < MIN_BODY_BYTES`. The server and the load generator call
this at startup. See [design.md#minimum-body-size](design.md#minimum-body-size).

## Request headers (client or canceller → server)

| Header | Value |
|---|---|
| `:method` | `POST` (for `/ping`) or `DELETE` (for `/cancel`) |
| `:path` | `/ping` or `/cancel` |
| `:scheme` | `http` |
| `:authority` | the `target` parameter |
| `content-type` | `application/json` |
| `content-length` | decimal byte length of the body |

## Response headers (server → client or canceller)

| Header | Value |
|---|---|
| `:status` | see the status constants above |
| `content-type` | `application/json` (only when the body is non-empty) |
| `content-length` | decimal byte length of the body (`0` when empty) |
