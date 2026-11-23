// Source: abstraction/ingress/retry_policy/design.md (retry_policy v0.1.0)
package ingress

import (
	"errors"
	"sort"
	"strings"
	"time"
)

// RetryEventKind says why the RetryPolicy is consulted.
//
// API: abstraction/ingress/retry_policy/api.md#retryevent
type RetryEventKind int

const (
	// EventAttemptEnded: the newest attempt ended without a usable answer (Outcome is OutcomeBadGateway).
	EventAttemptEnded RetryEventKind = iota
	// EventTick: LB_RETRY_TICK_MS passed while the newest attempt is still running.
	EventTick
)

// RetryEvent is AttemptEnded(outcome) | Tick.
//
// Design: abstraction/ingress/retry_policy/design.md#retryevent
type RetryEvent struct {
	Kind    RetryEventKind
	Outcome ForwardOutcome // meaningful for EventAttemptEnded only
}

// RetryDecision is Retry | Wait.
//
// API: abstraction/ingress/retry_policy/api.md#retrydecision
type RetryDecision int

const (
	Wait RetryDecision = iota
	Retry
)

// AttemptView is one attempt as a policy sees it.
//
// API: abstraction/ingress/retry_policy/api.md#attemptview
type AttemptView struct {
	ServerID uint32
	Start    time.Time // start_ns: the ticket's stamp (monotonic reading)
	Running  bool
	Outcome  ForwardOutcome // valid when !Running
}

// BackendView is a read-only copy of one backend's Load Balancer state.
//
// API: abstraction/ingress/retry_policy/api.md#backendview
type BackendView struct {
	Outstanding int64
	WorkerCount uint64
	EwmaExecNs  float64 // valid when HasEwmaExec
	HasEwmaExec bool
	Live        bool
}

// RetryState is everything a policy may look at; a copy, so changing it changes nothing.
//
// Design: abstraction/ingress/retry_policy/design.md#retrystate
type RetryState struct {
	RequestID    uint64
	RetriesSoFar uint32
	MaxRetries   uint32
	Now          time.Time // now_ns
	FirstStart   time.Time // first_start_ns
	RemainingNs  int64     // valid when HasDeadline
	HasDeadline  bool
	Attempts     []AttemptView
	Backends     []BackendView
}

// RetryPolicy decides whether to start another attempt now. Decide must not
// block or change shared state; it is only called while budget remains, and
// may be called by several ForwardTasks at once.
//
// Design: abstraction/ingress/retry_policy/design.md#retrypolicy
type RetryPolicy interface {
	Decide(state RetryState, event RetryEvent) RetryDecision
}

// RetryPolicyFunc adapts a function to RetryPolicy.
type RetryPolicyFunc func(state RetryState, event RetryEvent) RetryDecision

// Decide calls f.
func (f RetryPolicyFunc) Decide(state RetryState, event RetryEvent) RetryDecision {
	return f(state, event)
}

// RetryPolicyNone is the name of the never-retrying policy (the default).
const RetryPolicyNone = "none"

// retryPolicies is the RetryPolicyRegistry: name -> constructor. To add a
// policy, register its constructor here (see "Adding a RetryPolicy").
//
// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
var retryPolicies = map[string]func(Config) RetryPolicy{
	// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
	RetryPolicyNone: func(Config) RetryPolicy {
		return RetryPolicyFunc(func(RetryState, RetryEvent) RetryDecision { return Wait })
	},
}

// RegisterRetryPolicy adds (or replaces) a named policy constructor. It must be
// called before any Ingress is configured (e.g. from an init function).
//
// Design: abstraction/ingress/retry_policy/design.md#adding-a-retrypolicy
func RegisterRetryPolicy(name string, ctor func(Config) RetryPolicy) {
	retryPolicies[name] = ctor
}

// retryPolicyNames lists the registered names, sorted, for error messages.
func retryPolicyNames() string {
	names := make([]string, 0, len(retryPolicies))
	for n := range retryPolicies {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// NewRetryPolicy is retry_policy_new: the registered policy for name, or an error.
//
// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
func NewRetryPolicy(name string, cfg Config) (RetryPolicy, error) {
	ctor, ok := retryPolicies[name]
	if !ok {
		return nil, errors.New("lb_retry_policy must be one of " + retryPolicyNames())
	}
	return ctor(cfg), nil
}

// backendViews reads every backend's BackendView, in server_id order.
//
// Design: abstraction/ingress/retry_policy/design.md#retrystate
func (lb *LoadBalancer) backendViews() []BackendView {
	out := make([]BackendView, len(lb.stats))
	for s := range lb.stats {
		e, ok := lb.execValue(uint32(s))
		out[s] = BackendView{
			Outstanding: lb.stats[s].Outstanding.Load(),
			WorkerCount: lb.stats[s].WorkerCount.Load(),
			EwmaExecNs:  e,
			HasEwmaExec: ok,
			Live:        lb.monitor.IsLive(uint64(s)),
		}
	}
	return out
}
