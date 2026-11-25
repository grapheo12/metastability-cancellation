// Source: abstraction/server/sleeper/design.md (sleeper v0.1.1)
package server

import (
	"sync/atomic"
	"time"
)

// SleepQuantumUs is the longest single sleep; cancellations are checked only
// after each one.
//
// Design: abstraction/server/sleeper/design.md#quantumsleep
const SleepQuantumUs = 20000

// Sleep waits durationUs of wall-clock time without using CPU, in quanta of at
// most SleepQuantumUs against one monotonic deadline. A nil cancelFlag or
// idCheck disables that source. Precedence at a wake-up: deadline, RST, id.
//
// Design: abstraction/server/sleeper/design.md#quantumsleep
func Sleep(durationUs uint64, cancelFlag *atomic.Bool, idCheck func() bool) BurnOutcome {
	return quantumSleep(durationUs, cancelFlag, idCheck, monotonicNowNs, sleepNs)
}

// sleepNs blocks the calling goroutine (and only it) for ns nanoseconds.
//
// Design: abstraction/server/sleeper/design.md#timing
func sleepNs(ns int64) { time.Sleep(time.Duration(ns)) }

// quantumSleep is Sleep with the clock and the sleep injected, so tests can
// replay the worked examples exactly.
//
// Design: abstraction/server/sleeper/design.md#quantumsleep
func quantumSleep(durationUs uint64, cancelFlag *atomic.Bool, idCheck func() bool,
	now func() int64, sleep func(ns int64)) BurnOutcome {
	deadline := now() + int64(durationUs)*1000
	for {
		t := now()
		if t >= deadline {
			return BurnCompleted
		}
		q := int64(SleepQuantumUs) * 1000
		if left := deadline - t; left < q {
			q = left
		}
		sleep(q) // one quantum: blocks, no CPU, no lock held
		if now() >= deadline {
			return BurnCompleted // the last wake-up does not check
		}
		if cancelFlag != nil && cancelFlag.Load() {
			return BurnCancelledRST
		}
		if idCheck != nil && idCheck() {
			return BurnCancelledID
		}
	}
}
