// Source: abstraction/ingress/load_balancer/design.md (load_balancer v0.1.0)
package ingress

import "testing"

func newLB(policy string, b int) *LoadBalancer {
	backends := make([]string, b)
	for i := range backends {
		backends[i] = "h:1"
	}
	return NewLoadBalancer(Config{LbPolicy: policy, Backends: backends, Seed: 7})
}

func setOutstanding(lb *LoadBalancer, vals ...int64) {
	for i, v := range vals {
		lb.stats[i].Outstanding.Store(v)
	}
}

// Design: abstraction/ingress/load_balancer/design.md#lbpolicy
// Design: abstraction/ingress/load_balancer/design.md#worked-examples
func TestRoundRobinWorkedExample(t *testing.T) {
	lb := newLB(PolicyRoundRobin, 2)
	for i, want := range []uint32{0, 1, 0, 1} {
		if got := lb.Pick(); got != want {
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
	f := func(s uint64) int64 { return out[s] }
	for _, c := range []struct {
		i, j uint64
		want uint32
	}{{0, 0, 1}, {2, 1, 1}, {0, 1, 2}, {2, 0, 2}} {
		if got := powerOfTwoChoice(c.i, c.j, f); got != c.want {
			t.Fatalf("draws (%d,%d): got %d want %d", c.i, c.j, got, c.want)
		}
	}
	lb := newLB(PolicyPowerOfTwo, 3)
	for i := 0; i < 100; i++ {
		if s := lb.Pick(); s > 2 {
			t.Fatalf("out of range %d", s)
		}
	}
}

// Design: abstraction/ingress/load_balancer/design.md#loadbalancer
func TestSingleBackendAndStats(t *testing.T) {
	for _, p := range []string{PolicyRoundRobin, PolicyLeastOutstanding, PolicyPowerOfTwo} {
		lb := newLB(p, 1)
		before := *lb.rng
		if lb.Pick() != 0 || *lb.rng != before {
			t.Fatalf("%s: B=1 must pick 0 and draw nothing", p)
		}
	}
	lb := newLB(PolicyLeastOutstanding, 2)
	s := lb.Pick()
	if lb.stats[s].Outstanding.Load() != 1 || lb.totalOutstanding() != 1 {
		t.Fatal("pick counts outstanding")
	}
	lb.Finish(s, OutcomeRejected)
	v := lb.Snapshot()[s]
	if lb.totalOutstanding() != 0 || v.Forwarded != 1 || v.Rejected != 1 || v.ServerID != s {
		t.Fatalf("finish: %+v", v)
	}
}
