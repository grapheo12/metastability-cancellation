// Source: abstraction/server/ping_handler/design.md (ping_handler v0.13.0)
package server

import (
	"math"
	"testing"
	"time"

	"testbed/internal/protocol"
	"testbed/internal/sampling"
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

// The PastDeadlineRejection worked examples: popped past the deadline gives
// 503 and counts; at or before it, or without one, runs; RST still wins; ignore
// mode rejects too; reject_past_deadline = false never rejects.
//
// Design: abstraction/server/ping_handler/design.md#pastdeadlinerejection
func TestPastDeadlineRejectionWorkedExamples(t *testing.T) {
	now := uint64(time.Now().UnixNano())
	cases := []struct {
		name     string
		mode     CancelMode
		reject   bool
		deadline uint64
		rst      bool
		status   int // 0: Aborted
		counted  bool
	}{
		{"cooperative, 5 s left", CancelCooperative, true, now + 5_000_000_000, false, 202, false},
		{"cooperative, 1 s late", CancelCooperative, true, now - 1_000_000_000, false, 503, true},
		{"cooperative, late and reset: RST wins", CancelCooperative, true, now - 1_000_000_000, true, 0, false},
		{"cooperative, no deadline", CancelCooperative, true, NoDeadline, false, 202, false},
		{"ignore, 35 ms late", CancelIgnore, true, now - 35_000_000, false, 503, true},
		{"ignore, 5 s left", CancelIgnore, true, now + 5_000_000_000, false, 202, false},
		{"off: late runs", CancelCooperative, false, now - 1_000_000_000, false, 202, false},
	}
	for _, c := range cases {
		q := NewWorkQueue(0, QueueFIFO, 0)
		job := newPingJob(&StreamTask{}, protocol.MakeRequestID(0, 0), 0, 1_000, c.deadline) // 1 ms sleep
		job.Task.Cancelled.Store(c.rst)
		_ = q.Push(job)
		job = q.Pop()
		counters := &CancelCounters{}
		cfg := Config{CancelMode: c.mode, ResponseBytes: 128, Workload: WorkloadSleep, RejectPastDeadline: c.reject}
		r := runPing(job, cfg, NewCancelRegistry(16), counters, nil, q)
		if (c.status == 0) != r.Aborted || (c.status != 0 && r.Status != c.status) {
			t.Fatalf("%s: got %+v", c.name, r)
		}
		if got := counters.RejectedPastDeadline.Load(); (got == 1) != c.counted {
			t.Fatalf("%s: rejected_past_deadline = %d", c.name, got)
		}
		if c.counted {
			ex, exOK, sl, slOK := protocol.ParseExecReport(r.Body)
			if !exOK || ex > 50_000_000 || !slOK || sl >= 0 || len(r.Body) != 128 {
				t.Fatalf("%s: body %s", c.name, r.Body)
			}
		}
		q.Close()
	}
	// Exactly at the deadline (slack 0) runs: the test is strict.
	zero := int64(0)
	if pastDeadline(Config{RejectPastDeadline: true}, execReport{startSlackNs: &zero}) {
		t.Fatal("slack 0 must run")
	}
	minus := int64(-1)
	if !pastDeadline(Config{RejectPastDeadline: true}, execReport{startSlackNs: &minus}) ||
		pastDeadline(Config{RejectPastDeadline: false}, execReport{startSlackNs: &minus}) {
		t.Fatal("slack -1 must be rejected only when enabled")
	}
}

// Design: abstraction/server/design.md#configuration-validation
func TestValidateRejectPastDeadline(t *testing.T) {
	p := DefaultParams()
	p.ResponseRate, p.ResponseBytes = "100", "128"
	if c, err := Validate(p); err != nil || c.RejectPastDeadline {
		t.Fatalf("default: %+v %v", c, err)
	}
	p.RejectPastDeadline = "true"
	if c, err := Validate(p); err != nil || !c.RejectPastDeadline {
		t.Fatalf("true: %v", err)
	}
	for _, bad := range []string{"", "True", "1", "yes"} {
		p.RejectPastDeadline = bad
		if _, err := Validate(p); err == nil {
			t.Fatalf("reject_past_deadline %q must be rejected", bad)
		}
	}
}

// ConstantServiceTime worked example: round(1e6 / response_rate), half up.
//
// Design: abstraction/server/ping_handler/design.md#constantservicetime
func TestConstantServiceUs(t *testing.T) {
	for _, c := range []struct {
		rate float64
		want uint64
	}{{2.5, 400_000}, {3, 333_333}, {25, 40_000}, {1.6, 625_000}, {3_000_000, 0}} {
		if got := constantServiceUs(c.rate); got != c.want {
			t.Errorf("rate %v: %d, want %d", c.rate, got, c.want)
		}
	}
}

// sleep_constant admits every request with the ConstantServiceTime and draws
// nothing: the rng's next value is what it would have been without the admits.
//
// Design: abstraction/server/ping_handler/design.md#sampling-order
func TestAdmitSleepConstantNoDraw(t *testing.T) {
	cfg := Config{Workload: WorkloadSleepConstant, ResponseRate: 2.5}
	rng, ref := sampling.NewRng(9), sampling.NewRng(9)
	st := &ConnectionCancelState{}
	for seq := uint32(0); seq < 3; seq++ {
		job, resp := admitPing(&StreamTask{Body: protocol.BuildBody(protocol.MakeRequestID(seq, 1), 0, nil, 128)}, cfg, rng, st)
		if job == nil || resp.Status != 0 || job.ServiceUs != 400_000 {
			t.Fatalf("seq %d: job %+v resp %+v", seq, job, resp)
		}
	}
	if rng.ExponentialUs(1) != ref.ExponentialUs(1) {
		t.Fatal("sleep_constant advanced the rng")
	}
	// A 20 ms constant sleep runs as QuantumSleep and reports about 20 ms.
	q := NewWorkQueue(0, QueueFIFO, 0)
	_ = q.Push(newPingJob(&StreamTask{}, protocol.MakeRequestID(0, 0), 0, constantServiceUs(50), NoDeadline))
	r := runPing(q.Pop(), Config{CancelMode: CancelCooperative, ResponseBytes: 128, Workload: WorkloadSleepConstant},
		NewCancelRegistry(16), &CancelCounters{}, nil, q)
	if ex, ok, _, _ := protocol.ParseExecReport(r.Body); r.Status != 202 || !ok || ex < 20_000_000 || ex > 2_000_000_000 {
		t.Fatalf("status %d exec %d %v", r.Status, ex, ok)
	}
	q.Close()
}
