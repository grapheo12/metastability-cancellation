// Source: abstraction/ingress/load_balancer/design.md (load_balancer v0.1.0)
package ingress

import (
	"sync"
	"sync/atomic"

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
}

// LoadBalancer picks a backend per forward and keeps BackendStats.
//
// Design: abstraction/ingress/load_balancer/design.md#loadbalancer
type LoadBalancer struct {
	policy   string
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
	return &LoadBalancer{
		policy:   cfg.LbPolicy,
		backends: cfg.Backends,
		stats:    make([]BackendStats, len(cfg.Backends)),
		rng:      sampling.NewRng(sampling.DeriveSeed(cfg.Seed, sampling.StreamIngressLB, 0)),
	}
}

// Pick chooses a backend and counts the forward as started.
//
// Design: abstraction/ingress/load_balancer/design.md#loadbalancer
func (lb *LoadBalancer) Pick() uint32 {
	s := lb.choose()
	lb.stats[s].Outstanding.Add(1)
	lb.stats[s].Forwarded.Add(1)
	return s
}

// Finish ends a forward started by Pick.
//
// Design: abstraction/ingress/load_balancer/design.md#loadbalancer
func (lb *LoadBalancer) Finish(s uint32, outcome ForwardOutcome) {
	st := &lb.stats[s]
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
			st.BadRequest.Load(), st.BadGateway.Load(), st.Reset.Load()}
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
		lb.rngMu.Lock()
		i := lb.rng.UniformInt(0, b-1)
		j := lb.rng.UniformInt(0, b-2)
		lb.rngMu.Unlock()
		return powerOfTwoChoice(i, j, out)
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

// powerOfTwoChoice maps the draws (i in [0,B-1], j in [0,B-2]) to two distinct
// backends and returns the less loaded one; ties go to the lower id.
//
// Design: abstraction/ingress/load_balancer/design.md#lbpolicy
func powerOfTwoChoice(i, j uint64, out func(uint64) int64) uint32 {
	if j >= i {
		j++ // uniform over the other B-1 backends
	}
	lo, hi := min(i, j), max(i, j)
	if out(hi) < out(lo) {
		return uint32(hi)
	}
	return uint32(lo)
}
