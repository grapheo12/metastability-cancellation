// Source: abstraction/server/sleeper/design.md (sleeper v0.1.1)
package server

import (
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock replays the worked examples: sleep advances time by the quantum
// asked for, plus an optional lateness for the n-th quantum; events fire when
// the clock passes their time.
type fakeClock struct {
	t       int64
	quanta  []int64          // quanta asked for, ns
	late    map[int]int64    // quantum index -> extra ns
	events  map[int64]func() // fire at or after this time, once
	checked []int64          // times idCheck ran
}

func (c *fakeClock) now() int64 { return c.t }
func (c *fakeClock) sleep(ns int64) {
	c.t += ns + c.late[len(c.quanta)]
	c.quanta = append(c.quanta, ns)
	for at, f := range c.events {
		if c.t >= at {
			f()
			delete(c.events, at)
		}
	}
}

const ms = int64(time.Millisecond)

// Design: abstraction/server/sleeper/design.md#quantumsleep
func TestQuantumSleepWorkedExamples(t *testing.T) {
	type tc struct {
		name     string
		durUs    uint64
		rstAt    int64 // ns; -1 none
		cancelAt int64 // ns; -1 none
		late     map[int]int64
		quanta   []int64
		checks   []int64
		want     BurnOutcome
		endAt    int64
	}
	cases := []tc{
		{"50ms", 50000, -1, -1, nil, []int64{20 * ms, 20 * ms, 10 * ms}, []int64{20 * ms, 40 * ms}, BurnCompleted, 50 * ms},
		{"20ms one quantum", 20000, -1, -1, nil, []int64{20 * ms}, nil, BurnCompleted, 20 * ms},
		{"zero", 0, -1, -1, nil, nil, nil, BurnCompleted, 0},
		{"RST at 25ms", 100000, 25 * ms, -1, nil, []int64{20 * ms, 20 * ms}, []int64{20 * ms}, BurnCancelledRST, 40 * ms},
		{"cancel at 5ms", 100000, -1, 5 * ms, nil, []int64{20 * ms}, []int64{20 * ms}, BurnCancelledID, 20 * ms},
		{"RST in the last quantum", 30000, 25 * ms, -1, nil, []int64{20 * ms, 10 * ms}, []int64{20 * ms}, BurnCompleted, 30 * ms},
		{"late wake-up", 50000, -1, -1, map[int]int64{0: 3 * ms}, []int64{20 * ms, 20 * ms, 7 * ms}, []int64{23 * ms, 43 * ms}, BurnCompleted, 50 * ms},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var rst, cancelled atomic.Bool
			clk := &fakeClock{late: c.late, events: map[int64]func(){}}
			if c.rstAt >= 0 {
				clk.events[c.rstAt] = func() { rst.Store(true) }
			}
			if c.cancelAt >= 0 {
				clk.events[c.cancelAt] = func() { cancelled.Store(true) }
			}
			idCheck := func() bool { clk.checked = append(clk.checked, clk.t); return cancelled.Load() }
			got := quantumSleep(c.durUs, &rst, idCheck, clk.now, clk.sleep)
			if got != c.want || clk.t != c.endAt {
				t.Fatalf("outcome %v at %d ns, want %v at %d", got, clk.t, c.want, c.endAt)
			}
			if !equal(clk.quanta, c.quanta) {
				t.Fatalf("quanta %v, want %v", clk.quanta, c.quanta)
			}
			// RST cases stop before idCheck at the wake-up that sees the flag.
			if !equal(clk.checked, c.checks) {
				t.Fatalf("id checks at %v, want %v", clk.checked, c.checks)
			}
		})
	}
}

func equal(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Real clock: Sleep blocks for at least the duration and, with no sources,
// always completes.
//
// Design: abstraction/server/sleeper/design.md#timing
func TestSleepRealClock(t *testing.T) {
	start := time.Now()
	if got := Sleep(50000, nil, nil); got != BurnCompleted {
		t.Fatalf("got %v", got)
	}
	if d := time.Since(start); d < 50*time.Millisecond || d > 500*time.Millisecond {
		t.Fatalf("slept %v", d)
	}
	var rst atomic.Bool
	go func() { time.Sleep(30 * time.Millisecond); rst.Store(true) }()
	start = time.Now()
	if got := Sleep(5_000_000, &rst, nil); got != BurnCancelledRST {
		t.Fatalf("got %v", got)
	}
	if d := time.Since(start); d < 30*time.Millisecond || d > time.Second {
		t.Fatalf("RST seen after %v", d)
	}
}
