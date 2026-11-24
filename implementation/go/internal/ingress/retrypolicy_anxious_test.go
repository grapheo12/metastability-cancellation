// Source: abstraction/ingress/retry_policy/design.md (retry_policy v0.4.0)
package ingress

import (
	"testing"
	"time"
)

// trackerWith builds a tracker with B backends, count[b] entries on each (the
// entry under test gets TicketSeq 1000 and queue q on backend s), and the EWMAs
// of backend s.
func trackerWith(b int, count []int, s uint32, q uint64, exec, anti float64, hasExec, hasAnti bool) *anxiousTracker {
	tr := newAnxiousTracker(b)
	seq := uint64(0)
	for i, n := range count {
		for k := 0; k < n; k++ {
			if uint32(i) == s && k == 0 {
				tr.apply(RetryObservation{Kind: ObsAttemptSent, Seq: 1000, ServerID: s, QueueAtSend: q})
				continue
			}
			tr.apply(RetryObservation{Kind: ObsAttemptSent, Seq: seq, ServerID: uint32(i), QueueAtSend: 99})
			seq++
		}
	}
	x, a := make([]float64, b), make([]float64, b)
	hx, ha := make([]bool, b), make([]bool, b)
	x[s], a[s], hx[s], ha[s] = exec, anti, hasExec, hasAnti
	tr.apply(RetryObservation{Kind: ObsEwmaChanged, Exec: x, Anti: a, HasExec: hx, HasAnti: ha})
	return tr
}

// The anxious worked example, alpha = 0.3 (ms are written as ns: the ratio is
// unit-free).
//
// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
func TestAnxiousWorkedExample(t *testing.T) {
	cases := []struct {
		name             string
		b                int
		s                uint32
		q                uint64
		exec, anti       float64
		hasExec, hasAnti bool
		count            []int
		want             bool
	}{
		{"long queue", 2, 0, 4, 80, 80, true, true, []int{6, 2}, true},
		{"exactly alpha", 2, 0, 4, 80, 70, true, true, []int{5, 3}, true},
		{"short queue", 2, 0, 1, 80, 80, true, true, []int{2, 2}, false},
		{"three backends", 3, 0, 4, 100, 100, true, true, []int{5, 3, 1}, true},
		{"no sample", 2, 1, 9, 0, 80, false, true, []int{1, 10}, false},
		{"one backend", 1, 0, 9, 80, 0, true, false, []int{10}, false},
		{"other backend dead", 2, 0, 4, 80, 80, true, true, []int{6, 2}, false},
		{"three, one dead", 3, 0, 4, 100, 100, true, true, []int{5, 3, 1}, false},
	}
	dead := map[string]int{"other backend dead": 1, "three, one dead": 2}
	for _, c := range cases {
		tr := trackerWith(c.b, c.count, c.s, c.q, c.exec, c.anti, c.hasExec, c.hasAnti)
		if d, ok := dead[c.name]; ok {
			tr.live[d] = false
		}
		_, got := tr.anxiousSet(0.3)[1000]
		if got != c.want {
			t.Errorf("%s: anxious %v, want %v", c.name, got, c.want)
		}
	}
	// alpha = 0 retries when moving is no slower; alpha > 1 never.
	tr := trackerWith(2, []int{1, 0}, 0, 0, 80, 80, true, true) // current 80, retry (0/1+1)*80 = 80
	if _, ok := tr.anxiousSet(0)[1000]; !ok {
		t.Error("alpha 0, equal estimates: must retry")
	}
	if _, ok := tr.anxiousSet(1.5)[1000]; ok {
		t.Error("alpha > 1 must never retry")
	}
}

// The queue bookkeeping table: a finish lowers every other entry on its
// backend by one, floored at 0, and removes its own entry.
//
// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
func TestAnxiousQueueBookkeeping(t *testing.T) {
	tr := newAnxiousTracker(2)
	for seq, q := range map[uint64]uint64{9: 1, 10: 2, 11: 3} {
		tr.apply(RetryObservation{Kind: ObsAttemptSent, Seq: seq, ServerID: 0, QueueAtSend: q})
	}
	tr.apply(RetryObservation{Kind: ObsAttemptSent, Seq: 12, ServerID: 1, QueueAtSend: 5}) // other backend: untouched
	steps := []struct {
		finish uint64
		want   map[uint64]uint64
	}{
		{9, map[uint64]uint64{10: 1, 11: 2}},
		{10, map[uint64]uint64{11: 1}},
		{42, map[uint64]uint64{11: 0}},
		{43, map[uint64]uint64{11: 0}},
	}
	for i, st := range steps {
		tr.apply(RetryObservation{Kind: ObsAttemptFinished, Seq: st.finish, ServerID: 0})
		m := tr.entries[0]
		if len(m) != len(st.want) {
			t.Fatalf("step %d: count %d, want %d", i, len(m), len(st.want))
		}
		for seq, q := range st.want {
			if e, ok := m[seq]; !ok || e.queue != q {
				t.Fatalf("step %d: seq %d = %+v, want %d", i, seq, e, q)
			}
		}
	}
	if e := tr.entries[1][12]; e == nil || e.queue != 5 {
		t.Fatalf("backend 1 changed: %+v", e)
	}
}

// End to end through the policy: observations reach the tracker, a tick
// publishes the set, and decide retries only a running newest attempt on a
// Tick; after Stop, Observe returns at once.
//
// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
func TestAnxiousPolicyDecide(t *testing.T) {
	p := newAnxiousPolicy(0.3, 2, time.Millisecond)
	// Backend 0: TicketSeqs 0..5 with queues 0..5; backend 1 empty; both EWMAs 80.
	for q := uint64(0); q < 6; q++ {
		p.Observe(RetryObservation{Kind: ObsAttemptSent, Seq: q, ServerID: 0, QueueAtSend: q})
	}
	p.Observe(RetryObservation{Kind: ObsEwmaChanged, Exec: []float64{80, 80}, Anti: []float64{80, 80},
		HasExec: []bool{true, true}, HasAnti: []bool{true, true}})
	// retry = (0/1 + 1) * 80 = 80; current = (q+1)*80: q = 0 gives ratio 0, q >= 1 gives >= 0.5.
	state := func(seq uint64, running bool) RetryState {
		return RetryState{MaxRetries: 1, Attempts: []AttemptView{{TicketSeq: seq, Running: running}}}
	}
	deadline := time.Now().Add(2 * time.Second)
	for p.Decide(state(5, true), RetryEvent{Kind: EventTick}) != RetryElsewhere {
		if time.Now().After(deadline) {
			t.Fatal("seq 5 never became anxious")
		}
		time.Sleep(time.Millisecond)
	}
	if p.Decide(state(0, true), RetryEvent{Kind: EventTick}) != Wait {
		t.Error("queue 0, ratio 0 < 0.3: must wait")
	}
	if p.Decide(state(5, false), RetryEvent{Kind: EventTick}) != Wait {
		t.Error("newest not running: must wait")
	}
	if p.Decide(state(5, true), RetryEvent{Kind: EventAttemptEnded, Outcome: OutcomeBadGateway}) != Wait {
		t.Error("AttemptEnded: must wait")
	}
	p.Stop()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 2*AnxiousBuffer; i++ { // more than the channel holds: must not block
			p.Observe(RetryObservation{Kind: ObsAttemptSent, Seq: uint64(100 + i)})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Observe blocked after Stop")
	}
	p.Stop() // idempotent
}

// The registry builds anxious from lb_retry_anxiety_ratio; validation rejects
// a negative or non-finite ratio and defaults to 0.3.
//
// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
func TestAnxiousRegistryAndConfig(t *testing.T) {
	p := DefaultParams()
	p.Backends, p.NumClients, p.LbRetryPolicy = "a:1,b:2", "1", RetryPolicyAnxious
	c, err := Validate(p)
	if err != nil || c.LbRetryAnxietyRatio != 0.3 {
		t.Fatalf("default: %v %v", c.LbRetryAnxietyRatio, err)
	}
	pol, err := NewRetryPolicy(RetryPolicyAnxious, c)
	if err != nil {
		t.Fatal(err)
	}
	ap, ok := pol.(*anxiousPolicy)
	if !ok || ap.alpha != 0.3 || ap.b != 2 {
		t.Fatalf("built %+v", pol)
	}
	ap.Stop()
	for _, bad := range []string{"-0.1", "inf", "NaN", "x", ""} {
		p.LbRetryAnxietyRatio = bad
		if _, err := Validate(p); err == nil {
			t.Errorf("lb_retry_anxiety_ratio %q accepted", bad)
		}
	}
	p.LbRetryAnxietyRatio = "0"
	if c, err := Validate(p); err != nil || c.LbRetryAnxietyRatio != 0 {
		t.Errorf("0 rejected: %v", err)
	}
}

// AnxiousEvaluation: a tick re-evaluates only after a stat or liveness change;
// RetryRefused removes a TicketSeq until the next evaluation.
//
// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
func TestAnxiousEvaluateOnlyOnChange(t *testing.T) {
	tr := trackerWith(2, []int{1, 0}, 0, 4, 80, 80, true, true) // current 400, retry 80: anxious
	if !tr.dirty {
		t.Fatal("observations must mark the tracker dirty")
	}
	tr.dirty = false
	tr.readLiveness(nil) // all live, as before: no change
	if tr.dirty {
		t.Fatal("unchanged liveness must not mark dirty")
	}
	down := func(s uint32) bool { return s != 1 }
	tr.readLiveness(&down)
	if !tr.dirty || tr.live[1] {
		t.Fatal("a liveness change must mark dirty")
	}

	// Through the policy: the set appears, a refusal removes it, and it stays
	// out while nothing changes; a new observation brings it back.
	p := newAnxiousPolicy(0.3, 2, time.Millisecond)
	defer p.Stop()
	p.Observe(RetryObservation{Kind: ObsAttemptSent, Seq: 7, ServerID: 0, QueueAtSend: 4})
	p.Observe(RetryObservation{Kind: ObsEwmaChanged, Exec: []float64{80, 80}, Anti: []float64{80, 80},
		HasExec: []bool{true, true}, HasAnti: []bool{true, true}})
	has := func() bool { _, ok := (*p.anxious.Load())[7]; return ok }
	waitFor := func(want bool, what string) {
		deadline := time.Now().Add(2 * time.Second)
		for has() != want {
			if time.Now().After(deadline) {
				t.Fatalf("%s: seq 7 in set = %v", what, has())
			}
			time.Sleep(time.Millisecond)
		}
	}
	waitFor(true, "first evaluation")
	p.Observe(RetryObservation{Kind: ObsRetryRefused, Seq: 7})
	waitFor(false, "refused")
	time.Sleep(30 * time.Millisecond) // many ticks, no change
	if has() {
		t.Fatal("re-evaluated without a change")
	}
	p.Observe(RetryObservation{Kind: ObsAttemptSent, Seq: 8, ServerID: 1, QueueAtSend: 0})
	waitFor(true, "after a new observation") // retry = (1/1+1)*80 = 160 < 400*0.7
	// Attach: backend 1 goes down -> no live other backend -> 7 leaves the set.
	down1 := func(s uint32) bool { return s != 1 }
	p.Attach(down1)
	waitFor(false, "other backend not live")
}
