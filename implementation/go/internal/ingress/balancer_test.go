// Source: abstraction/ingress/load_balancer/design.md (load_balancer v0.7.0)
package ingress

import (
	"math"
	"testing"
	"time"
)

func newLB(policy string, b int) *LoadBalancer {
	backends := make([]string, b)
	for i := range backends {
		backends[i] = "h:1"
	}
	return NewLoadBalancer(Config{LbPolicy: policy, EwmaBeta: 0.7, Backends: backends, Seed: 7}, nil)
}

var all3 = []uint64{0, 1, 2} // L with every one of three backends live

func setOutstanding(lb *LoadBalancer, vals ...int64) {
	for i, v := range vals {
		lb.stats[i].Outstanding.Store(v)
	}
}

func setEwma(lb *LoadBalancer, vals ...float64) {
	for i, v := range vals {
		lb.stats[i].EwmaBits.Store(math.Float64bits(v))
	}
}

// Design: abstraction/ingress/load_balancer/design.md#lbpolicy
// Design: abstraction/ingress/load_balancer/design.md#worked-examples
func TestRoundRobinWorkedExample(t *testing.T) {
	lb := newLB(PolicyRoundRobin, 2)
	for i, want := range []uint32{0, 1, 0, 1} {
		if got := lb.Pick().ServerID; got != want {
			t.Fatalf("pick %d: %d", i, got)
		}
	}
}

// Design: abstraction/ingress/load_balancer/design.md#lbpolicy
// Design: abstraction/ingress/load_balancer/design.md#worked-examples
func TestLeastOutstandingWorkedExample(t *testing.T) {
	lb := newLB(PolicyLeastOutstanding, 3)
	cases := []struct {
		out  [3]int64
		want uint32
	}{{[3]int64{0, 0, 0}, 0}, {[3]int64{1, 0, 0}, 1}, {[3]int64{1, 1, 0}, 2}, {[3]int64{2, 1, 1}, 1}}
	for _, c := range cases {
		setOutstanding(lb, c.out[:]...)
		if got := lb.chooseAny(); got != c.want {
			t.Fatalf("outstanding %v: got %d want %d", c.out, got, c.want)
		}
	}
}

// Design: abstraction/ingress/load_balancer/design.md#lbpolicy
// Design: abstraction/ingress/load_balancer/design.md#worked-examples
func TestPowerOfTwoWorkedExample(t *testing.T) {
	out := []int64{2, 0, 1}
	less := func(x, y uint64) bool { return out[x] < out[y] }
	for _, c := range []struct {
		i, j uint64
		want uint32
	}{{0, 0, 1}, {2, 1, 1}, {0, 1, 2}, {2, 0, 2}} {
		if got := twoChoice(all3, c.i, c.j, less); got != c.want {
			t.Fatalf("draws (%d,%d): got %d want %d", c.i, c.j, got, c.want)
		}
	}
	lb := newLB(PolicyPowerOfTwo, 3)
	for i := 0; i < 100; i++ {
		if s := lb.Pick().ServerID; s > 2 {
			t.Fatalf("out of range %d", s)
		}
	}
}

// Design: abstraction/ingress/load_balancer/design.md#loadbalancer
func TestSingleBackendAndStats(t *testing.T) {
	for _, p := range []string{PolicyRoundRobin, PolicyLeastOutstanding, PolicyPowerOfTwo, PolicyEwma} {
		lb := newLB(p, 1)
		before := *lb.rng
		if lb.Pick().ServerID != 0 || *lb.rng != before {
			t.Fatalf("%s: B=1 must pick 0 and draw nothing", p)
		}
	}
	lb := newLB(PolicyLeastOutstanding, 2)
	tk := lb.Pick()
	s := tk.ServerID
	if lb.stats[s].Outstanding.Load() != 1 || lb.totalOutstanding() != 1 {
		t.Fatal("pick counts outstanding")
	}
	lb.Finish(tk, OutcomeRejected, 0, false, 0)
	v := lb.Snapshot()[s]
	if lb.totalOutstanding() != 0 || v.Forwarded != 1 || v.Rejected != 1 || v.ServerID != s ||
		v.EwmaLatencyNs != EwmaInitialLatencyNs {
		t.Fatalf("finish: %+v", v)
	}
}

// Only ok forwards are EwmaLatency samples; the sample is the time since Pick.
//
// Design: abstraction/ingress/load_balancer/design.md#ewmalatency
// Design: abstraction/ingress/load_balancer/design.md#forwardticket
func TestEwmaOnlyOkSamples(t *testing.T) {
	for _, o := range []ForwardOutcome{OutcomeRejected, OutcomeBadRequest, OutcomeBadGateway, OutcomeReset} {
		lb := newLB(PolicyRoundRobin, 1)
		tk := lb.Pick()
		tk.Start = tk.Start.Add(-5 * time.Millisecond) // pretend 5 ms passed
		lb.Finish(tk, o, 0, false, 0)
		if v := lb.Snapshot()[0].EwmaLatencyNs; v != EwmaInitialLatencyNs {
			t.Fatalf("outcome %d moved the EwmaLatency to %v", o, v)
		}
	}
	lb := newLB(PolicyRoundRobin, 1)
	tk := lb.Pick()
	tk.Start = tk.Start.Add(-5 * time.Millisecond)
	lb.Finish(tk, OutcomeOK, 0, false, 0)
	// 1e6*0.7 + (>=5e6)*0.3 >= 2.2e6
	if v := lb.Snapshot()[0].EwmaLatencyNs; v < 2.2e6 {
		t.Fatalf("ok sample not applied: %v", v)
	}
}

// Design: abstraction/ingress/load_balancer/design.md#ewmalatency
// Design: abstraction/ingress/load_balancer/design.md#worked-examples
func TestEwmaUpdateWorkedExample(t *testing.T) {
	for _, c := range []struct {
		old    float64
		sample uint64
		beta   float64
		want   float64
	}{
		{1_000_000, 2_000_000, 0.7, 1_300_000},
		{1_000_000, 500_000, 0.7, 850_000},
		{1_000_000, 0, 0.7, 700_000},
		{1_300_000, 1_300_000, 0.7, 1_300_000},
		{1_000_000, 3_000_000, 0.5, 2_000_000},
		{1_000_000, 3_000_000, 0, 3_000_000},
		{1_000_000, 3_000_000, 1, 1_000_000},
	} {
		if got := ewmaNext(c.old, c.sample, c.beta); got != c.want {
			t.Fatalf("ewmaNext(%v, %d, %v) = %v want %v", c.old, c.sample, c.beta, got, c.want)
		}
	}
	lb := newLB(PolicyEwma, 1)
	lb.ewmaUpdate(0, 2_000_000)
	if v := lb.Snapshot()[0].EwmaLatencyNs; v != 1_300_000 {
		t.Fatalf("ewmaUpdate: %v", v)
	}
}

// Design: abstraction/ingress/load_balancer/design.md#ewmascore
// Design: abstraction/ingress/load_balancer/design.md#lbpolicy
// Design: abstraction/ingress/load_balancer/design.md#worked-examples
func TestEwmaWorkedExample(t *testing.T) {
	lb := newLB(PolicyEwma, 3)
	less := func(x, y uint64) bool { return lb.ewmaScore(x) < lb.ewmaScore(y) }
	setOutstanding(lb, 2, 0, 1)
	setEwma(lb, 1_000_000, 4_000_000, 1_500_000)
	for s, want := range []float64{3_000_000, 4_000_000, 3_000_000} {
		if got := lb.ewmaScore(uint64(s)); got != want {
			t.Fatalf("score %d: %v want %v", s, got, want)
		}
	}
	for _, c := range []struct {
		i, j uint64
		want uint32
	}{{0, 0, 0}, {2, 1, 2}, {0, 1, 0}, {2, 0, 0}} {
		if got := twoChoice(all3, c.i, c.j, less); got != c.want {
			t.Fatalf("draws (%d,%d): got %d want %d", c.i, c.j, got, c.want)
		}
	}
	start := newLB(PolicyEwma, 3)
	sless := func(x, y uint64) bool { return start.ewmaScore(x) < start.ewmaScore(y) }
	for _, c := range [][2]uint64{{0, 0}, {2, 1}, {0, 1}, {2, 0}} {
		i, j := c[0], c[1]
		if j >= i {
			j++
		}
		if got := twoChoice(all3, c[0], c[1], sless); got != uint32(min(i, j)) {
			t.Fatalf("start, draws %v: got %d, want the lower id", c, got)
		}
	}
	setOutstanding(start, 0, 1, 0)
	if got := twoChoice(all3, 0, 0, sless); got != 0 {
		t.Fatalf("(0,1,0) draws (0,0): got %d", got)
	}
	for i := 0; i < 100; i++ {
		if s := lb.Pick().ServerID; s > 2 {
			t.Fatalf("out of range %d", s)
		}
	}
}

// Every Finish appends one ForwardRecord; ewma_latency_ns is the value after
// it (the stored sample for ok, unchanged otherwise). ewmaUpdate returns the
// stored value (worked example: 1_300_000, then 1_060_000).
//
// Design: abstraction/ingress/load_balancer/design.md#forwardlog
// Design: abstraction/ingress/load_balancer/design.md#worked-examples
func TestForwardLog(t *testing.T) {
	lb := newLB(PolicyEwma, 1)
	if v := lb.ewmaUpdate(0, 2_000_000); v != 1_300_000 {
		t.Fatalf("first: %v", v)
	}
	if v := lb.ewmaUpdate(0, 500_000); v != 1_060_000 {
		t.Fatalf("second: %v", v)
	}
	lb = newLB(PolicyEwma, 1)
	tk := lb.Pick()
	tk.Start = tk.Start.Add(-2 * time.Millisecond)
	lb.Finish(tk, OutcomeOK, 0, false, 0)
	tk = lb.Pick()
	lb.Finish(tk, OutcomeRejected, 0, false, 0)
	log := lb.ForwardLog()
	if len(log) != 2 || log[0].Outcome != OutcomeOK || log[1].Outcome != OutcomeRejected || log[0].ServerID != 0 {
		t.Fatalf("log %+v", log)
	}
	if log[0].LatencyNs < 2_000_000 || log[0].EwmaLatencyNs != ewmaNext(EwmaInitialLatencyNs, log[0].LatencyNs, 0.7) {
		t.Fatalf("ok record %+v", log[0])
	}
	if log[1].EwmaLatencyNs != log[0].EwmaLatencyNs || log[1].End.Before(log[0].End) {
		t.Fatalf("rejected record %+v", log[1])
	}
	log[0].ServerID = 9 // a copy
	if lb.ForwardLog()[0].ServerID != 0 {
		t.Fatal("ForwardLog must return a copy")
	}
}

// The EwmaExecTime worked example (ewma_beta = 0.7, one backend, in finish order).
//
// Design: abstraction/ingress/load_balancer/design.md#ewmaexectime
func TestEwmaExecTimeWorkedExample(t *testing.T) {
	lb := newLB(PolicyRoundRobin, 1)
	if v := lb.Snapshot()[0].EwmaExecNs; v != nil {
		t.Fatalf("before a sample: %v, want null", *v)
	}
	steps := []struct {
		outcome  ForwardOutcome
		exec     uint64
		hasExec  bool
		want     float64
		hasValue bool
	}{
		{OutcomeReset, 0, false, 0, false},
		{OutcomeOK, 2_000_000, true, 2_000_000, true},
		{OutcomeRejected, 1_000_000, true, 1_700_000, true},
		{OutcomeBadGateway, 0, false, 1_700_000, true},
		{OutcomeOK, 500_000, true, 1_340_000, true},
		{OutcomeOK, 0, false, 1_340_000, true}, // old server: no ExecReport
	}
	for i, st := range steps {
		lb.Finish(lb.Pick(), st.outcome, st.exec, st.hasExec, 0)
		r := lb.ForwardLog()[i]
		if r.HasEwmaExec != st.hasValue || r.EwmaExecNs != st.want || r.HasExec != st.hasExec || r.ExecNs != st.exec {
			t.Fatalf("step %d: %+v", i, r)
		}
	}
	if v := lb.Snapshot()[0].EwmaExecNs; v == nil || *v != 1_340_000 {
		t.Fatalf("summary ewma_exec_ns: %v", v)
	}
	// A bad_request never samples even if given an exec_ns.
	lb.Finish(lb.Pick(), OutcomeBadRequest, 9, true, 0)
	if r := lb.ForwardLog()[len(steps)]; r.HasExec || r.EwmaExecNs != 1_340_000 {
		t.Fatalf("bad_request sampled: %+v", r)
	}
}

// chooseAny is choose for a request without a RequestDeadline (never dropped).
func (lb *LoadBalancer) chooseAny() uint32 {
	s, _ := lb.choose(0, false)
	return s
}

// The DeadlineFilter worked example: B = 2, EwmaExecTime 140 ms each,
// outstanding (10, 40), WorkerCount (10, 10).
//
// Design: abstraction/ingress/load_balancer/design.md#deadlinefilter
func TestDeadlineFilterWorkedExample(t *testing.T) {
	mk := func(sampleBoth bool, workers0 uint64) *LoadBalancer {
		lb := NewLoadBalancer(Config{LbPolicy: PolicyEwma, LbFilter: FilterDeadline, EwmaBeta: 0.7,
			Backends: []string{"a:1", "b:1"}, Seed: 7}, nil)
		lb.stats[0].Outstanding.Store(10)
		lb.stats[1].Outstanding.Store(40)
		lb.stats[0].WorkerCount.Store(workers0)
		lb.stats[1].WorkerCount.Store(10)
		lb.ewmaExecUpdate(0, 140_000_000)
		if sampleBoth {
			lb.ewmaExecUpdate(1, 140_000_000)
		}
		return lb
	}
	cases := []struct {
		remaining int64
		sampled   bool
		workers0  uint64
		want      []uint64
	}{
		{6_000_000_000, true, 10, []uint64{0, 1}},
		{500_000_000, true, 10, []uint64{0}},
		{154_000_000, true, 10, []uint64{0}}, // <=: exactly fits
		{100_000_000, true, 10, []uint64{}},
		{-5_000_000, true, 10, []uint64{}},    // already late
		{100_000_000, false, 10, []uint64{1}}, // backend 1 has no sample: eligible
		{1_000_000_000, true, 0, []uint64{1}}, // backend 0's WorkerCount unknown: 1 -> 1_540_000_000
	}
	for _, c := range cases {
		lb := mk(c.sampled, c.workers0)
		got := lb.deadlineFilter([]uint64{0, 1}, c.remaining)
		if len(got) != len(c.want) || (len(got) > 0 && got[0] != c.want[0]) || (len(got) == 2 && got[1] != c.want[1]) {
			t.Fatalf("remaining %d: kept %v, want %v", c.remaining, got, c.want)
		}
	}
	// Pick: a drop counts filter_dropped and changes no backend counter; n = 1 picks it without a draw.
	lb := mk(true, 10)
	if _, ok := lb.PickWithin(100_000_000, true); ok || lb.FilterDropped() != 1 ||
		lb.stats[0].Forwarded.Load() != 0 || lb.stats[0].Outstanding.Load() != 10 {
		t.Fatal("drop must only count filter_dropped")
	}
	before := *lb.rng
	if tk, ok := lb.PickWithin(500_000_000, true); !ok || tk.ServerID != 0 || *lb.rng != before {
		t.Fatalf("one eligible backend: %+v %v", tk, ok)
	}
	// No deadline, or lb_filter = none: no filtering at all.
	if _, ok := lb.PickWithin(0, false); !ok {
		t.Fatal("no RequestDeadline must not be dropped")
	}
	off := mk(true, 10)
	off.filter = FilterNone
	if _, ok := off.PickWithin(-5_000_000, true); !ok || off.FilterDropped() != 0 {
		t.Fatal("lb_filter = none must not filter")
	}
	// Finish stores the WorkerCount of ok/rejected answers only.
	w := mk(true, 0)
	w.Finish(w.Pick(), OutcomeBadGateway, 0, false, 7)
	if w.stats[0].WorkerCount.Load() != 0 || w.stats[1].WorkerCount.Load() != 10 {
		t.Fatal("bad_gateway must not set the WorkerCount")
	}
	tk := w.Pick()
	w.Finish(tk, OutcomeOK, 1, true, 4)
	if w.stats[tk.ServerID].WorkerCount.Load() != 4 || w.Snapshot()[tk.ServerID].WorkerCount != 4 {
		t.Fatal("ok must set the WorkerCount")
	}
}

// Design: abstraction/ingress/frontend/design.md#forwardtask
func TestRemaining(t *testing.T) {
	if remaining(1794533190000000000, 1794533185000000000) != 5_000_000_000 ||
		remaining(1794533184995000000, 1794533185000000000) != -5_000_000 ||
		remaining(1<<64-1, 0) != 1<<63-1 {
		t.Fatal("remaining")
	}
}
