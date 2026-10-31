# Run Controller — API

## `main(params) -> exit status`

Entry point of the load generator program. Takes the parameters in
[../api.md#program-parameters](../api.md#program-parameters).

## `validate(params) -> LoadGenConfig | ConfigError`

See [design.md#configuration-validation](design.md#configuration-validation).

## Uses

- [../client/api.md](../client/api.md): `run_client`
- [../metrics/api.md](../metrics/api.md): `aggregate`, `write_requests_csv`, `write_summary_json`
- [../../sampling/api.md](../../sampling/api.md): `derive_seed`, `rng_new`
- [../../protocol/api.md](../../protocol/api.md): `validate_body_size`
