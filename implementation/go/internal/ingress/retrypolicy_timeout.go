// Source: abstraction/ingress/retry_policy/design.md (retry_policy v0.3.0)
package ingress

import "time"

// RetryPolicyTimeout is the name of the policy that mimics the client's RetryTimer.
const RetryPolicyTimeout = "timeout"

// init registers timeout: Retry on a Tick once the newest attempt is still
// running and has run lb_retry_timeout_ms since its pick; Wait otherwise
// (including AttemptEnded, so a 502 is relayed).
//
// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
func init() {
	RegisterRetryPolicy(RetryPolicyTimeout, func(cfg Config) RetryPolicy {
		d := time.Duration(cfg.LbRetryTimeoutNs)
		return RetryPolicyFunc(func(s RetryState, e RetryEvent) RetryDecision {
			newest := s.Attempts[len(s.Attempts)-1]
			if e.Kind == EventTick && newest.Running && s.Now.Sub(newest.Start) >= d {
				return Retry
			}
			return Wait
		})
	})
}
