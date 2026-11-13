# Run Controller — API

## Functions

### `main(params) -> exit status`

Entry point of the load generator program. Takes the parameters in
[../api.md#program-parameters](../api.md#program-parameters). Exit status is as in
[../api.md#exit-status](../api.md#exit-status). See [design.md#startup](design.md#startup),
[design.md#drainphase](design.md#drainphase) and [design.md#shutdown-ordering](design.md#shutdown-ordering).

### `validate(params) -> LoadGenConfig | ConfigError`

See [design.md#configuration-validation](design.md#configuration-validation).

## Uses

- [../client/api.md](../client/api.md): `run_client`
- [../metrics/api.md](../metrics/api.md): `aggregate`, `write_requests_csv`, `write_summary_json`
- [../../sampling/api.md](../../sampling/api.md): `derive_seed`, `rng_new`
- [../../protocol/api.md](../../protocol/api.md): `validate_request_body_size`
