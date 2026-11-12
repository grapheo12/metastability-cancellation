// Source: abstraction/ingress/load_balancer/design.md (load_balancer v0.2.0)
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
}

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
}

// LoadBalancer picks a backend per forward and keeps BackendStats.
//
// Design: abstraction/ingress/load_balancer/design.md#loadbalancer
type LoadBalancer struct {
	policy   string
	beta     float64
	backends []string
	stats    []BackendStats
	rr       atomic.Uint64
	rngMu    sync.Mutex
	rng      *sampling.Rng
}

// NewLoadBalancer builds the stats array and the policy's random stream.
//
// Design: abstraction/ingress/load_balancer/design.md#loadbalancer
func NewLoadBalancer(cfg Config) *LoadBalancer {
	lb := &LoadBalancer{
		policy:   cfg.LbPolicy,
		beta:     cfg.EwmaBeta,
		backends: cfg.Backends,
		stats:    make([]BackendStats, len(cfg.Backends)),
		rng:      sampling.NewRng(sampling.DeriveSeed(cfg.Seed, sampling.StreamIngressLB, 0)),
	}
	for i := range lb.stats {
		lb.stats[i].EwmaBits.Store(math.Float64bits(EwmaInitialLatencyNs))
	}
	return lb
}

// Pick chooses a backend, counts the forward as started and stamps the ticket
// after the counters.
//
// Design: abstraction/ingress/load_balancer/design.md#loadbalancer
// Design: abstraction/ingress/load_balancer/design.md#forwardticket
func (lb *LoadBalancer) Pick() ForwardTicket {
	s := lb.choose()
	lb.stats[s].Outstanding.Add(1)
	lb.stats[s].Forwarded.Add(1)
	return ForwardTicket{ServerID: s, Start: time.Now()}
}

// Finish ends a forward started by Pick; an ok forward is an EwmaLatency sample.
//
// Design: abstraction/ingress/load_balancer/design.md#loadbalancer
func (lb *LoadBalancer) Finish(t ForwardTicket, outcome ForwardOutcome) {
	st := &lb.stats[t.ServerID]
	st.Outstanding.Add(-1)
	switch outcome {
	case OutcomeOK:
		st.OK.Add(1)
		// Design: abstraction/ingress/load_balancer/design.md#forwardticket
		lb.ewmaUpdate(t.ServerID, uint64(time.Since(t.Start).Nanoseconds()))
	case OutcomeRejected:
		st.Rejected.Add(1)
	case OutcomeBadRequest:
		st.BadRequest.Add(1)
	case OutcomeBadGateway:
		st.BadGateway.Add(1)
	case OutcomeReset:
		st.Reset.Add(1)
	}
}

// ewmaUpdate folds one sample into the backend's EwmaLatency with a CAS loop.
//
// Design: abstraction/ingress/load_balancer/design.md#ewmalatency
func (lb *LoadBalancer) ewmaUpdate(s uint32, sampleNs uint64) {
	e := &lb.stats[s].EwmaBits
	for {
		oldBits := e.Load()
		next := ewmaNext(math.Float64frombits(oldBits), sampleNs, lb.beta)
		if e.CompareAndSwap(oldBits, math.Float64bits(next)) {
			return
		}
	}
}

// ewmaNext is old*beta + sample*(1-beta) in binary64, left to right.
//
// Design: abstraction/ingress/load_balancer/design.md#ewmalatency
func ewmaNext(old float64, sampleNs uint64, beta float64) float64 {
	return old*beta + float64(sampleNs)*(1-beta)
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
		out[i] = BackendStatsValue{uint32(i), lb.backends[i], st.Forwarded.Load(), st.OK.Load(), st.Rejected.Load(),
			st.BadRequest.Load(), st.BadGateway.Load(), st.Reset.Load(), math.Float64frombits(st.EwmaBits.Load())}
	}
	return out
}

// choose applies the LbPolicy. B == 1 always gives 0 with no draw.
//
// Design: abstraction/ingress/load_balancer/design.md#lbpolicy
func (lb *LoadBalancer) choose() uint32 {
	b := uint64(len(lb.stats))
	if b == 1 {
		return 0
	}
	out := func(s uint64) int64 { return lb.stats[s].Outstanding.Load() }
	switch lb.policy {
	case PolicyRoundRobin:
		return uint32((lb.rr.Add(1) - 1) % b)
	case PolicyPowerOfTwo:
		i, j := lb.randomPair(b)
		return twoChoice(i, j, func(x, y uint64) bool { return out(x) < out(y) })
	case PolicyEwma:
		i, j := lb.randomPair(b)
		return twoChoice(i, j, func(x, y uint64) bool { return lb.ewmaScore(x) < lb.ewmaScore(y) })
	default: // least_outstanding
		best := uint64(0)
		for s := uint64(1); s < b; s++ {
			if out(s) < out(best) { // strict: ties go to the lower id
				best = s
			}
		}
		return uint32(best)
	}
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

// twoChoice maps the draws (i in [0,B-1], j in [0,B-2]) to two distinct
// backends a < b and returns b if less(b, a), else a (ties go to the lower id).
//
// Design: abstraction/ingress/load_balancer/design.md#lbpolicy
func twoChoice(i, j uint64, less func(x, y uint64) bool) uint32 {
	if j >= i {
		j++ // uniform over the other B-1 backends
	}
	lo, hi := min(i, j), max(i, j)
	if less(hi, lo) {
		return uint32(hi)
	}
	return uint32(lo)
}
