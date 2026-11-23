// Source: abstraction/ingress/load_balancer/design.md (load_balancer v0.13.0)
package ingress

import (
	"sync"
	"time"
)

// FilterQueueTick is FILTER_QUEUE_TICK_MS.
//
// API: abstraction/ingress/load_balancer/api.md#functions
const FilterQueueTick = 10 * time.Millisecond

// waitState is a WaitEntry's state.
type waitState int

const (
	waitWaiting waitState = iota
	waitSent
	waitDropped
	waitGone
)

// WaitEntry is one first attempt waiting in the FilterWaitQueue. Its state
// and ticket are written under the queue's lock; signal is closed once, when a
// scan sends or drops it.
//
// Design: abstraction/ingress/load_balancer/design.md#filterwaitqueue
type WaitEntry struct {
	deadline uint64
	state    waitState
	ticket   ForwardTicket // valid when state == waitSent
	signal   chan struct{}
}

// filterWaitQueue is the FilterWaitQueue: a FIFO of WaitEntries and its
// summary counters, all under mu; a scanner goroutine runs while the filter is on.
//
// Design: abstraction/ingress/load_balancer/design.md#filterwaitqueue
type filterWaitQueue struct {
	mu                                     sync.Mutex
	waiting                                []*WaitEntry
	queued, sentFromQueue, dropped, maxLen uint64
	stop                                   chan struct{}
	done                                   chan struct{}
}

// Admit is admit(lb, deadline) for attempt 0: a ticket now, or a WaitEntry
// (nil ticket meaning) whose signal fires when a scan sends or drops it.
//
// Design: abstraction/ingress/load_balancer/design.md#filterwaitqueue
func (lb *LoadBalancer) Admit(deadline uint64, hasDeadline bool) (ForwardTicket, *WaitEntry) {
	if lb.filter == FilterNone || !hasDeadline {
		t, _ := lb.pick(0, false, 0, true) // never filtered, so never Dropped
		return t, nil
	}
	lb.wq.mu.Lock()
	defer lb.wq.mu.Unlock()
	if len(lb.wq.waiting) == 0 { // FIFO: a newcomer never overtakes a waiting request
		if t, ok := lb.pick(remaining(deadline, time.Now().UnixNano()), true, 0, true); ok {
			return t, nil
		}
	}
	e := &WaitEntry{deadline: deadline, state: waitWaiting, signal: make(chan struct{})}
	lb.wq.waiting = append(lb.wq.waiting, e)
	lb.wq.queued++
	return ForwardTicket{}, e
}

// Result reads a signalled entry: the ticket and true if it was sent, false if dropped.
func (lb *LoadBalancer) Result(e *WaitEntry) (ForwardTicket, bool) {
	lb.wq.mu.Lock()
	defer lb.wq.mu.Unlock()
	return e.ticket, e.state == waitSent
}

// Gone marks a waiting entry whose client reset its stream; the next scan
// removes it. It returns false if a scan already sent or dropped it.
//
// Design: abstraction/ingress/load_balancer/design.md#filterwaitqueue
func (lb *LoadBalancer) Gone(e *WaitEntry) bool {
	lb.wq.mu.Lock()
	defer lb.wq.mu.Unlock()
	if e.state != waitWaiting {
		return false
	}
	e.state = waitGone
	return true
}

// scan is scan(lb): oldest first, drop entries past their deadline, send
// those a re-check (no PickRecord) now passes, keep the rest in place.
//
// Design: abstraction/ingress/load_balancer/design.md#filterwaitqueue
func (lb *LoadBalancer) scan() {
	lb.wq.mu.Lock()
	defer lb.wq.mu.Unlock()
	if n := uint64(len(lb.wq.waiting)); n > lb.wq.maxLen {
		lb.wq.maxLen = n
	}
	kept := lb.wq.waiting[:0]
	for _, e := range lb.wq.waiting {
		if e.state == waitGone {
			continue // its client reset the stream
		}
		rem := remaining(e.deadline, time.Now().UnixNano())
		if rem <= 0 { // past its SLO
			e.state = waitDropped
			lb.wq.dropped++
			close(e.signal)
			continue
		}
		if t, ok := lb.pick(rem, true, 0, false); ok {
			e.state, e.ticket = waitSent, t
			lb.wq.sentFromQueue++
			close(e.signal)
			continue
		}
		kept = append(kept, e) // still waiting, in place
	}
	for i := len(kept); i < len(lb.wq.waiting); i++ {
		lb.wq.waiting[i] = nil // let removed entries be collected
	}
	lb.wq.waiting = kept
}

// startFilterQueue starts the scanner when a DeadlineStrategy is on.
//
// Design: abstraction/ingress/load_balancer/design.md#filterwaitqueue
func (lb *LoadBalancer) startFilterQueue() {
	if lb.filter == FilterNone {
		return
	}
	lb.wq.stop, lb.wq.done = make(chan struct{}), make(chan struct{})
	go func() {
		defer close(lb.wq.done)
		t := time.NewTicker(FilterQueueTick)
		defer t.Stop()
		for {
			select {
			case <-lb.wq.stop:
				return
			case <-t.C:
				lb.scan()
			}
		}
	}()
}

// stopFilterQueue stops the scanner (idempotent).
func (lb *LoadBalancer) stopFilterQueue() {
	if lb.wq.stop == nil {
		return
	}
	select {
	case <-lb.wq.stop:
	default:
		close(lb.wq.stop)
	}
	<-lb.wq.done
}

// filterQueueCounts returns the summary counters.
func (lb *LoadBalancer) filterQueueCounts() (queued, sent, dropped, maxLen uint64) {
	lb.wq.mu.Lock()
	defer lb.wq.mu.Unlock()
	return lb.wq.queued, lb.wq.sentFromQueue, lb.wq.dropped, lb.wq.maxLen
}

// noteDropped counts a direct drop (kept only until the Frontend waits in the queue).
func (lb *LoadBalancer) noteDropped() {
	lb.wq.mu.Lock()
	lb.wq.dropped++
	lb.wq.mu.Unlock()
}
