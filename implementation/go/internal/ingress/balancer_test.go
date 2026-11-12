// Source: abstraction/ingress/load_balancer/design.md (load_balancer v0.3.0)
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
	return NewLoadBalancer(Config{LbPolicy: policy, EwmaBeta: 0.7, Backends: backends, Seed: 7})
}

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
		if got := lb.choose(); got != c.want {
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
		if got := twoChoice(c.i, c.j, less); got != c.want {
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
	lb.Finish(tk, OutcomeRejected)
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
		lb.Finish(tk, o)
		if v := lb.Snapshot()[0].EwmaLatencyNs; v != EwmaInitialLatencyNs {
			t.Fatalf("outcome %d moved the EwmaLatency to %v", o, v)
		}
	}
	lb := newLB(PolicyRoundRobin, 1)
	tk := lb.Pick()
	tk.Start = tk.Start.Add(-5 * time.Millisecond)
	lb.Finish(tk, OutcomeOK)
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
		if got := twoChoice(c.i, c.j, less); got != c.want {
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
		if got := twoChoice(c[0], c[1], sless); got != uint32(min(i, j)) {
			t.Fatalf("start, draws %v: got %d, want the lower id", c, got)
		}
	}
	setOutstanding(start, 0, 1, 0)
	if got := twoChoice(0, 0, sless); got != 0 {
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
	lb.Finish(tk, OutcomeOK)
	tk = lb.Pick()
	lb.Finish(tk, OutcomeRejected)
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
