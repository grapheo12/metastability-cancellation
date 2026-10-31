// Package loadgen implements the load generator: one file per component of
// abstraction/load_generator.
//
// Source: abstraction/load_generator/design.md (load_generator v0.1.0)
package loadgen

import (
	"math"
	"time"
)

// Config is the validated LoadGenConfig plus its derived values.
//
// API: abstraction/load_generator/api.md#loadgenconfig
type Config struct {
	Target        string
	NumClients    uint32
	GlobalRate    float64
	RequestBytes  int
	SloMs         float64
	DurationS     float64
	Seed          uint64
	OutputPath    string
	PerClientRate float64 // global_rate / N
	SloNs         int64   // round(slo_ms * 1e6)
	DurationNs    int64   // round(duration_s * 1e9)
}

// Params holds the raw command-line values, named after the API parameters.
type Params struct {
	Target       string
	NumClients   string
	GlobalRate   string
	RequestBytes string
	SloMs        string
	DurationS    string
	Seed         string
	OutputPath   string
}

// DefaultParams returns the defaults from abstraction/load_generator/api.md#program-parameters.
func DefaultParams() Params {
	return Params{Target: "127.0.0.1:8080", Seed: "0", OutputPath: "results"}
}

// nowNs is monotonic nanoseconds since runStart (time.Time keeps a monotonic reading).
//
// Design: abstraction/load_generator/design.md#time-base
func nowNs(runStart time.Time) int64 {
	return int64(time.Since(runStart))
}

// sleepUntil blocks until nowNs(runStart) >= t; it returns at once if t is past.
//
// Design: abstraction/load_generator/design.md#time-base
func sleepUntil(runStart time.Time, t int64) {
	if d := t - nowNs(runStart); d > 0 {
		time.Sleep(time.Duration(d))
	}
}

// addGap adds an exponential gap to a schedule time, saturating instead of
// overflowing int64 for extreme gaps.
func addGap(t int64, gap uint64) int64 {
	if gap >= uint64(math.MaxInt64-t) {
		return math.MaxInt64
	}
	return t + int64(gap)
}
