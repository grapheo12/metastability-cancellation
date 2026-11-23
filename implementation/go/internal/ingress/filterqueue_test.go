// Source: abstraction/ingress/load_balancer/design.md (load_balancer v0.13.0)
package ingress

import (
	"testing"
	"time"
)

// mkQueueLB is one backend, deadline_mean, EwmaExecTime 200 ms, WorkerCount 10:
// a request with 5 s left passes while (outstanding + 1) x 0.2 s <= 5 s.
func mkQueueLB(t *testing.T, outstanding int64) *LoadBalancer {
	t.Helper()
	lb := NewLoadBalancer(Config{LbPolicy: PolicyEwma, LbFilter: FilterDeadlineMean, EwmaBeta: 0.7,
		Backends: []string{"a:1"}, Seed: 7}, nil)
	lb.stats[0].Outstanding.Store(outstanding)
	lb.stats[0].WorkerCount.Store(10)
	lb.ewmaExecUpdate(0, 200_000_000)
	return lb
}

func inSecs(s float64) uint64 { return uint64(time.Now().UnixNano()) + uint64(s*1e9) }

func signalled(e *WaitEntry) bool {
	select {
	case <-e.signal:
		return true
	default:
		return false
	}
}

// Nothing fits: the request waits; once outstanding drops a scan sends it.
// Past its deadline a scan drops it instead.
//
// Design: abstraction/ingress/load_balancer/design.md#filterwaitqueue
func TestFilterWaitQueueSendAndDrop(t *testing.T) {
	lb := mkQueueLB(t, 30) // 31 x 0.2 = 6.2 s > 5 s
	if _, e := lb.Admit(inSecs(5), true); e == nil {
		t.Fatal("must wait")
	} else {
		lb.scan()
		if signalled(e) {
			t.Fatal("still nothing fits: keep waiting")
		}
		lb.stats[0].Outstanding.Store(10) // 11 x 0.2 = 2.2 s
		lb.scan()
		if tk, sent := lb.Result(e); !signalled(e) || !sent || tk.ServerID != 0 || lb.stats[0].Outstanding.Load() != 11 {
			t.Fatalf("must be sent once it fits: %+v %v", tk, sent)
		}
	}
	lb2 := mkQueueLB(t, 30)
	_, e := lb2.Admit(inSecs(5), true)
	e.deadline = inSecs(-0.001) // its SLO passes while it waits
	lb2.scan()
	if _, sent := lb2.Result(e); !signalled(e) || sent {
		t.Fatal("past its deadline it must be dropped")
	}
	q, s, d, m := lb2.filterQueueCounts()
	if q != 1 || s != 0 || d != 1 || m != 1 || lb2.FilterDropped() != 1 {
		t.Fatalf("counters %d %d %d %d", q, s, d, m)
	}
}

// FIFO: a newcomer joins behind a non-empty queue even if it would fit now;
// a scan then tries both, oldest first. No deadline bypasses the queue.
//
// Design: abstraction/ingress/load_balancer/design.md#filterwaitqueue
func TestFilterWaitQueueFIFOAndBypass(t *testing.T) {
	lb := mkQueueLB(t, 30)
	_, a := lb.Admit(inSecs(5), true)
	lb.stats[0].Outstanding.Store(0)
	if _, b := lb.Admit(inSecs(5), true); b == nil {
		t.Fatal("a newcomer must join behind a waiting request")
	} else {
		lb.scan()
		ta, okA := lb.Result(a)
		tb, okB := lb.Result(b)
		if !okA || !okB || !ta.Start.Before(tb.Start) && ta.Start != tb.Start {
			t.Fatal("both sent, oldest first")
		}
	}
	lb.stats[0].Outstanding.Store(1000)
	if _, e := lb.Admit(0, false); e != nil {
		t.Fatal("a request without a deadline never waits")
	}
}

// A client reset while waiting: the entry is removed without a pick.
//
// Design: abstraction/ingress/load_balancer/design.md#filterwaitqueue
func TestFilterWaitQueueGone(t *testing.T) {
	lb := mkQueueLB(t, 30)
	_, e := lb.Admit(inSecs(5), true)
	if !lb.Gone(e) {
		t.Fatal("a waiting entry can be marked gone")
	}
	lb.stats[0].Outstanding.Store(0)
	lb.scan()
	if signalled(e) || lb.stats[0].Forwarded.Load() != 0 || len(lb.wq.waiting) != 0 {
		t.Fatal("a gone entry is removed without a pick")
	}
}
