// Package sampling provides reproducible, language-independent randomness:
// SplitMix64 seeding, xoshiro256**, and exponential / uniform-integer samplers.
//
// Source: abstraction/sampling/design.md (sampling v0.2.0)
package sampling

import (
	"math"
	"math/bits"
)

// API: abstraction/sampling/api.md#constants
const (
	StreamClientArrivals   uint32 = 1
	StreamServerConnection uint32 = 2
	StreamCanceller        uint32 = 3
)

// Design: abstraction/sampling/design.md#splitmix64
const golden uint64 = 0x9E3779B97F4A7C15

// Mix64 is the SplitMix64 finalizer (all arithmetic wraps modulo 2^64).
//
// Design: abstraction/sampling/design.md#splitmix64
func Mix64(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// Design: abstraction/sampling/design.md#splitmix64
func splitmixNext(state *uint64) uint64 {
	*state += golden
	return Mix64(*state)
}

// DeriveSeed gives each (kind, index) stream its own seed from one base seed.
//
// Design: abstraction/sampling/design.md#seedderivation
func DeriveSeed(baseSeed uint64, kind, index uint32) uint64 {
	tag := uint64(kind)<<32 | uint64(index)
	return Mix64(baseSeed ^ Mix64(tag+golden))
}

// Rng is a xoshiro256** generator. It must not be shared between goroutines.
//
// Design: abstraction/sampling/design.md#generator
type Rng struct {
	s [4]uint64
}

// NewRng seeds the four state words from a SplitMix64 sequence.
//
// Design: abstraction/sampling/design.md#generator
func NewRng(seed uint64) *Rng {
	r := &Rng{}
	sm := seed
	for j := range r.s {
		r.s[j] = splitmixNext(&sm)
	}
	return r
}

// NextU64 returns the next raw 64-bit output.
//
// Design: abstraction/sampling/design.md#generator
func (r *Rng) NextU64() uint64 {
	s := &r.s
	result := bits.RotateLeft64(s[1]*5, 7) * 9
	t := s[1] << 17
	s[2] ^= s[0]
	s[3] ^= s[1]
	s[1] ^= s[2]
	s[0] ^= s[3]
	s[2] ^= t
	s[3] = bits.RotateLeft64(s[3], 45)
	return result
}

// NextUnit returns a uniform float64 in [0, 1) built from the top 53 bits.
//
// Design: abstraction/sampling/design.md#uniform
func (r *Rng) NextUnit() float64 {
	return float64(r.NextU64()>>11) * (1.0 / (1 << 53))
}

// ExponentialSeconds draws Exp(rate) by inverse CDF using exactly one NextU64.
//
// Design: abstraction/sampling/design.md#exponentialsampler
func (r *Rng) ExponentialSeconds(rate float64) float64 {
	u := r.NextUnit()
	return -math.Log(1-u) / rate
}

// ExponentialUs is ExponentialSeconds rounded half-up to whole microseconds.
//
// Design: abstraction/sampling/design.md#exponentialsampler
func (r *Rng) ExponentialUs(rate float64) uint64 {
	return uint64(math.Floor(r.ExponentialSeconds(rate)*1e6 + 0.5))
}

// ExponentialNs is ExponentialSeconds rounded half-up to whole nanoseconds.
//
// Design: abstraction/sampling/design.md#exponentialsampler
func (r *Rng) ExponentialNs(rate float64) uint64 {
	return uint64(math.Floor(r.ExponentialSeconds(rate)*1e9 + 0.5))
}

// UniformInt returns an unbiased integer in [lo, hi] by rejection sampling.
// It may consume more than one NextU64.
//
// Design: abstraction/sampling/design.md#uniformintsampler
func (r *Rng) UniformInt(lo, hi uint64) uint64 {
	span := hi - lo + 1 // wraps to 0 when [lo, hi] is all of u64
	if span == 0 {
		return r.NextU64()
	}
	threshold := (0 - span) % span // equals 2^64 mod span
	for {
		x := r.NextU64()
		if x >= threshold {
			return lo + x%span
		}
	}
}
