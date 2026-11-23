// Source: abstraction/ingress/load_balancer/design.md (load_balancer v0.13.0)
package ingress

import (
	"math"
	"sync"
	"sync/atomic"
	"time"

	"testbed/internal/sampling"
)

// ForwardOutcome is how a forward ended; each maps to a BackendStats counter.
//
// API: abstraction/ingress/load_balancer/api.md#forwardoutcome
type ForwardOutcome int

const (
	OutcomeOK ForwardOutcome = iota
	OutcomeRejected
	OutcomeBadRequest
	OutcomeBadGateway
	OutcomeReset
)

// BackendStats are one backend's counters.
//
// API: abstraction/ingress/api.md#backendstats
type BackendStats struct {
	Outstanding atomic.Int64
	Forwarded   atomic.Uint64
	OK          atomic.Uint64
	Rejected    atomic.Uint64
	BadRequest  atomic.Uint64
	BadGateway  atomic.Uint64
	Reset       atomic.Uint64
	// EwmaBits is the EwmaLatency in ns, as math.Float64bits.
	//
	// Design: abstraction/ingress/load_balancer/design.md#ewmalatency
	EwmaBits atomic.Uint64
	// ExecBits is the EwmaExecTime in ns, as math.Float64bits, or
	// execNoSampleBits before the first sample.
	//
	// Design: abstraction/ingress/load_balancer/design.md#ewmaexectime
	ExecBits atomic.Uint64
	// WorkerCount is the backend's latest x-worker-threads; 0: not known yet.
	//
	// Design: abstraction/ingress/load_balancer/design.md#deadlinefilter
	WorkerCount atomic.Uint64
}

// execNoSampleBits is EXEC_NO_SAMPLE: the quiet NaN 0x7FF8000000000000, stored
// in ExecBits until the backend's first ExecutionTime sample.
//
// API: abstraction/ingress/load_balancer/api.md#constants
const execNoSampleBits = 0x7FF8000000000000

// EwmaInitialLatencyNs is every backend's EwmaLatency before its first sample.
//
// API: abstraction/ingress/load_balancer/api.md#constants
const EwmaInitialLatencyNs = 1_000_000

// ForwardTicket is returned by Pick and given back to Finish to time the forward.
//
// Design: abstraction/ingress/load_balancer/design.md#forwardticket
type ForwardTicket struct {
	ServerID uint32
	Start    time.Time // carries the monotonic reading
	Attempt  uint32    // LbRetries of the ForwardTask before this attempt (0: first)
}

// BackendStatsValue is a plain copy of one backend's counters for the summary.
type BackendStatsValue struct {
	ServerID   uint32 `json:"server_id"`
	Addr       string `json:"addr"`
	Forwarded  uint64 `json:"forwarded"`
	OK         uint64 `json:"ok"`
	Rejected   uint64 `json:"rejected"`
	BadRequest uint64 `json:"bad_request"`
	BadGateway uint64 `json:"bad_gateway"`
	Reset      uint64 `json:"reset"`
	// EwmaLatencyNs is the final EwmaLatency.
	EwmaLatencyNs float64 `json:"ewma_latency_ns"`
	// EwmaExecNs is the final EwmaExecTime; nil (JSON null) before a sample.
	EwmaExecNs *float64 `json:"ewma_exec_ns"`
	// WorkerCount is the backend's latest WorkerCount, 0 before its first answer.
	WorkerCount uint64 `json:"worker_count"`
}

// outcomeNames are the ForwardOutcome words of forwards.csv, by enum value.
var outcomeNames = [...]string{"ok", "rejected", "bad_request", "bad_gateway", "reset"}

// ForwardRecord is one finished forward, kept in the ForwardLog.
//
// API: abstraction/ingress/load_balancer/api.md#forwardrecord
type ForwardRecord struct {
	End           time.Time // carries the monotonic reading
	ServerID      uint32
	Outcome       ForwardOutcome
	LatencyNs     uint64
	EwmaLatencyNs float64 // the backend's EwmaLatency right after this finish
	ExecNs        uint64  // the ExecutionTime the backend reported; valid if HasExec
	HasExec       bool
	EwmaExecNs    float64 // the backend's EwmaExecTime right after this finish; valid if HasEwmaExec
	HasEwmaExec   bool
	Attempt       uint32 // the ticket's attempt
}

// PickRecord is what the LoadBalancer saw of one backend at one pick.
//
// API: abstraction/ingress/load_balancer/api.md#pickrecord
type PickRecord struct {
	T           time.Time // the pick, before choose; carries the monotonic reading
	ServerID    uint32
	Outstanding int64
	EwmaExecNs  float64 // valid if HasEwmaExec
	HasEwmaExec bool
	WorkerCount uint64
}

// LoadBalancer picks a backend per forward and keeps BackendStats.
//
// Design: abstraction/ingress/load_balancer/design.md#loadbalancer
type LoadBalancer struct {
	policy   string
	filter   string          // FilterNone or the DeadlineStrategy
	wq       filterWaitQueue // FilterWaitQueue (first attempts no backend passes)
	beta     float64
	backends []string
	stats    []BackendStats
	rr       atomic.Uint64
	rngMu    sync.Mutex
	rng      *sampling.Rng
	logMu    sync.Mutex        // ForwardLog: held for one append or one copy
	monitor  *HeartbeatMonitor // BackendLiveness; nil: every backend live
	log      []ForwardRecord
	picks    []PickRecord // PickLog, under logMu
}

// NewLoadBalancer builds the stats array and the policy's random stream.
//
// Design: abstraction/ingress/load_balancer/design.md#loadbalancer
func NewLoadBalancer(cfg Config, monitor *HeartbeatMonitor) *LoadBalancer {
	lb := &LoadBalancer{
		monitor:  monitor,
		policy:   cfg.LbPolicy,
		filter:   cfg.LbFilter,
		beta:     cfg.EwmaBeta,
		backends: cfg.Backends,
		stats:    make([]BackendStats, len(cfg.Backends)),
		rng:      sampling.NewRng(sampling.DeriveSeed(cfg.Seed, sampling.StreamIngressLB, 0)),
	}
	for i := range lb.stats {
		lb.stats[i].EwmaBits.Store(math.Float64bits(EwmaInitialLatencyNs))
		lb.stats[i].ExecBits.Store(execNoSampleBits)
	}
	return lb
}

// Pick chooses a backend, counts the forward as started and stamps the ticket
// after the counters.
//
// Design: abstraction/ingress/load_balancer/design.md#loadbalancer
// Design: abstraction/ingress/load_balancer/design.md#forwardticket
func (lb *LoadBalancer) Pick() ForwardTicket {
	t, _ := lb.PickWithin(0, false) // no RequestDeadline: never dropped
	return t
}

// PickWithin is pick(lb, remaining_ns): with hasDeadline, remainingNs is the
// request's time left for the DeadlineFilter. ok is false when no backend
// passed: nothing changed, and no Finish may follow.
//
// Design: abstraction/ingress/load_balancer/design.md#loadbalancer
// Design: abstraction/ingress/load_balancer/design.md#deadlinefilter
func (lb *LoadBalancer) PickWithin(remainingNs int64, hasDeadline bool) (ForwardTicket, bool) {
	return lb.PickAttempt(remainingNs, hasDeadline, 0)
}

// PickAttempt is pick(lb, remaining_ns, attempt): PickWithin for attempt k of a
// ForwardTask; the ticket carries k into the ForwardRecord.
//
// Design: abstraction/ingress/load_balancer/design.md#loadbalancer
// Design: abstraction/ingress/load_balancer/design.md#forwardticket
func (lb *LoadBalancer) PickAttempt(remainingNs int64, hasDeadline bool, attempt uint32) (ForwardTicket, bool) {
	return lb.pick(remainingNs, hasDeadline, attempt, true)
}

// pick is pick(lb, remaining_ns, attempt, record): record = false for a
// FilterWaitQueue re-check writes no PickRecord. A Dropped changes nothing.
//
// Design: abstraction/ingress/load_balancer/design.md#loadbalancer
func (lb *LoadBalancer) pick(remainingNs int64, hasDeadline bool, attempt uint32, record bool) (ForwardTicket, bool) {
	if record {
		lb.recordPick() // PickLog: what choose is about to see
	}
	s, ok := lb.choose(remainingNs, hasDeadline)
	if !ok {
		return ForwardTicket{}, false // the caller decides: wait in the queue, or skip a refused retry
	}
	lb.stats[s].Outstanding.Add(1)
	lb.stats[s].Forwarded.Add(1)
	return ForwardTicket{ServerID: s, Start: time.Now(), Attempt: attempt}, true
}

// Finish ends a forward started by Pick: counters, then one clock read; an ok
// forward is an EwmaLatency sample, an ok or rejected forward with the
// backend's ExecutionTime (hasExec) an EwmaExecTime sample; every forward gets
// a ForwardRecord.
//
// Design: abstraction/ingress/load_balancer/design.md#loadbalancer
func (lb *LoadBalancer) Finish(t ForwardTicket, outcome ForwardOutcome, execNs uint64, hasExec bool, workers uint32) {
	st := &lb.stats[t.ServerID]
	if workers > 0 && (outcome == OutcomeOK || outcome == OutcomeRejected) {
		st.WorkerCount.Store(uint64(workers)) // the backend's latest WorkerCount (0: none given)
	}
	st.Outstanding.Add(-1)
	switch outcome {
	case OutcomeOK:
		st.OK.Add(1)
	case OutcomeRejected:
		st.Rejected.Add(1)
	case OutcomeBadRequest:
		st.BadRequest.Add(1)
	case OutcomeBadGateway:
		st.BadGateway.Add(1)
	case OutcomeReset:
		st.Reset.Add(1)
	}
	// Design: abstraction/ingress/load_balancer/design.md#forwardticket
	end := time.Now()
	latencyNs := uint64(end.Sub(t.Start).Nanoseconds()) // monotonic: never negative
	var e float64
	if outcome == OutcomeOK {
		e = lb.ewmaUpdate(t.ServerID, latencyNs)
	} else {
		e = math.Float64frombits(st.EwmaBits.Load())
	}
	// Design: abstraction/ingress/load_balancer/design.md#ewmaexectime
	sample := hasExec && (outcome == OutcomeOK || outcome == OutcomeRejected)
	var x float64
	var hasX bool
	if sample {
		x, hasX = lb.ewmaExecUpdate(t.ServerID, execNs), true
	} else {
		x, hasX = lb.execValue(t.ServerID)
	}
	if !sample {
		execNs = 0 // none unless ok/rejected with a report
	}
	// Design: abstraction/ingress/load_balancer/design.md#forwardlog
	lb.logMu.Lock()
	lb.log = append(lb.log, ForwardRecord{End: end, ServerID: t.ServerID, Outcome: outcome, LatencyNs: latencyNs, EwmaLatencyNs: e,
		ExecNs: execNs, HasExec: sample, EwmaExecNs: x, HasEwmaExec: hasX, Attempt: t.Attempt})
	lb.logMu.Unlock()
}

// recordPick appends one PickRecord per backend, in id order, read before
// choose.
//
// Design: abstraction/ingress/load_balancer/design.md#picklog
func (lb *LoadBalancer) recordPick() {
	t := time.Now()
	rows := make([]PickRecord, len(lb.stats))
	for s := range lb.stats {
		e, ok := lb.execValue(uint32(s))
		rows[s] = PickRecord{T: t, ServerID: uint32(s), Outstanding: lb.stats[s].Outstanding.Load(),
			EwmaExecNs: e, HasEwmaExec: ok, WorkerCount: lb.stats[s].WorkerCount.Load()}
	}
	lb.logMu.Lock()
	lb.picks = append(lb.picks, rows...)
	lb.logMu.Unlock()
}

// PickLog returns a copy of the PickLog in append order.
//
// Design: abstraction/ingress/load_balancer/design.md#picklog
func (lb *LoadBalancer) PickLog() []PickRecord {
	lb.logMu.Lock()
	defer lb.logMu.Unlock()
	return append([]PickRecord(nil), lb.picks...)
}

// ForwardLog returns a copy of the ForwardLog in append order.
//
// Design: abstraction/ingress/load_balancer/design.md#forwardlog
func (lb *LoadBalancer) ForwardLog() []ForwardRecord {
	lb.logMu.Lock()
	defer lb.logMu.Unlock()
	return append([]ForwardRecord(nil), lb.log...)
}

// ewmaUpdate folds one sample into the backend's EwmaLatency with a CAS loop.
//
// Design: abstraction/ingress/load_balancer/design.md#ewmalatency
func (lb *LoadBalancer) ewmaUpdate(s uint32, sampleNs uint64) float64 {
	e := &lb.stats[s].EwmaBits
	for {
		oldBits := e.Load()
		next := ewmaNext(math.Float64frombits(oldBits), sampleNs, lb.beta)
		if e.CompareAndSwap(oldBits, math.Float64bits(next)) {
			return next // the value stored by this sample
		}
	}
}

// ewmaNext is old*beta + sample*(1-beta) in binary64, left to right.
//
// Design: abstraction/ingress/load_balancer/design.md#ewmalatency
func ewmaNext(old float64, sampleNs uint64, beta float64) float64 {
	// The explicit float64 conversions round each product, as the design's
	// "binary64, round to nearest" requires: without them Go may fuse the
	// multiply and add (FMA, e.g. on arm64), which rounds once and gives a
	// slightly different value at different call sites.
	return float64(old*beta) + float64(float64(sampleNs)*(1-beta))
}

// ewmaExecUpdate folds one ExecutionTime into the backend's EwmaExecTime with
// a CAS loop; the first sample becomes the average.
//
// Design: abstraction/ingress/load_balancer/design.md#ewmaexectime
func (lb *LoadBalancer) ewmaExecUpdate(s uint32, sampleNs uint64) float64 {
	e := &lb.stats[s].ExecBits
	for {
		oldBits := e.Load()
		var next float64
		if oldBits == execNoSampleBits {
			next = float64(sampleNs)
		} else {
			next = ewmaNext(math.Float64frombits(oldBits), sampleNs, lb.beta)
		}
		if e.CompareAndSwap(oldBits, math.Float64bits(next)) {
			return next
		}
	}
}

// execValue reads the backend's EwmaExecTime; ok is false before a sample.
//
// Design: abstraction/ingress/load_balancer/design.md#ewmaexectime
func (lb *LoadBalancer) execValue(s uint32) (float64, bool) {
	bits := lb.stats[s].ExecBits.Load()
	if bits == execNoSampleBits {
		return 0, false
	}
	return math.Float64frombits(bits), true
}

// ewmaScore is (outstanding + 1) * EwmaLatency.
//
// Design: abstraction/ingress/load_balancer/design.md#ewmascore
func (lb *LoadBalancer) ewmaScore(s uint64) float64 {
	o := lb.stats[s].Outstanding.Load()
	e := math.Float64frombits(lb.stats[s].EwmaBits.Load())
	return float64(o+1) * e
}

// totalOutstanding is the number of forwards in progress on all backends.
func (lb *LoadBalancer) totalOutstanding() int64 {
	var n int64
	for i := range lb.stats {
		n += lb.stats[i].Outstanding.Load()
	}
	return n
}

// Snapshot copies every counter in server_id order.
//
// Design: abstraction/ingress/load_balancer/design.md#loadbalancer
func (lb *LoadBalancer) Snapshot() []BackendStatsValue {
	out := make([]BackendStatsValue, len(lb.stats))
	for i := range lb.stats {
		st := &lb.stats[i]
		var exec *float64
		if x, ok := lb.execValue(uint32(i)); ok {
			exec = &x
		}
		out[i] = BackendStatsValue{uint32(i), lb.backends[i], st.Forwarded.Load(), st.OK.Load(), st.Rejected.Load(),
			st.BadRequest.Load(), st.BadGateway.Load(), st.Reset.Load(), math.Float64frombits(st.EwmaBits.Load()), exec, st.WorkerCount.Load()}
	}
	return out
}

// choose applies the LbPolicy. B == 1 always gives 0 with no draw.
//
// Design: abstraction/ingress/load_balancer/design.md#lbpolicy
func (lb *LoadBalancer) choose(remainingNs int64, hasDeadline bool) (uint32, bool) {
	live := lb.liveSet()
	if lb.filter != FilterNone && hasDeadline {
		// Design: abstraction/ingress/load_balancer/design.md#deadlinefilter
		live = lb.deadlineFilter(live, remainingNs)
		if len(live) == 0 {
			return 0, false // Dropped
		}
	}
	return lb.policyChoice(live), true
}

// FilterDropped is the summary's filter_dropped: requests dropped past their
// deadline while waiting in the FilterWaitQueue.
func (lb *LoadBalancer) FilterDropped() uint64 {
	lb.wq.mu.Lock()
	defer lb.wq.mu.Unlock()
	return lb.wq.dropped
}

// DeadlineFilterConfidence and DeadlineFilterMiss are DEADLINE_FILTER_CONFIDENCE
// and DEADLINE_FILTER_MISS: a backend passes if Pr(late) <= DeadlineFilterMiss.
//
// API: abstraction/ingress/load_balancer/api.md#constants
const (
	DeadlineFilterConfidence = 0.99
	DeadlineFilterMiss       = 0.01
)

// deadlineFilter keeps the backends of live (id order) that have no
// ExecutionTime sample yet or that pass the DeadlineStrategy lb.filter
// (WorkerCount 0 read as 1).
//
// Design: abstraction/ingress/load_balancer/design.md#deadlinefilter
func (lb *LoadBalancer) deadlineFilter(live []uint64, remainingNs int64) []uint64 {
	kept := make([]uint64, 0, len(live))
	for _, s := range live {
		e, ok := lb.execValue(uint32(s))
		if !ok {
			kept = append(kept, s) // no sample yet: eligible
			continue
		}
		n := lb.stats[s].Outstanding.Load()
		if n < 0 {
			n = 0 // a transient negative read cannot happen with matched pick/finish; be safe
		}
		k := lb.stats[s].WorkerCount.Load()
		if k == 0 {
			k = 1 // WorkerCount not known yet
		}
		T := float64(remainingNs)
		var keep bool
		switch lb.filter { // DeadlineStrategy
		case FilterDeadlineMean:
			keep = float64(float64(n+1)*e) <= T
		case FilterDeadlineMeanPerWorker:
			keep = float64(float64(float64(n+1)*e)/float64(k)) <= T
		case FilterDeadlineMeanQueuePlusShare:
			keep = float64(float64(float64(n)*e)+float64(e/float64(k))) <= T // each step rounded: no FMA
		default: // FilterDeadlineMMK
			keep = latePr(uint64(n), k, e, T) <= DeadlineFilterMiss
		}
		if keep {
			kept = append(kept, s)
		}
	}
	return kept
}

// latePr is late_probability: Pr(wait + service > T | N = n) for an M/M/k
// backend with mean service time e (ns), T in ns, in binary64.
//
// Design: abstraction/ingress/load_balancer/design.md#deadlinefilter
func latePr(n, k uint64, e, T float64) float64 {
	if T <= 0 {
		return 1
	}
	x := T / e
	if n < k {
		return math.Exp(-x)
	}
	m := n - k + 1
	if k == 1 {
		return poissonLowerSum(m+1, x)
	}
	a := poissonLowerSum(m, float64(k)*x)
	logB := float64(float64(-x)+float64(float64(m)*math.Log1p(1/float64(k-1)))) + logPoissonUpper(m, float64(k-1)*x)
	return math.Min(float64(a+math.Exp(logB)), 1)
}

// poissonLowerSum is Q(m, y) = sum_{i<m} e^-y y^i / i!, terms in log space,
// summed in increasing i.
//
// Design: abstraction/ingress/load_balancer/design.md#deadlinefilter
func poissonLowerSum(m uint64, y float64) float64 {
	ly := math.Log(y)
	s := 0.0
	for i := uint64(0); i < m; i++ {
		lg, _ := math.Lgamma(float64(i + 1))
		s = float64(s + math.Exp(float64(float64(-y+float64(float64(i)*ly))-lg)))
	}
	return math.Min(s, 1)
}

// logPoissonUpper is ln(1 - Q(m, y)): the lower regularized gamma series when
// y < m (no cancellation), else log1p(-Q).
//
// Design: abstraction/ingress/load_balancer/design.md#deadlinefilter
func logPoissonUpper(m uint64, y float64) float64 {
	if y < float64(m) {
		t, s := 1.0, 1.0
		for j := uint64(1); ; j++ {
			t = float64(t * y / float64(m+j))
			s = float64(s + t)
			if t < 1e-17*s {
				break
			}
		}
		lg, _ := math.Lgamma(float64(m + 1))
		return float64(float64(float64(-y+float64(float64(m)*math.Log(y)))-lg) + math.Log(s))
	}
	q := poissonLowerSum(m, y)
	if q < 1 {
		return math.Log1p(-q)
	}
	return math.Inf(-1)
}

// policyChoice applies the LbPolicy to a non-empty candidate list in id order.
//
// Design: abstraction/ingress/load_balancer/design.md#lbpolicy
func (lb *LoadBalancer) policyChoice(live []uint64) uint32 {
	n := uint64(len(live))
	if n == 1 {
		return uint32(live[0]) // no draw, no counter change
	}
	out := func(s uint64) int64 { return lb.stats[s].Outstanding.Load() }
	switch lb.policy {
	case PolicyRoundRobin:
		return uint32(live[(lb.rr.Add(1)-1)%n])
	case PolicyPowerOfTwo:
		i, j := lb.randomPair(n)
		return twoChoice(live, i, j, func(x, y uint64) bool { return out(x) < out(y) })
	case PolicyEwma:
		i, j := lb.randomPair(n)
		return twoChoice(live, i, j, func(x, y uint64) bool { return lb.ewmaScore(x) < lb.ewmaScore(y) })
	default: // least_outstanding
		best := live[0]
		for _, s := range live[1:] {
			if out(s) < out(best) { // strict: ties go to the lower id
				best = s
			}
		}
		return uint32(best)
	}
}

// liveSet is L: the live backends in id order, or every backend when none
// is live (and always every backend without a monitor).
//
// Design: abstraction/ingress/load_balancer/design.md#lbpolicy
func (lb *LoadBalancer) liveSet() []uint64 {
	b := uint64(len(lb.stats))
	live := make([]uint64, 0, b)
	for s := uint64(0); s < b; s++ {
		if lb.monitor.IsLive(s) {
			live = append(live, s)
		}
	}
	if len(live) == 0 {
		for s := uint64(0); s < b; s++ {
			live = append(live, s)
		}
	}
	return live
}

// randomPair draws i in [0,B-1] and j in [0,B-2] under rngMu.
//
// Design: abstraction/ingress/load_balancer/design.md#lbpolicy
func (lb *LoadBalancer) randomPair(b uint64) (uint64, uint64) {
	lb.rngMu.Lock()
	defer lb.rngMu.Unlock()
	i := lb.rng.UniformInt(0, b-1)
	j := lb.rng.UniformInt(0, b-2)
	return i, j
}

// twoChoice maps the draws (i in [0,n-1], j in [0,n-2]) to two distinct
// members a < b of L and returns b if less(b, a), else a (ties go to the lower id).
//
// Design: abstraction/ingress/load_balancer/design.md#lbpolicy
func twoChoice(live []uint64, i, j uint64, less func(x, y uint64) bool) uint32 {
	if j >= i {
		j++ // uniform over the other n-1 positions
	}
	lo, hi := live[min(i, j)], live[max(i, j)] // L is in id order
	if less(hi, lo) {
		return uint32(hi)
	}
	return uint32(lo)
}
