// Source: abstraction/server/ping_handler/design.md (ping_handler v0.11.0)
package server

import (
	"math"
	"testing"
	"time"

	"testbed/internal/protocol"
)

// Design: abstraction/server/ping_handler/design.md#execreport
func TestStartSlackWorkedExamples(t *testing.T) {
	const wall = int64(1794533185000000000)
	cases := []struct {
		deadline uint64
		want     int64
	}{
		{1794533190000000000, 5000000000},
		{1794533184965000000, -35000000},
		{1794533185000000000, 0},
		{0, -1794533185000000000},
		{NoDeadline - 1, math.MaxInt64}, // saturates
	}
	for _, c := range cases {
		if got := startSlack(c.deadline, wall); got != c.want {
			t.Errorf("startSlack(%d) = %d, want %d", c.deadline, got, c.want)
		}
	}
	if got := startSlack(1<<63, 0); got != math.MaxInt64 {
		t.Errorf("2^63 - 0 must saturate, got %d", got)
	}
}

// A sleep job's body carries exec_ns of about its ServiceTime and the StartSlack
// against its deadline: positive, negative, or empty without one.
//
// Design: abstraction/server/ping_handler/design.md#execreport
func TestExecReportInBody(t *testing.T) {
	now := uint64(time.Now().UnixNano())
	cases := []struct {
		name     string
		deadline uint64
		slackOK  bool
		lo, hi   int64
	}{
		{"far deadline", now + 5_000_000_000, true, 3_000_000_000, 5_000_000_000},
		{"past deadline", now - 1_000_000_000, true, -3_000_000_000, -1_000_000_000},
		{"no deadline", NoDeadline, false, 0, 0},
	}
	for _, c := range cases {
		q := NewWorkQueue(0, QueueFIFO, 0)
		_ = q.Push(newPingJob(&StreamTask{}, protocol.MakeRequestID(0, 0), 0, 20_000, c.deadline)) // 20 ms
		job := q.Pop()
		cfg := Config{CancelMode: CancelCooperative, ResponseBytes: 128, Workload: WorkloadSleep}
		r := runPing(job, cfg, NewCancelRegistry(16), &CancelCounters{}, nil, q)
		ex, exOK, sl, slOK := protocol.ParseExecReport(r.Body)
		if r.Status != 202 || len(r.Body) != 128 || !exOK || ex < 20_000_000 || ex > 2_000_000_000 {
			t.Fatalf("%s: status %d, body %s", c.name, r.Status, r.Body)
		}
		if slOK != c.slackOK || (slOK && (sl < c.lo || sl > c.hi)) {
			t.Fatalf("%s: start_slack_ns %d %v, want in [%d, %d] (%v)", c.name, sl, slOK, c.lo, c.hi, c.slackOK)
		}
		q.Close()
	}
}
