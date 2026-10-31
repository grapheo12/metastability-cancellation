// Source: abstraction/sampling/design.md (sampling v0.2.0)
package sampling

import (
	"math"
	"testing"
)

// Design: abstraction/sampling/design.md#generator
func TestXoshiroReferenceStep(t *testing.T) {
	r := &Rng{s: [4]uint64{1, 2, 3, 4}}
	// rotl(2*5, 7) * 9 = 1280 * 9
	if got := r.NextU64(); got != 11520 {
		t.Fatalf("first output %d want 11520", got)
	}
}

// Design: abstraction/sampling/design.md#seedderivation
func TestDeterminismAndDerivation(t *testing.T) {
	a, b := NewRng(DeriveSeed(7, StreamClientArrivals, 3)), NewRng(DeriveSeed(7, StreamClientArrivals, 3))
	for i := 0; i < 100; i++ {
		if a.NextU64() != b.NextU64() {
			t.Fatal("same seed must give the same sequence")
		}
	}
	if DeriveSeed(7, StreamClientArrivals, 3) == DeriveSeed(7, StreamServerConnection, 3) {
		t.Fatal("different kinds must give different seeds")
	}
}

// Design: abstraction/sampling/design.md#uniformintsampler
func TestUniformInt(t *testing.T) {
	r := NewRng(1)
	seen := map[uint64]int{}
	for i := 0; i < 40000; i++ {
		v := r.UniformInt(1, 4)
		if v < 1 || v > 4 {
			t.Fatalf("out of range %d", v)
		}
		seen[v]++
	}
	for v := uint64(1); v <= 4; v++ {
		if seen[v] < 9000 || seen[v] > 11000 {
			t.Fatalf("value %d drawn %d times", v, seen[v])
		}
	}
	if r.UniformInt(0, 0) != 0 {
		t.Fatal("degenerate range must return lo")
	}
	three, four := uint64(3), uint64(4)
	if (0-three)%three != 1 || (0-four)%four != 0 {
		t.Fatal("threshold worked examples")
	}
}

// Design: abstraction/sampling/design.md#exponentialsampler
func TestExponentialMean(t *testing.T) {
	r := NewRng(42)
	const n, rate = 200000, 1000.0
	sum := 0.0
	for i := 0; i < n; i++ {
		sum += r.ExponentialSeconds(rate)
	}
	if mean := sum / n; math.Abs(mean-1/rate) > 0.02/rate {
		t.Fatalf("mean %g want ~%g", mean, 1/rate)
	}
}
