// Source: abstraction/ingress/retry_policy/design.md (retry_policy v0.8.0)
package ingress

import (
	"sort"
	"testing"
	"time"
)

// MAX is NO_DEADLINE in the worked-example tables.
const MAX = NoDeadline

// trackerWith builds a tracker with B backends holding the given in-flight
// deadlines (any TicketSeqs) plus the entry under test, TicketSeq 1000 with
// deadline d on backend s, and backend s's EWMAs.
func trackerWith(b int, inflight [][]uint64, s uint32, d uint64, exec, anti float64, hasExec, hasAnti bool) *anxiousTracker {
	tr := newAnxiousTracker(b)
	seq := uint64(0)
	for c, ds := range inflight {
		for _, dl := range ds {
			tr.apply(RetryObservation{Kind: ObsAttemptSent, Seq: seq, ServerID: uint32(c), QueueAtSend: 99, Deadline: dl})
			seq++
		}
	}
	tr.apply(RetryObservation{Kind: ObsAttemptSent, Seq: 1000, ServerID: s, Deadline: d})
	x, a := make([]float64, b), make([]float64, b)
	hx, ha := make([]bool, b), make([]bool, b)
	x[s], a[s], hx[s], ha[s] = exec, anti, hasExec, hasAnti
	tr.apply(RetryObservation{Kind: ObsEwmaChanged, Exec: x, Anti: a, HasExec: hx, HasAnti: ha})
	return tr
}

// The anxious worked example, alpha = 0.3 (ms written as ns: the ratio is
// unit-free); inflight lists the other entries, the starred one is added by
// trackerWith.
//
// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
func TestAnxiousWorkedExample(t *testing.T) {
	cases := []struct {
		name             string
		b                int
		inflight         [][]uint64
		s                uint32
		d                uint64
		exec, anti       float64
		hasExec, hasAnti bool
		dead             int // -1: all live
		want             bool
		current          float64
	}{
		{"EDF, not FIFO", 2, [][]uint64{{3, 5, 20, 30, 40}, {4, 12, 15}}, 0, 10, 80, 80, true, true, -1, true, 240},
		{"late deadline", 2, [][]uint64{{3, 5, 20, 30, 40}, {4, 12, 15}}, 0, 50, 80, 80, true, true, -1, true, 480},
		{"earliest deadline", 2, [][]uint64{{3, 5}, {4, 12, 15}}, 0, 2, 80, 80, true, true, -1, false, 0},
		{"exactly alpha", 2, [][]uint64{{1, 2, 3, 4}, {5, 6, 7, 20}}, 0, 10, 80, 70, true, true, -1, true, 400},
		{"tie", 2, [][]uint64{{10}, {10, 10}}, 0, 10, 80, 80, true, true, -1, false, 0},
		{"no deadline", 2, [][]uint64{{3}, {4, MAX}}, 0, MAX, 80, 80, true, true, -1, false, 0},
		{"three backends", 3, [][]uint64{{3, 5, 7, 9}, {1, 2, 30}, {50}}, 0, 10, 100, 100, true, true, -1, true, 500},
		{"three, one dead", 3, [][]uint64{{3, 5, 7, 9}, {1, 2, 30}, {50}}, 0, 10, 100, 100, true, true, 2, true, 500},
		{"other backend dead", 2, [][]uint64{{3}, {}}, 0, 10, 80, 80, true, true, 1, false, 0},
		{"no sample", 2, [][]uint64{{1}, {}}, 1, 10, 0, 80, false, true, -1, false, 0},
		{"one backend", 1, [][]uint64{{}}, 0, 9, 80, 0, true, false, -1, false, 0},
	}
	for _, c := range cases {
		tr := trackerWith(c.b, c.inflight, c.s, c.d, c.exec, c.anti, c.hasExec, c.hasAnti)
		if c.dead >= 0 {
			tr.live[c.dead] = false
		}
		cur, got := tr.anxiousSet(0.3)[1000]
		if got != c.want || (got && cur != c.current) {
			t.Errorf("%s: anxious %v current %v, want %v %v", c.name, got, cur, c.want, c.current)
		}
	}
	// alpha = 0 retries when moving is no slower; alpha > 1 never.
	tr := trackerWith(2, [][]uint64{{}, {}}, 0, 5, 80, 80, true, true) // current 80, retry (0/1+1)*80 = 80
	if _, ok := tr.anxiousSet(0)[1000]; !ok {
		t.Error("alpha 0, equal estimates: must retry")
	}
	if _, ok := tr.anxiousSet(1.5)[1000]; ok {
		t.Error("alpha > 1 must never retry")
	}
}

// The bookkeeping table: AttemptSent records the deadline, AttemptFinished
// removes the entry; edfAhead(0, 12) counts deadlines <= 12 whenever sent.
//
// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
func TestAnxiousBookkeeping(t *testing.T) {
	tr := newAnxiousTracker(2)
	for seq, d := range map[uint64]uint64{9: 5, 10: 8, 11: 12} {
		tr.apply(RetryObservation{Kind: ObsAttemptSent, Seq: seq, ServerID: 0, Deadline: d})
	}
	ahead := func() uint64 {
		var ds []uint64
		for _, d := range tr.entries[0] {
			ds = append(ds, d)
		}
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		return edfAhead(ds, 12)
	}
	steps := []struct {
		obs  RetryObservation
		want uint64
	}{
		{RetryObservation{Kind: ObsEwmaChanged}, 3}, // start (no entry change)
		{RetryObservation{Kind: ObsAttemptSent, Seq: 12, ServerID: 0, Deadline: 20}, 3},
		{RetryObservation{Kind: ObsAttemptSent, Seq: 13, ServerID: 0, Deadline: 1}, 4},
		{RetryObservation{Kind: ObsAttemptFinished, Seq: 9, ServerID: 0}, 3},
		{RetryObservation{Kind: ObsAttemptFinished, Seq: 42, ServerID: 0}, 3},
	}
	for i, st := range steps {
		if st.obs.Kind != ObsEwmaChanged {
			tr.apply(st.obs)
		}
		if got := ahead(); got != st.want {
			t.Fatalf("step %d: edfAhead %d, want %d", i, got, st.want)
		}
	}
	if edfAhead(nil, 5) != 0 || edfAhead([]uint64{1, MAX}, MAX) != 2 {
		t.Fatal("edfAhead edges")
	}
}

// End to end through the policy: observations reach the tracker, a tick
// publishes the set, and decide retries only a running newest attempt on a
// Tick; after Stop, Observe returns at once.
//
// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
func TestAnxiousPolicyDecide(t *testing.T) {
	p := newAnxiousPolicy(0.3, 1, 2, time.Millisecond)
	// Backend 0: TicketSeqs 0..5 with deadlines 0..5 (seq q has q ahead of it); backend 1 empty; both EWMAs 80.
	for q := uint64(0); q < 6; q++ {
		p.Observe(RetryObservation{Kind: ObsAttemptSent, Seq: q, ServerID: 0, Deadline: q})
	}
	p.Observe(RetryObservation{Kind: ObsEwmaChanged, Exec: []float64{80, 80}, Anti: []float64{80, 80},
		HasExec: []bool{true, true}, HasAnti: []bool{true, true}})
	// retry = (0/1 + 1) * 80 = 80; current = (q+1)*80: q = 0 gives ratio 0, q >= 1 gives >= 0.5.
	state := func(seq uint64, running bool) RetryState {
		// 10 ms left: every anxious attempt here (CurrentExpFinish >= 80 ns-units) is ExpectedLate
		return RetryState{MaxRetries: 1, Attempts: []AttemptView{{TicketSeq: seq, Running: running}}, RemainingNs: 10, HasDeadline: true}
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
	if err != nil || c.LbRetryAnxietyRatio != 0.3 || c.LbRetryLateFactor != 10 {
		t.Fatalf("default: %v %v %v", c.LbRetryAnxietyRatio, c.LbRetryLateFactor, err)
	}
	pol, err := NewRetryPolicy(RetryPolicyAnxious, c)
	if err != nil {
		t.Fatal(err)
	}
	ap, ok := pol.(*anxiousPolicy)
	if !ok || ap.alpha != 0.3 || ap.beta != 10 || ap.b != 2 {
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
	for _, bad := range []string{"-1", "inf", "NaN", "x", ""} {
		p.LbRetryLateFactor = bad
		if _, err := Validate(p); err == nil {
			t.Errorf("lb_retry_late_factor %q accepted", bad)
		}
	}
	p.LbRetryLateFactor = "0"
	if c, err := Validate(p); err != nil || c.LbRetryLateFactor != 0 {
		t.Errorf("late factor 0 rejected: %v", err)
	}
}

// AnxiousEvaluation: a tick re-evaluates only after a stat or liveness change;
// RetryRefused removes a TicketSeq until the next evaluation.
//
// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
func TestAnxiousEvaluateOnlyOnChange(t *testing.T) {
	tr := trackerWith(2, [][]uint64{{1, 2, 3, 4}, {}}, 0, 10, 80, 80, true, true) // current 400, retry 80: anxious
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
	p := newAnxiousPolicy(0.3, 1, 2, time.Millisecond)
	defer p.Stop()
	for seq, d := range map[uint64]uint64{1: 1, 2: 2, 3: 3, 4: 4, 7: 10} { // 4 ahead of seq 7 by deadline
		p.Observe(RetryObservation{Kind: ObsAttemptSent, Seq: seq, ServerID: 0, Deadline: d})
	}
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
	p.Observe(RetryObservation{Kind: ObsAttemptSent, Seq: 8, ServerID: 1, Deadline: 5})
	waitFor(true, "after a new observation") // retry = (1/1+1)*80 = 160 <= 400*0.7
	// Attach: backend 1 goes down -> no live other backend -> 7 leaves the set.
	down1 := func(s uint32) bool { return s != 1 }
	p.Attach(down1)
	waitFor(false, "other backend not live")
}

// ExpectedLate: decide retries an anxious running newest attempt only if its
// CurrentExpFinish exceeds the time left (strict); no deadline never retries.
// The decide worked example (ms written as ns).
//
// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
func TestAnxiousDecideRemaining(t *testing.T) {
	m := map[uint64]float64{7: 400}
	cases := []struct {
		beta      float64
		seq       uint64
		remaining int64
		has       bool
		want      RetryDecision
	}{
		{10, 7, 30, true, RetryElsewhere}, // 400 > 300
		{10, 7, 40, true, Wait},           // tie: 400 > 400 is false
		{10, 7, 150, true, Wait},          // 400 < 1500
		{10, 7, -20, true, RetryElsewhere},
		{10, 7, 0, false, Wait},
		{10, 8, 10, true, Wait},
		{1, 7, 150, true, RetryElsewhere},
		{0, 7, 4000, true, RetryElsewhere},
	}
	for i, c := range cases {
		p := &anxiousPolicy{beta: c.beta}
		p.anxious.Store(&m)
		s := RetryState{MaxRetries: 1, Attempts: []AttemptView{{TicketSeq: c.seq, Running: true}}, RemainingNs: c.remaining, HasDeadline: c.has}
		if got := p.Decide(s, RetryEvent{Kind: EventTick}); got != c.want {
			t.Errorf("case %d (%+v): %v, want %v", i, c, got, c.want)
		}
	}
	// AttemptEnded and a finished newest attempt always wait.
	p := &anxiousPolicy{beta: 10}
	p.anxious.Store(&m)
	s := RetryState{MaxRetries: 1, Attempts: []AttemptView{{TicketSeq: 7, Running: true}}, RemainingNs: 1, HasDeadline: true}
	if p.Decide(s, RetryEvent{Kind: EventAttemptEnded, Outcome: OutcomeBadGateway}) != Wait {
		t.Error("AttemptEnded must wait")
	}
	s.Attempts[0].Running = false
	if p.Decide(s, RetryEvent{Kind: EventTick}) != Wait {
		t.Error("newest not running must wait")
	}
}
