# Sampling — API

## Constants

| Name | Value | Use |
|---|---|---|
| `STREAM_CLIENT_ARRIVALS` | `1` | Kind tag for load-generator client arrival streams |
| `STREAM_SERVER_CONNECTION` | `2` | Kind tag for server per-connection ServiceTime streams |

## Types

### `Rng`
Opaque generator state. It is not shared between concurrent activities. Each client and each
server connection owns its own `Rng`.

## Functions

### `derive_seed(base_seed: u64, kind: u32, index: u32) -> u64`
Deterministic per-stream seed. See [design.md#seedderivation](design.md#seedderivation).

### `rng_new(seed: u64) -> Rng`
Creates a generator. See [design.md#generator](design.md#generator).

### `next_u64(rng) -> u64`
Next raw 64-bit output.

### `next_unit(rng) -> float in [0, 1)`
Uniform double. See [design.md#uniform](design.md#uniform).

### `exponential_seconds(rng, rate: float > 0) -> float >= 0`
One sample of `Exp(rate)` in seconds. See [design.md#exponentialsampler](design.md#exponentialsampler).

### `exponential_us(rng, rate) -> integer >= 0`
`exponential_seconds` rounded to whole microseconds (ServiceTime).

### `exponential_ns(rng, rate) -> integer >= 0`
`exponential_seconds` rounded to whole nanoseconds (inter-arrival gaps).
