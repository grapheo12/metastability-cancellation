// Source: abstraction/ingress/design.md (ingress v0.1.17)
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
