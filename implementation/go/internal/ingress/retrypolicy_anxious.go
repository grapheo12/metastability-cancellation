// Source: abstraction/ingress/retry_policy/design.md (retry_policy v0.7.0)
package ingress

import (
	"sort"
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

// init registers anxious: RetryElsewhere on a Tick when the newest attempt is running,
// its TicketSeq is in the map the AnxiousTracker last published, and its
// CurrentExpFinish exceeds the request's time left (ExpectedLate).
//
// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
func init() {
	RegisterRetryPolicy(RetryPolicyAnxious, func(cfg Config) RetryPolicy {
		return newAnxiousPolicy(cfg.LbRetryAnxietyRatio, cfg.LbRetryLateFactor, len(cfg.Backends), AnxiousTick)
	})
}

// anxiousPolicy is AnxiousRetry: decide reads the published set; Observe feeds
// the tracker goroutine through inbox; Stop ends it.
//
// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
type anxiousPolicy struct {
	alpha    float64
	beta     float64 // ExpectedLate factor (lb_retry_late_factor)
	b        int
	inbox    chan RetryObservation
	stopped  chan struct{} // closed by Stop
	stopOnce sync.Once
	done     chan struct{}                      // closed when the tracker goroutine returned
	anxious  atomic.Pointer[map[uint64]float64] // TicketSeq -> CurrentExpFinish (ns)
	isLive   atomic.Pointer[func(uint32) bool]  // set by Attach; nil: every backend live
}

// Attach is attach(policy, is_live): the tracker reads it at every tick.
//
// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
func (p *anxiousPolicy) Attach(isLive func(s uint32) bool) {
	p.isLive.Store(&isLive)
}

func newAnxiousPolicy(alpha, beta float64, b int, tick time.Duration) *anxiousPolicy {
	p := &anxiousPolicy{alpha: alpha, beta: beta, b: b, inbox: make(chan RetryObservation, AnxiousBuffer),
		stopped: make(chan struct{}), done: make(chan struct{})}
	empty := map[uint64]float64{}
	p.anxious.Store(&empty)
	go p.run(tick)
	return p
}

// Decide is anxious_decide: RetryElsewhere for an anxious running newest
// attempt that is ExpectedLate (CurrentExpFinish > beta x RemainingNs, strict; no
// deadline: never) on a Tick; AttemptEnded is Wait (the 502 is relayed).
//
// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
func (p *anxiousPolicy) Decide(s RetryState, e RetryEvent) RetryDecision {
	newest := s.Attempts[len(s.Attempts)-1]
	if e.Kind != EventTick || !newest.Running {
		return Wait
	}
	c, ok := (*p.anxious.Load())[newest.TicketSeq]
	if !ok || !s.HasDeadline {
		return Wait
	}
	if c > float64(p.beta*float64(s.RemainingNs)) { // ExpectedLate: product rounded first, no FMA
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
	next := make(map[uint64]float64, len(old))
	for k, v := range old {
		if k != seq {
			next[k] = v
		}
	}
	p.anxious.Store(&next)
}

// anxiousTracker is the AnxiousTracker's state, owned by one goroutine.
// entries is kept per backend: TicketSeq -> RequestDeadline (NoDeadline
// without one).
//
// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
type anxiousTracker struct {
	entries []map[uint64]uint64 // per backend: TicketSeq -> deadline (Unix ns)
	exec    []float64
	hasExec []bool
	anti    []float64
	hasAnti []bool
	live    []bool // BackendLiveness as last read
	dirty   bool   // a stat or liveness changed since the last evaluation
}

func newAnxiousTracker(b int) *anxiousTracker {
	tr := &anxiousTracker{entries: make([]map[uint64]uint64, b), exec: make([]float64, b),
		hasExec: make([]bool, b), anti: make([]float64, b), hasAnti: make([]bool, b), live: make([]bool, b)}
	for i := range tr.entries {
		tr.entries[i] = map[uint64]uint64{}
		tr.live[i] = true
	}
	return tr
}

// apply folds one observation into the state. An observation naming a backend
// out of range is ignored. QueueAtSend is not used (EdfAhead replaces it).
//
// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
func (tr *anxiousTracker) apply(o RetryObservation) {
	switch o.Kind {
	case ObsAttemptSent:
		if int(o.ServerID) < len(tr.entries) {
			tr.entries[o.ServerID][o.Seq] = o.Deadline
			tr.dirty = true
		}
	case ObsAttemptFinished:
		if int(o.ServerID) < len(tr.entries) {
			delete(tr.entries[o.ServerID], o.Seq)
			tr.dirty = true
		}
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

// edfAhead is EdfAhead: the number of deadlines in the sorted slice that are
// <= d (an upper bound; ties count as ahead).
//
// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
func edfAhead(sorted []uint64, d uint64) uint64 {
	return uint64(sort.Search(len(sorted), func(i int) bool { return sorted[i] > d }))
}

// anxiousSet is anxious_set: every TicketSeq with
// (current - retry) / current >= alpha, where current counts the attempts
// ahead of it by deadline on its own backend and retry averages the same count
// over the live backends other than its own (none: not anxious). Each binary64
// step is converted explicitly so Go cannot fuse a multiply and an add.
//
// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
func (tr *anxiousTracker) anxiousSet(alpha float64) map[uint64]float64 {
	out := map[uint64]float64{}                 // TicketSeq -> CurrentExpFinish
	sorted := make([][]uint64, len(tr.entries)) // per backend, deadlines ascending
	for c, m := range tr.entries {
		ds := make([]uint64, 0, len(m))
		for _, d := range m {
			ds = append(ds, d)
		}
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		sorted[c] = ds
	}
	for b, m := range tr.entries {
		if len(m) == 0 || !tr.hasExec[b] || !tr.hasAnti[b] || tr.exec[b] == 0 {
			continue
		}
		var nLive uint64 // |L|: live backends other than b
		for c := range tr.entries {
			if c != b && tr.live[c] {
				nLive++
			}
		}
		if nLive == 0 {
			continue
		}
		for seq, d := range m {
			mine := edfAhead(sorted[b], d) - 1 // itself excluded
			var others uint64
			for c := range tr.entries {
				if c != b && tr.live[c] {
					others += edfAhead(sorted[c], d)
				}
			}
			current := float64(float64(mine+1) * tr.exec[b])                         // CurrentExpFinish
			retry := float64(float64(float64(others)/float64(nLive)+1) * tr.anti[b]) // RetryExpFinish
			if float64(float64(current-retry)/current) >= alpha {
				out[seq] = current
			}
		}
	}
	return out
}
