# Sampling — API

## Constants

Kind tags for [design.md#seedderivation](design.md#seedderivation):

| Name | Value | Use |
|---|---|---|
| `STREAM_CLIENT_ARRIVALS` | `1` | Kind tag for load-generator client arrival streams |
| `STREAM_SERVER_CONNECTION` | `2` | Kind tag for server per-connection ServiceTime streams |
| `STREAM_CANCELLER` | `3` | Kind tag for the load canceller's single stream (index `0`) |
| `STREAM_INGRESS_LB` | `4` | Kind tag for an Ingress LoadBalancer's single stream (index `0`), used by the `power_of_two` policy |
| `STREAM_SERVER_MESSAGES` | `5` | Kind tag for a server request's MessageTree stream, derived twice (client id, then seq) |
| `STREAM_SERVER_PREFILL` | `6` | Kind tag for the server's single MessageBuffer fill stream (index `0`) |

## Types

### `Rng`
Opaque generator state (xoshiro256**, 256 bits). It is not shared between concurrent activities.
Each client, each server connection and the canceller own their own `Rng`. See
[design.md#generator](design.md#generator).

## Functions

### `derive_seed(base_seed: u64, kind: u32, index: u32) -> u64`
Deterministic per-stream seed. See [design.md#seedderivation](design.md#seedderivation).

### `mix64(z: u64) -> u64`
SplitMix64 finalizer. A bijective 64-bit mixer, also used for hashing (e.g. by the server's
CancelRegistry). See [design.md#splitmix64](design.md#splitmix64).

### `rng_new(seed: u64) -> Rng`
Creates a generator. See [design.md#generator](design.md#generator).

### `next_u64(rng) -> u64`
Next raw 64-bit output. See [design.md#generator](design.md#generator).

### `next_unit(rng) -> float in [0, 1)`
Uniform double. See [design.md#uniform](design.md#uniform).

### `exponential_seconds(rng, rate: float > 0) -> float >= 0`
One sample of `Exp(rate)` in seconds. See [design.md#exponentialsampler](design.md#exponentialsampler).

### `exponential_us(rng, rate) -> integer >= 0`
`exponential_seconds` rounded to whole microseconds (ServiceTime). See
[design.md#exponentialsampler](design.md#exponentialsampler).

### `exponential_ns(rng, rate) -> integer >= 0`
`exponential_seconds` rounded to whole nanoseconds (inter-arrival gaps). See
[design.md#exponentialsampler](design.md#exponentialsampler).

### `uniform_int(rng, lo: u64, hi: u64) -> u64 in [lo, hi]`
Unbiased integer from the closed range. Precondition: `lo <= hi`. It may use more than one
`next_u64`. See [design.md#uniformintsampler](design.md#uniformintsampler).
