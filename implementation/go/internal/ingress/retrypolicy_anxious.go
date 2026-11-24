// Source: abstraction/ingress/retry_policy/design.md (retry_policy v0.4.0)
package ingress

import (
	"sync"
	"sync/atomic"
	"time"
)

// RetryPolicyAnxious is the name of AnxiousRetry.
const RetryPolicyAnxious = "anxious"

// AnxiousTick is ANXIOUS_TICK_MS; AnxiousBuffer is ANXIOUS_BUFFER.
//
// API: abstraction/ingress/retry_policy/api.md#constants
const (
	AnxiousTick   = 10 * time.Millisecond
	AnxiousBuffer = 4096
)

// init registers anxious: RetryElsewhere on a Tick when the newest attempt is running
// and its TicketSeq is in the set the AnxiousTracker last published.
//
// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
func init() {
	RegisterRetryPolicy(RetryPolicyAnxious, func(cfg Config) RetryPolicy {
		return newAnxiousPolicy(cfg.LbRetryAnxietyRatio, len(cfg.Backends), AnxiousTick)
	})
}

// anxiousPolicy is AnxiousRetry: decide reads the published set; Observe feeds
// the tracker goroutine through inbox; Stop ends it.
//
// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
type anxiousPolicy struct {
	alpha    float64
	b        int
	inbox    chan RetryObservation
	stopped  chan struct{} // closed by Stop
	stopOnce sync.Once
	done     chan struct{} // closed when the tracker goroutine returned
	anxious  atomic.Pointer[map[uint64]struct{}]
	isLive   atomic.Pointer[func(uint32) bool] // set by Attach; nil: every backend live
}

// Attach is attach(policy, is_live): the tracker reads it at every tick.
//
// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
func (p *anxiousPolicy) Attach(isLive func(s uint32) bool) {
	p.isLive.Store(&isLive)
}

func newAnxiousPolicy(alpha float64, b int, tick time.Duration) *anxiousPolicy {
	p := &anxiousPolicy{alpha: alpha, b: b, inbox: make(chan RetryObservation, AnxiousBuffer),
		stopped: make(chan struct{}), done: make(chan struct{})}
	empty := map[uint64]struct{}{}
	p.anxious.Store(&empty)
	go p.run(tick)
	return p
}

// Decide is anxious_decide: RetryElsewhere for an anxious running newest
// attempt on a Tick; AttemptEnded is Wait (the 502 is relayed).
//
// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
func (p *anxiousPolicy) Decide(s RetryState, e RetryEvent) RetryDecision {
	newest := s.Attempts[len(s.Attempts)-1]
	if e.Kind != EventTick || !newest.Running {
		return Wait
	}
	if _, ok := (*p.anxious.Load())[newest.TicketSeq]; ok {
		return RetryElsewhere
	}
	return Wait
}

// Observe is anxious_observe: blocks only while the channel is full, and
// returns at once after Stop.
//
// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
func (p *anxiousPolicy) Observe(o RetryObservation) {
	select {
	case <-p.stopped:
		return
	default:
	}
	select {
	case p.inbox <- o:
	case <-p.stopped:
	}
}

// Stop is anxious_stop; it waits for the tracker goroutine to return.
//
// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
func (p *anxiousPolicy) Stop() {
	p.stopOnce.Do(func() { close(p.stopped) })
	<-p.done
}

// run is anxious_tracker: apply observations as they come; at a tick, read
// liveness and re-evaluate only if something changed (AnxiousEvaluation).
// It is the only writer of the published set.
//
// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
func (p *anxiousPolicy) run(tick time.Duration) {
	defer close(p.done)
	tr := newAnxiousTracker(p.b)
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-p.stopped:
			return
		case o := <-p.inbox:
			if o.Kind == ObsRetryRefused {
				p.unpublish(o.Seq) // not a stat change: no evaluation
				continue
			}
			tr.apply(o)
		case <-t.C:
			tr.readLiveness(p.isLive.Load())
			if tr.dirty {
				set := tr.anxiousSet(p.alpha)
				p.anxious.Store(&set)
				tr.dirty = false
			}
		}
	}
}

// unpublish republishes the set without seq (copy on write: a published map
// is never mutated), so a refused ForwardTask is not asked again until the
// next evaluation.
//
// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
func (p *anxiousPolicy) unpublish(seq uint64) {
	old := *p.anxious.Load()
	if _, ok := old[seq]; !ok {
		return
	}
	next := make(map[uint64]struct{}, len(old))
	for k := range old {
		if k != seq {
			next[k] = struct{}{}
		}
	}
	p.anxious.Store(&next)
}

// anxiousEntry is one attempt in flight: its backend's requests ahead of it.
type anxiousEntry struct {
	queue uint64
}

// anxiousTracker is the AnxiousTracker's state, owned by one goroutine.
// entries is kept per backend, so a finish only walks its own backend.
//
// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
type anxiousTracker struct {
	entries []map[uint64]*anxiousEntry // per backend: TicketSeq -> entry; count[b] = len(entries[b])
	exec    []float64
	hasExec []bool
	anti    []float64
	hasAnti []bool
	live    []bool // BackendLiveness as last read
	dirty   bool   // a stat or liveness changed since the last evaluation
}

func newAnxiousTracker(b int) *anxiousTracker {
	tr := &anxiousTracker{entries: make([]map[uint64]*anxiousEntry, b), exec: make([]float64, b),
		hasExec: make([]bool, b), anti: make([]float64, b), hasAnti: make([]bool, b), live: make([]bool, b)}
	for i := range tr.entries {
		tr.entries[i] = map[uint64]*anxiousEntry{}
		tr.live[i] = true
	}
	return tr
}

// apply folds one observation into the state. An observation naming a backend
// out of range is ignored.
//
// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
func (tr *anxiousTracker) apply(o RetryObservation) {
	switch o.Kind {
	case ObsAttemptSent:
		if int(o.ServerID) < len(tr.entries) {
			tr.entries[o.ServerID][o.Seq] = &anxiousEntry{queue: o.QueueAtSend}
			tr.dirty = true
		}
	case ObsAttemptFinished:
		if int(o.ServerID) >= len(tr.entries) {
			return
		}
		m := tr.entries[o.ServerID]
		delete(m, o.Seq) // count[s] -= 1 when it was tracked
		for _, e := range m {
			if e.queue > 0 {
				e.queue-- // one fewer request ahead of it; never below 0
			}
		}
		tr.dirty = true
	case ObsEwmaChanged:
		copy(tr.exec, o.Exec)
		copy(tr.hasExec, o.HasExec)
		copy(tr.anti, o.Anti)
		copy(tr.hasAnti, o.HasAnti)
		tr.dirty = true
	}
}

// readLiveness reads every backend's liveness (all live when isLive is nil)
// and marks the tracker dirty if any changed.
//
// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
func (tr *anxiousTracker) readLiveness(isLive *func(uint32) bool) {
	for s := range tr.live {
		now := isLive == nil || (*isLive)(uint32(s))
		if now != tr.live[s] {
			tr.live[s] = now
			tr.dirty = true
		}
	}
}

// anxiousSet is anxious_set: every TicketSeq with
// (current - retry) / current >= alpha, where retry averages over the live
// backends other than the attempt's (none: not anxious). Each binary64 step
// is converted explicitly so Go cannot fuse a multiply and an add.
//
// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
func (tr *anxiousTracker) anxiousSet(alpha float64) map[uint64]struct{} {
	out := map[uint64]struct{}{}
	for s, m := range tr.entries {
		if len(m) == 0 || !tr.hasExec[s] || !tr.hasAnti[s] || tr.exec[s] == 0 {
			continue
		}
		var others, nLive uint64 // over L = live backends other than s
		for c := range tr.entries {
			if c != s && tr.live[c] {
				others += uint64(len(tr.entries[c]))
				nLive++
			}
		}
		if nLive == 0 {
			continue
		}
		retry := float64(float64(float64(others)/float64(nLive)+1) * tr.anti[s]) // RetryExpFinish
		for seq, e := range m {
			current := float64(float64(e.queue+1) * tr.exec[s]) // CurrentExpFinish
			if float64(float64(current-retry)/current) >= alpha {
				out[seq] = struct{}{}
			}
		}
	}
	return out
}
