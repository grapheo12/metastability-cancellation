// Source: abstraction/server/cpu_burner/design.md (cpu_burner v0.2.1)
package server

import (
	"sync/atomic"
	"time"
)

// BurnOutcome is the result of Burn.
type BurnOutcome int

const (
	BurnCompleted BurnOutcome = iota
	BurnCancelledRST
	BurnCancelledID
)

// Design: abstraction/server/cpu_burner/design.md#spin-unit
const spinUnitIterations = 64

// clockBase carries Go's monotonic clock reading; time.Since uses it, so
// monotonicNowNs is immune to wall-clock adjustments.
//
// Design: abstraction/server/cpu_burner/design.md#timing
var clockBase = time.Now()

// Design: abstraction/server/cpu_burner/design.md#timing
func monotonicNowNs() int64 {
	return int64(time.Since(clockBase))
}

// Burn spins for durationUs of wall-clock time. A nil cancelFlag or idCheck
// disables that source. Precedence per iteration: deadline, RST, id.
//
// Design: abstraction/server/cpu_burner/design.md#cooperativecpuburn
func Burn(durationUs uint64, cancelFlag *atomic.Bool, checkIntervalUs uint64, idCheck func() bool, checkIter int) BurnOutcome {
	start := monotonicNowNs()
	deadline := start + int64(durationUs)*1000
	nextCheck := start + int64(checkIntervalUs)*1000
	units := 0 // spin units since the last id check
	for {
		t := monotonicNowNs()
		if t >= deadline {
			return BurnCompleted
		}
		if cancelFlag != nil && t >= nextCheck {
			if cancelFlag.Load() {
				return BurnCancelledRST
			}
			nextCheck += int64(checkIntervalUs) * 1000
		}
		if idCheck != nil && units >= checkIter {
			units = 0
			if idCheck() {
				return BurnCancelledID
			}
		}
		spinUnit()
		units++
	}
}

// sink makes the spin work observable so the compiler cannot remove it.
//
// Design: abstraction/server/cpu_burner/design.md#spin-unit
var sink atomic.Uint64

// Design: abstraction/server/cpu_burner/design.md#spin-unit
func spinUnit() {
	x := sink.Load()
	for i := 0; i < spinUnitIterations; i++ {
		x = x*6364136223846793005 + 1442695040888963407
	}
	sink.Store(x)
}
