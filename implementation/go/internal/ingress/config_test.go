// Source: abstraction/ingress/design.md (ingress v0.16.1)
package ingress

import "testing"

// lb_policy accepts ewma; ewma_beta must be a number in [0, 1] under every policy.
//
// Design: abstraction/ingress/design.md#configuration-validation
func TestValidateEwmaBeta(t *testing.T) {
	base := DefaultParams()
	base.Backends, base.NumClients = "127.0.0.1:1,127.0.0.1:2", "4"
	for _, c := range []struct {
		policy, beta string
		ok           bool
		want         float64
	}{
		{PolicyEwma, "0.7", true, 0.7}, {PolicyEwma, "0", true, 0}, {PolicyEwma, "1", true, 1},
		{PolicyRoundRobin, "0.25", true, 0.25},
		{PolicyEwma, "-0.1", false, 0}, {PolicyEwma, "1.1", false, 0}, {PolicyEwma, "NaN", false, 0},
		{PolicyEwma, "Inf", false, 0}, {PolicyEwma, "", false, 0}, {PolicyLeastOutstanding, "2", false, 0},
	} {
		p := base
		p.LbPolicy, p.EwmaBeta = c.policy, c.beta
		cfg, err := Validate(p)
		if (err == nil) != c.ok || (c.ok && (cfg.EwmaBeta != c.want || cfg.LbPolicy != c.policy)) {
			t.Fatalf("%s beta=%q: cfg %+v err %v", c.policy, c.beta, cfg, err)
		}
	}
	if d := DefaultParams(); d.EwmaBeta != "0.7" {
		t.Fatalf("default ewma_beta %q", d.EwmaBeta)
	}
	p := base
	p.LbPolicy = "ewmaa"
	if _, err := Validate(p); err == nil {
		t.Fatal("unknown lb_policy accepted")
	}
}

// Design: abstraction/ingress/design.md#configuration-validation
func TestValidateLbFilter(t *testing.T) {
	p := DefaultParams()
	p.Backends, p.NumClients = "127.0.0.1:8080", "1"
	if c, err := Validate(p); err != nil || c.LbFilter != FilterNone {
		t.Fatalf("default: %+v %v", c, err)
	}
	for _, ok := range []string{FilterDeadlineMean, FilterDeadlineMeanPerWorker, FilterDeadlineMeanQueuePlusShare, FilterDeadlineMMK} {
		p.LbFilter = ok
		if c, err := Validate(p); err != nil || c.LbFilter != ok {
			t.Fatalf("%s: %+v %v", ok, c, err)
		}
	}
	for _, bad := range []string{"", "deadline", "Deadline_mmk", "slo"} { // "deadline" was replaced
		p.LbFilter = bad
		if _, err := Validate(p); err == nil {
			t.Fatalf("lb_filter %q must be rejected", bad)
		}
	}
}

// The OptionalValues peak_ewma, deadline_provisional_rtt and forward_mode hedge are not realized in Go: each is
// rejected as not supported, after the combination rules; unknown values are invalid.
//
// Design: abstraction/ingress/design.md#configuration-validation
func TestValidateOptionalValues(t *testing.T) {
	base := DefaultParams()
	base.Backends, base.NumClients = "127.0.0.1:1,127.0.0.1:2", "4"
	for _, c := range []struct {
		policy, filter, mode, err string
	}{
		{PolicyPeakEwma, FilterNone, ForwardRelay, "lb_policy peak_ewma is not supported by this implementation"},
		{PolicyPeakEwma, FilterDeadlineProvisionalRtt, ForwardRelay, "lb_policy peak_ewma is not supported by this implementation"},
		{PolicyEwma, FilterDeadlineProvisionalRtt, ForwardRelay, "lb_filter deadline_provisional_rtt needs lb_policy peak_ewma"},
		{PolicyPeakEwma, FilterDeadlineMean, ForwardRelay, "lb_policy peak_ewma needs lb_filter none or deadline_provisional_rtt"},
		{PolicyEwma, FilterNone, ForwardHedge, "forward_mode hedge is not supported by this implementation"},
		{PolicyEwma, FilterNone, "race", "forward_mode must be relay or hedge"},
		{PolicyEwma, FilterDeadlineMean, ForwardRelay, ""},
	} {
		p := base
		p.LbPolicy, p.LbFilter, p.ForwardMode = c.policy, c.filter, c.mode
		_, err := Validate(p)
		if (c.err == "" && err != nil) || (c.err != "" && (err == nil || err.Error() != c.err)) {
			t.Errorf("%s/%s/%s: %v, want %q", c.policy, c.filter, c.mode, err, c.err)
		}
	}
	if DefaultParams().ForwardMode != ForwardRelay {
		t.Fatal("default forward_mode must be relay")
	}
}
