// Package ingress implements the Ingress program: a load-balancing /ping
// proxy in front of several servers, with an in-process LoadCanceller that
// picks victims from the InFlightMap.
//
// Source: abstraction/ingress/design.md (ingress v0.1.15)
package ingress

import (
	"errors"
	"math"
	"net"
	"strconv"
	"strings"
)

// API: abstraction/ingress/api.md#constants
const (
	MaxClients            = 1 << 20
	MaxBackends           = 64
	IngressDrainTimeoutMs = 2000
)

// LbPolicy names, as accepted by --lb_policy.
//
// Design: abstraction/ingress/load_balancer/design.md#lbpolicy
const (
	PolicyRoundRobin       = "round_robin"
	PolicyLeastOutstanding = "least_outstanding"
	PolicyPowerOfTwo       = "power_of_two"
	PolicyEwma             = "ewma"
)

// Config is the validated IngressConfig.
//
// API: abstraction/ingress/api.md#ingressconfig
type Config struct {
	ListenAddr string
	Backends   []string
	NumClients uint32
	LbPolicy   string
	EwmaBeta   float64
	CancelRate float64
	DurationS  float64
	Seed       uint64
	OutputPath string
	DurationNs int64
	// InitialStallMs delays the LoadCanceller's first arrival; InitialStallNs is it in ns.
	InitialStallMs uint64
	InitialStallNs int64
}

// Params holds the raw command-line values, named after the API parameters.
type Params struct {
	ListenAddr     string
	Backends       string
	NumClients     string
	LbPolicy       string
	EwmaBeta       string
	CancelRate     string
	DurationS      string
	Seed           string
	OutputPath     string
	InitialStallMs string
}

// DefaultParams returns the defaults from abstraction/ingress/api.md#program-parameters.
func DefaultParams() Params {
	return Params{ListenAddr: "0.0.0.0:9090", LbPolicy: PolicyLeastOutstanding, EwmaBeta: "0.7", CancelRate: "0", Seed: "0",
		OutputPath: "ingress", InitialStallMs: "1000"}
}

// Validate turns Params into a Config or returns a one-line error.
//
// Design: abstraction/ingress/design.md#configuration-validation
func Validate(p Params) (Config, error) {
	var c Config
	if !isHostPort(p.ListenAddr) {
		return c, errors.New("listen_addr must be host:port")
	}
	c.ListenAddr = p.ListenAddr
	if strings.TrimSpace(p.Backends) == "" {
		return c, errors.New("backends is required")
	}
	for i, b := range strings.Split(p.Backends, ",") {
		b = strings.TrimSpace(b)
		if !isHostPort(b) {
			return c, errors.New("backend " + strconv.Itoa(i) + " must be host:port")
		}
		c.Backends = append(c.Backends, b)
	}
	if len(c.Backends) > MaxBackends {
		return c, errors.New("at most 64 backends")
	}
	n, err := strconv.ParseUint(p.NumClients, 10, 32)
	if err != nil || n < 1 || n > MaxClients {
		return c, errors.New("num_clients must be an integer in 1 ..= 2^20")
	}
	c.NumClients = uint32(n)
	switch p.LbPolicy {
	case PolicyRoundRobin, PolicyLeastOutstanding, PolicyPowerOfTwo, PolicyEwma:
		c.LbPolicy = p.LbPolicy
	default:
		return c, errors.New("lb_policy must be round_robin, least_outstanding, power_of_two or ewma")
	}
	// !(in range) also rejects NaN; the range rejects infinities.
	if c.EwmaBeta, err = strconv.ParseFloat(p.EwmaBeta, 64); err != nil || !(c.EwmaBeta >= 0 && c.EwmaBeta <= 1) {
		return c, errors.New("ewma_beta must be a number in [0, 1]")
	}
	if c.CancelRate, err = strconv.ParseFloat(p.CancelRate, 64); err != nil || c.CancelRate < 0 ||
		math.IsInf(c.CancelRate, 0) || math.IsNaN(c.CancelRate) {
		return c, errors.New("cancel_rate must be a finite number >= 0")
	}
	if c.CancelRate > 0 {
		d, err := strconv.ParseFloat(p.DurationS, 64)
		if p.DurationS == "" || err != nil || !(d > 0) || math.IsInf(d, 0) {
			return c, errors.New("duration_s is required with cancel_rate > 0 (a finite number > 0)")
		}
		c.DurationS = d
		c.DurationNs = int64(math.Round(d * 1e9))
	}
	if c.Seed, err = strconv.ParseUint(p.Seed, 10, 64); err != nil {
		return c, errors.New("seed must be an unsigned 64-bit integer")
	}
	st, err := strconv.ParseUint(p.InitialStallMs, 10, 64)
	if err != nil || st > math.MaxInt64/1_000_000 {
		return c, errors.New("initial_stall_ms must be an integer >= 0")
	}
	c.InitialStallMs, c.InitialStallNs = st, int64(st)*1_000_000
	c.OutputPath = p.OutputPath
	return c, nil
}

func isHostPort(s string) bool {
	_, port, err := net.SplitHostPort(s)
	return err == nil && port != ""
}
