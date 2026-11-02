# Sampling — Design

All integer arithmetic below is on **unsigned 64-bit integers, wrapping modulo 2^64**. `rotl(x, r)`
is a left bit-rotation of a 64-bit value by `r` bits. `>>` is a logical shift.

## SplitMix64

Used for seeding and seed derivation.

```
GOLDEN = 0x9E3779B97F4A7C15

function mix64(z):
    z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
    z = (z ^ (z >> 27)) * 0x94D049BB133111EB
    return z ^ (z >> 31)

function splitmix_next(state):          # state is mutated
    state = state + GOLDEN
    return mix64(state)
```

## SeedDerivation

```
function derive_seed(base_seed, kind, index):
    tag = (kind << 32) | index          # kind and index are u32, widened to u64
    return mix64(base_seed ^ mix64(tag + GOLDEN))
```

- Load-generator client `i` uses `derive_seed(seed, STREAM_CLIENT_ARRIVALS, i)`.
- Server connection number `c` (accept order, starting at 0) uses
  `derive_seed(seed, STREAM_SERVER_CONNECTION, c)`. Server reproducibility therefore depends on
  connections being accepted in the same order.
- The load canceller uses `derive_seed(seed, STREAM_CANCELLER, 0)`.
- An Ingress LoadBalancer uses `derive_seed(seed, STREAM_INGRESS_LB, 0)`. Its stream is separate from the
  canceller's, so the canceller's draws do not depend on how many requests were balanced.

## Generator

The generator is xoshiro256** with 256 bits of state `s[0..3]`.

```
function rng_new(seed):
    sm = seed
    for j in 0..3: s[j] = splitmix_next(sm)
    return Rng(s)

function next_u64(rng):
    result = rotl(s[1] * 5, 7) * 9
    t = s[1] << 17
    s[2] = s[2] ^ s[0]
    s[3] = s[3] ^ s[1]
    s[1] = s[1] ^ s[2]
    s[0] = s[0] ^ s[3]
    s[2] = s[2] ^ t
    s[3] = rotl(s[3], 45)
    return result
```

## Uniform

```
function next_unit(rng):
    return (next_u64(rng) >> 11) * 2^-53       # 53-bit mantissa, result in [0, 1)
```

## ExponentialSampler

Inverse-CDF method. Using `1 - u` keeps the logarithm's argument in `(0, 1]`, so it is never `ln(0)`.

```
function exponential_seconds(rng, rate):
    u = next_unit(rng)
    return -ln(1 - u) / rate

function exponential_us(rng, rate):
    return floor(exponential_seconds(rng, rate) * 1e6 + 0.5)

function exponential_ns(rng, rate):
    return floor(exponential_seconds(rng, rate) * 1e9 + 0.5)
```

Each call uses exactly **one** `next_u64`. Callers must sample in a fixed order (see the
callers' designs) so that sequences are reproducible.

## UniformIntSampler

Rejection sampling over the full 64-bit output. It is unbiased for every range. Arithmetic is on
unsigned 64-bit, wrapping, and `mod` is the unsigned remainder.

```
function uniform_int(rng, lo, hi):
    range = hi - lo + 1                     # wraps to 0 when [lo, hi] is all of u64
    if range == 0:
        return next_u64(rng)
    threshold = (0 - range) mod range       # equals 2^64 mod range
    loop:
        x = next_u64(rng)
        if x >= threshold:
            return lo + (x mod range)
```

The accepted values `[threshold, 2^64)` number `2^64 - (2^64 mod range)`, which is a multiple of
`range`, so every result is equally likely. Each call uses **one or more** `next_u64` draws. The
expected number is below 2.

Worked examples:

| lo | hi | range | threshold | Note |
|---|---|---|---|---|
| 1 | 4 | 4 | 0 | Power of two: never rejects, result `1 + (x mod 4)` |
| 0 | 2 | 3 | 1 | `2^64 mod 3 = 1`: rejects only `x = 0` |
| 0 | 0 | 1 | 0 | Always returns `0` and still uses one draw |
