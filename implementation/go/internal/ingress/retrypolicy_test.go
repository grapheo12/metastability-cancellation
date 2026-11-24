// Source: abstraction/ingress/retry_policy/design.md (retry_policy v0.7.0)
package ingress

import (
	"testing"
	"time"
)

// The registry holds none, which never retries on either event; unknown names fail.
//
// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
func TestRetryPolicyRegistryNone(t *testing.T) {
	p, err := NewRetryPolicy(RetryPolicyNone, Config{})
	if err != nil {
		t.Fatal(err)
	}
	st := RetryState{MaxRetries: 3}
	if p.Decide(st, RetryEvent{Kind: EventTick}) != Wait ||
		p.Decide(st, RetryEvent{Kind: EventAttemptEnded, Outcome: OutcomeBadGateway}) != Wait {
		t.Fatal("none must always wait")
	}
	if _, err := NewRetryPolicy("hedge", Config{}); err == nil {
		t.Fatal("unknown policy must fail")
	}
}

// Registering a policy makes it constructible and valid for lb_retry_policy.
//
// Design: abstraction/ingress/retry_policy/design.md#adding-a-retrypolicy
func TestRegisterRetryPolicy(t *testing.T) {
	RegisterRetryPolicy("test_register_only", func(Config) RetryPolicy {
		return RetryPolicyFunc(func(RetryState, RetryEvent) RetryDecision { return Retry })
	})
	defer delete(retryPolicies, "test_register_only")
	p, err := NewRetryPolicy("test_register_only", Config{})
	if err != nil || p.Decide(RetryState{}, RetryEvent{Kind: EventTick}) != Retry {
		t.Fatal("registered policy")
	}
	q := DefaultParams()
	q.Backends, q.NumClients, q.LbRetryPolicy, q.LbMaxRetries = "127.0.0.1:1", "1", "test_register_only", "2"
	if c, err := Validate(q); err != nil || c.LbRetryPolicy != "test_register_only" || c.LbMaxRetries != 2 {
		t.Fatalf("%+v %v", c, err)
	}
}

// Design: abstraction/ingress/design.md#configuration-validation
func TestValidateLbRetry(t *testing.T) {
	p := DefaultParams()
	p.Backends, p.NumClients = "127.0.0.1:1", "1"
	if c, err := Validate(p); err != nil || c.LbRetryPolicy != RetryPolicyNone || c.LbMaxRetries != 0 {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	for _, bad := range []struct{ policy, max string }{{"", "0"}, {"None", "0"}, {"none", "-1"}, {"none", "1.5"}, {"none", ""}, {"none", "4294967296"}} {
		p.LbRetryPolicy, p.LbMaxRetries = bad.policy, bad.max
		if _, err := Validate(p); err == nil {
			t.Fatalf("%+v must be rejected", bad)
		}
	}
}

// The timeout policy's worked example (d = 2000 ms), as pure Decide calls.
//
// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
func TestTimeoutPolicyWorkedExample(t *testing.T) {
	p, err := NewRetryPolicy(RetryPolicyTimeout, Config{LbRetryTimeoutNs: 2_000_000_000})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	at := func(ms int64, running bool) RetryState {
		return RetryState{MaxRetries: 1, Now: start.Add(time.Duration(ms) * time.Millisecond),
			Attempts: []AttemptView{{ServerID: 0, Start: start, Running: running}}}
	}
	tick := RetryEvent{Kind: EventTick}
	if p.Decide(at(1990, true), tick) != Wait || p.Decide(at(2000, true), tick) != Retry || p.Decide(at(2010, true), tick) != Retry {
		t.Fatal("tick: Wait before 2000 ms, Retry from 2000 ms")
	}
	if p.Decide(at(3000, false), tick) != Wait {
		t.Fatal("an answered attempt is never retried")
	}
	if p.Decide(at(30, false), RetryEvent{Kind: EventAttemptEnded, Outcome: OutcomeBadGateway}) != Wait {
		t.Fatal("a 502 is relayed, not retried")
	}
}

// Design: abstraction/ingress/design.md#configuration-validation
func TestValidateLbRetryTimeout(t *testing.T) {
	p := DefaultParams()
	p.Backends, p.NumClients = "127.0.0.1:1", "1"
	if c, err := Validate(p); err != nil || c.LbRetryTimeoutMs != 150 || c.LbRetryTimeoutNs != 150_000_000 {
		t.Fatalf("default: %+v %v", c, err)
	}
	p.LbRetryPolicy, p.LbRetryTimeoutMs = RetryPolicyTimeout, "2000"
	if c, err := Validate(p); err != nil || c.LbRetryPolicy != "timeout" || c.LbRetryTimeoutNs != 2_000_000_000 {
		t.Fatalf("2000: %+v %v", c, err)
	}
	for _, bad := range []string{"", "0", "-1", "inf", "NaN", "abc"} {
		p.LbRetryTimeoutMs = bad
		if _, err := Validate(p); err == nil {
			t.Fatalf("lb_retry_timeout_ms %q must be rejected", bad)
		}
	}
}
