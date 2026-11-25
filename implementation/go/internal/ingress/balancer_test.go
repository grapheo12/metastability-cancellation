// Source: abstraction/ingress/load_balancer/design.md (load_balancer v0.17.0)
package ingress

import (
	"math"
	"sync/atomic"
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
	s, _ := lb.choose(0, false, NoExclude)
	return s
}

// late_probability against the design's worked example (e = 200 ms, T = 5 s),
// to a relative 1e-12, plus the cancellation-prone case and the edges.
//
// Design: abstraction/ingress/load_balancer/design.md#deadlinefilter
func TestLatePrWorkedExample(t *testing.T) {
	const e, T = 200_000_000.0, 5_000_000_000.0
	cases := []struct {
		k, n uint64
		want float64
	}{
		{10, 0, 1.3887943864964040e-11}, {10, 9, 1.3887943864964040e-11}, {10, 10, 1.5431048738848933e-11},
		{10, 50, 1.0439869390746449e-9}, {10, 150, 3.9304667021466753e-5}, {10, 200, 0.0075996341805018727},
		{10, 202, 0.0093662549934202290}, {10, 203, 0.010395504157208201}, {10, 220, 0.057518866812073766},
		{1, 0, 1.3887943864964040e-11}, {1, 10, 0.00058646162975308126}, {1, 13, 0.0064674843658217432},
		{1, 14, 0.012402060718900588},
	}
	for _, c := range cases {
		got := latePr(c.n, c.k, e, T)
		if math.Abs(got-c.want) > 1e-12*c.want {
			t.Errorf("k=%d n=%d: %.17g, want %.17g", c.k, c.n, got, c.want)
		}
		if (got <= DeadlineFilterMiss) != (c.want <= DeadlineFilterMiss) {
			t.Errorf("k=%d n=%d: eligibility differs", c.k, c.n)
		}
	}
	// 1 - Q(999, 500) ~ e^-196 times 2^999: the lower-gamma branch (checked by 50-digit quadrature).
	if got := latePr(1000, 2, 10_000_000, T); math.Abs(got-0.508384337202304) > 1e-12 {
		t.Errorf("n=1000 k=2: %.17g", got)
	}
	if latePr(0, 10, e, 0) != 1 || latePr(0, 10, e, -5) != 1 {
		t.Error("T <= 0 must be late with probability 1")
	}
	if got := latePr(5000, 10, e, T); got < 0.999999 {
		t.Errorf("a very deep queue is late: %v", got)
	}
}

// The filter over two backends (e = 200 ms, T = 5 s) and its effect on pick.
//
// Design: abstraction/ingress/load_balancer/design.md#deadlinefilter
func TestDeadlineFilterWorkedExample(t *testing.T) {
	mk := func(out0, out1 int64, w0, w1 uint64, sample1 bool) *LoadBalancer {
		lb := NewLoadBalancer(Config{LbPolicy: PolicyEwma, LbFilter: FilterDeadlineMMK, EwmaBeta: 0.7,
			Backends: []string{"a:1", "b:1"}, Seed: 7}, nil)
		lb.stats[0].Outstanding.Store(out0)
		lb.stats[1].Outstanding.Store(out1)
		lb.stats[0].WorkerCount.Store(w0)
		lb.stats[1].WorkerCount.Store(w1)
		lb.ewmaExecUpdate(0, 200_000_000)
		if sample1 {
			lb.ewmaExecUpdate(1, 200_000_000)
		}
		return lb
	}
	const T = 5_000_000_000
	cases := []struct {
		name string
		lb   *LoadBalancer
		rem  int64
		want []uint64
	}{
		{"both fit", mk(150, 200, 10, 10, true), T, []uint64{0, 1}},
		{"admit edge 202 / 203", mk(202, 203, 10, 10, true), T, []uint64{0}},
		{"both too deep", mk(220, 220, 10, 10, true), T, []uint64{}},
		{"already late", mk(0, 0, 10, 10, true), -5_000_000, []uint64{}},
		{"no sample: eligible", mk(220, 220, 10, 10, false), T, []uint64{1}},
		{"WorkerCount unknown counts as 1", mk(13, 14, 0, 0, true), T, []uint64{0}},
	}
	for _, c := range cases {
		got := c.lb.deadlineFilter([]uint64{0, 1}, c.rem)
		if len(got) != len(c.want) || (len(got) > 0 && got[0] != c.want[0]) || (len(got) == 2 && got[1] != c.want[1]) {
			t.Fatalf("%s: kept %v, want %v", c.name, got, c.want)
		}
	}
	// Pick: a Dropped changes nothing (the caller decides: wait or skip); n = 1 picks it without a draw.
	lb := mk(220, 220, 10, 10, true)
	if _, ok := lb.PickWithin(T, true); ok || lb.FilterDropped() != 0 ||
		lb.stats[0].Forwarded.Load() != 0 || lb.stats[0].Outstanding.Load() != 220 {
		t.Fatal("a Dropped pick must change nothing")
	}
	lb = mk(202, 203, 10, 10, true)
	before := *lb.rng
	if tk, ok := lb.PickWithin(T, true); !ok || tk.ServerID != 0 || *lb.rng != before {
		t.Fatalf("one eligible backend: %+v %v", tk, ok)
	}
	// No deadline, or lb_filter = none: no filtering at all.
	if _, ok := lb.PickWithin(0, false); !ok {
		t.Fatal("no RequestDeadline must not be dropped")
	}
	off := mk(220, 220, 10, 10, true)
	off.filter = FilterNone
	if _, ok := off.PickWithin(-5_000_000, true); !ok || off.FilterDropped() != 0 {
		t.Fatal("lb_filter = none must not filter")
	}
	// Finish stores the WorkerCount of ok/rejected answers only.
	w := mk(0, 0, 0, 10, true)
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

// The PickLog worked example: B = 2, backend 1 without a sample; a dropped
// pick is recorded too.
//
// Design: abstraction/ingress/load_balancer/design.md#picklog
func TestPickLogWorkedExample(t *testing.T) {
	lb := NewLoadBalancer(Config{LbPolicy: PolicyEwma, LbFilter: FilterDeadlineMMK, EwmaBeta: 0.7,
		Backends: []string{"a:1", "b:1"}, Seed: 7}, nil)
	lb.stats[0].Outstanding.Store(12)
	lb.stats[0].WorkerCount.Store(10)
	lb.ewmaExecUpdate(0, 140_000_000)
	lb.Pick()               // no deadline: forwarded
	lb.PickWithin(-1, true) // late: dropped, still recorded
	log := lb.PickLog()
	if len(log) != 4 {
		t.Fatalf("want 2 records per pick, got %d", len(log))
	}
	r0, r1 := log[0], log[1]
	if r0.ServerID != 0 || r0.Outstanding != 12 || !r0.HasEwmaExec || r0.EwmaExecNs != 140_000_000 || r0.WorkerCount != 10 ||
		r1.ServerID != 1 || r1.Outstanding != 0 || r1.HasEwmaExec || r1.WorkerCount != 0 || !r0.T.Equal(r1.T) {
		t.Fatalf("first pick: %+v %+v", r0, r1)
	}
	if float64(r0.Outstanding+1)*r0.EwmaExecNs != 1_820_000_000 {
		t.Fatal("(outstanding + 1) x EwmaExecTime")
	}
	log[0].ServerID = 9 // a copy
	if lb.PickLog()[0].ServerID != 0 {
		t.Fatal("PickLog must return a copy")
	}
}

// The two mean DeadlineStrategies' worked example: e = 140 ms, outstanding
// (10, 40), WorkerCount (10, 10).
//
// Design: abstraction/ingress/load_balancer/design.md#deadlinefilter
func TestDeadlineMeanStrategiesWorkedExample(t *testing.T) {
	mk := func(filter string) *LoadBalancer {
		lb := NewLoadBalancer(Config{LbPolicy: PolicyEwma, LbFilter: filter, EwmaBeta: 0.7,
			Backends: []string{"a:1", "b:1"}, Seed: 7}, nil)
		lb.stats[0].Outstanding.Store(10)
		lb.stats[1].Outstanding.Store(40)
		lb.stats[0].WorkerCount.Store(10)
		lb.stats[1].WorkerCount.Store(10)
		lb.ewmaExecUpdate(0, 140_000_000)
		lb.ewmaExecUpdate(1, 140_000_000)
		return lb
	}
	cases := []struct {
		rem                        int64
		mean, perWorker, plusShare []uint64
	}{
		{6_000_000_000, []uint64{0, 1}, []uint64{0, 1}, []uint64{0, 1}},
		{3_000_000_000, []uint64{0}, []uint64{0, 1}, []uint64{0}},
		{1_540_000_000, []uint64{0}, []uint64{0, 1}, []uint64{0}},
		{1_414_000_000, []uint64{}, []uint64{0, 1}, []uint64{0}},
		{1_000_000_000, []uint64{}, []uint64{0, 1}, []uint64{}},
		{500_000_000, []uint64{}, []uint64{0}, []uint64{}},
		{154_000_000, []uint64{}, []uint64{0}, []uint64{}},
		{100_000_000, []uint64{}, []uint64{}, []uint64{}},
	}
	same := func(a, b []uint64) bool {
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
	for _, c := range cases {
		if got := mk(FilterDeadlineMean).deadlineFilter([]uint64{0, 1}, c.rem); !same(got, c.mean) {
			t.Errorf("deadline_mean, remaining %d: %v, want %v", c.rem, got, c.mean)
		}
		if got := mk(FilterDeadlineMeanPerWorker).deadlineFilter([]uint64{0, 1}, c.rem); !same(got, c.perWorker) {
			t.Errorf("deadline_mean_per_worker, remaining %d: %v, want %v", c.rem, got, c.perWorker)
		}
		if got := mk(FilterDeadlineMeanQueuePlusShare).deadlineFilter([]uint64{0, 1}, c.rem); !same(got, c.plusShare) {
			t.Errorf("deadline_mean_queue_plus_share, remaining %d: %v, want %v", c.rem, got, c.plusShare)
		}
	}
}

// The EwmaAntiServiceTime worked example: beta 0.5, B = 3; each sample goes to
// every backend but the one that served it.
//
// Design: abstraction/ingress/load_balancer/design.md#ewmaantiservicetime
func TestEwmaAntiServiceTimeWorkedExample(t *testing.T) {
	lb := NewLoadBalancer(Config{LbPolicy: PolicyRoundRobin, EwmaBeta: 0.5, Backends: []string{"h:1", "h:1", "h:1"}}, nil)
	none := -1.0
	steps := []struct {
		server  uint32
		outcome ForwardOutcome
		exec    uint64
		hasExec bool
		sampled bool
		want    [3]float64
	}{
		{0, OutcomeOK, 100, true, true, [3]float64{none, 100, 100}},
		{1, OutcomeRejected, 300, true, true, [3]float64{300, 100, 200}},
		{2, OutcomeReset, 0, false, false, [3]float64{300, 100, 200}},
		{2, OutcomeOK, 500, true, true, [3]float64{400, 300, 200}},
	}
	for i, st := range steps {
		if got := lb.Finish(ForwardTicket{ServerID: st.server, Start: time.Now()}, st.outcome, st.exec, st.hasExec, 0); got != st.sampled {
			t.Fatalf("step %d: sampled %v", i, got)
		}
		for b := uint32(0); b < 3; b++ {
			v, ok := lb.antiValue(b)
			if (st.want[b] == none) == ok || (ok && v != st.want[b]) {
				t.Fatalf("step %d backend %d: %v %v, want %v", i, b, v, ok, st.want[b])
			}
		}
	}
	snap := lb.Snapshot()
	if snap[0].EwmaAntiExecNs == nil || *snap[0].EwmaAntiExecNs != 400 {
		t.Fatalf("summary ewma_anti_exec_ns: %v", snap[0].EwmaAntiExecNs)
	}
	one := newLB(PolicyRoundRobin, 1)
	one.Finish(one.Pick(), OutcomeOK, 7, true, 0)
	if _, ok := one.antiValue(0); ok || one.Snapshot()[0].EwmaAntiExecNs != nil {
		t.Fatal("B = 1: EwmaAntiServiceTime must stay none")
	}
}

// TicketSeq increases in pick order; QueueAtSend is outstanding before the pick.
//
// Design: abstraction/ingress/load_balancer/design.md#forwardticket
func TestTicketSeqAndQueueAtSend(t *testing.T) {
	lb := newLB(PolicyRoundRobin, 2)
	want := []struct {
		seq, queue uint64
		server     uint32
	}{{0, 0, 0}, {1, 0, 1}, {2, 1, 0}, {3, 1, 1}, {4, 2, 0}}
	var tks []ForwardTicket
	for i, w := range want {
		tk := lb.Pick()
		if tk.Seq != w.seq || tk.QueueAtSend != w.queue || tk.ServerID != w.server {
			t.Fatalf("pick %d: %+v, want %+v", i, tk, w)
		}
		tks = append(tks, tk)
	}
	lb.Finish(tks[0], OutcomeOK, 0, false, 0)
	if tk := lb.Pick(); tk.Seq != 5 || tk.ServerID != 1 || tk.QueueAtSend != 2 {
		t.Fatalf("after a finish: %+v", tk)
	}
	if tk := lb.Pick(); tk.ServerID != 0 || tk.QueueAtSend != 2 {
		t.Fatalf("backend 0 after its finish: %+v", tk)
	}
}

// ExcludedBackend: a pick with exclude never chooses it, even when it is the
// only live backend (then Dropped, no counter change); NoExclude is unchanged.
//
// Design: abstraction/ingress/load_balancer/design.md#lbpolicy
func TestPickExcluding(t *testing.T) {
	lb := newLB(PolicyRoundRobin, 3)
	for i := 0; i < 6; i++ {
		tk, ok := lb.PickExcluding(0, false, 1, 1)
		if !ok || tk.ServerID == 1 || tk.Attempt != 1 {
			t.Fatalf("pick %d: %+v %v", i, tk, ok)
		}
	}
	m := &HeartbeatMonitor{live: make([]atomic.Bool, 2)}
	m.live[0].Store(true) // backend 1 dead: L = [0]
	two := NewLoadBalancer(Config{LbPolicy: PolicyRoundRobin, EwmaBeta: 0.7, Backends: []string{"h:1", "h:1"}}, m)
	if _, ok := two.PickExcluding(0, false, 1, 0); ok {
		t.Fatal("the only live backend excluded: want Dropped")
	}
	if s := two.Snapshot(); s[0].Forwarded != 0 || s[1].Forwarded != 0 {
		t.Fatalf("Dropped changed counters: %+v", s)
	}
	if tk, ok := two.PickExcluding(0, false, 0, NoExclude); !ok || tk.ServerID != 0 {
		t.Fatalf("NoExclude: %+v %v", tk, ok)
	}
	m.live[0].Store(false) // all silent: fallback to [0, 1], minus 0
	if tk, ok := two.PickExcluding(0, false, 1, 0); !ok || tk.ServerID != 1 {
		t.Fatalf("all-silent fallback minus exclude: %+v %v", tk, ok)
	}
}

// EstimatedFinish: (QueueAtSend + 1) * EwmaExecTime / WorkerCount at the pick, none before a
// sample; Finish copies it into the ForwardRecord.
//
// Design: abstraction/ingress/load_balancer/design.md#estimatedfinish
func TestEstimatedFinish(t *testing.T) {
	lb := newLB(PolicyRoundRobin, 1)
	tk := lb.Pick()
	if tk.HasEstimate {
		t.Fatalf("no EwmaExecTime yet: %+v", tk)
	}
	lb.Finish(tk, OutcomeOK, 400_000_000, true, 0) // EwmaExecTime = 400 ms
	a, b, c := lb.Pick(), lb.Pick(), lb.Pick()
	for i, w := range []struct {
		tk   ForwardTicket
		want float64
	}{{a, 400_000_000}, {b, 800_000_000}, {c, 1_200_000_000}} {
		if !w.tk.HasEstimate || w.tk.EstimateNs != w.want {
			t.Fatalf("pick %d: %+v, want %v", i, w.tk, w.want)
		}
	}
	lb.Finish(c, OutcomeOK, 0, false, 0)
	// WorkerCount k divides it: q = 3, k = 4 -> 4 x 400 ms / 4; q = 15, k = 40 -> 160 ms.
	for _, w := range []struct {
		q, k uint64
		want float64
	}{{3, 4, 400_000_000}, {15, 40, 160_000_000}, {3, 0, 1_600_000_000}} {
		lb.stats[0].WorkerCount.Store(w.k)
		if got, ok := lb.estimatedFinish(0, w.q); !ok || got != w.want {
			t.Fatalf("q %d k %d: %v %v, want %v", w.q, w.k, got, ok, w.want)
		}
	}
	log := lb.ForwardLog()
	if r := log[len(log)-1]; !r.HasEstimate || r.EstimateNs != 1_200_000_000 || log[0].HasEstimate {
		t.Fatalf("ForwardRecord estimate: %+v / first %+v", r, log[0])
	}
}
