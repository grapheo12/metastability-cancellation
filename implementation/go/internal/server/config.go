// Package server implements the HTTP/2 /ping + /cancel server. Each file maps
// to one component of abstraction/server.
//
// Source: abstraction/server/design.md (server v0.2.0)
package server

import (
	"errors"
	"math"
	"strconv"

	"testbed/internal/protocol"
)

// API: abstraction/server/api.md#constants
const (
	CheckIter              = 1024
	CancelRegistryCapacity = 65536
)

// CancelMode selects how the server reacts to RST_STREAM and /cancel.
type CancelMode int

const (
	CancelIgnore CancelMode = iota
	CancelCooperative
)

// Config is the validated, immutable ServerConfig shared by all connection handlers.
//
// Design: abstraction/server/design.md#configuration-validation
type Config struct {
	ListenAddr            string
	ResponseRate          float64
	ResponseBytes         int
	CancelMode            CancelMode
	CancelCheckIntervalUs uint64
	Seed                  uint64
}

// Params holds the raw command-line values, named after the API parameters.
type Params struct {
	ListenAddr            string
	ResponseRate          string
	ResponseBytes         string
	CancelMode            string
	CancelCheckIntervalUs string
	Seed                  string
}

// DefaultParams returns the defaults from abstraction/server/api.md#program-parameters.
func DefaultParams() Params {
	return Params{
		ListenAddr:            "0.0.0.0:8080",
		CancelMode:            "cooperative",
		CancelCheckIntervalUs: "50",
		Seed:                  "0",
	}
}

// Validate turns Params into a Config or returns a one-line error.
//
// Design: abstraction/server/design.md#configuration-validation
func Validate(p Params) (Config, error) {
	var c Config
	c.ListenAddr = p.ListenAddr
	if p.ResponseRate == "" {
		return c, errors.New("response_rate is required")
	}
	rate, err := strconv.ParseFloat(p.ResponseRate, 64)
	if err != nil || !(rate > 0) || math.IsInf(rate, 0) {
		return c, errors.New("response_rate must be a finite number > 0")
	}
	c.ResponseRate = rate
	if p.ResponseBytes == "" {
		return c, errors.New("response_bytes is required")
	}
	n, err := strconv.Atoi(p.ResponseBytes)
	if err != nil {
		return c, errors.New("response_bytes must be an integer")
	}
	if err := protocol.ValidateBodySize(n); err != nil {
		return c, errors.New("response_bytes: " + err.Error())
	}
	c.ResponseBytes = n
	switch p.CancelMode {
	case "ignore":
		c.CancelMode = CancelIgnore
	case "cooperative":
		c.CancelMode = CancelCooperative
	default:
		return c, errors.New("cancel_mode must be ignore or cooperative")
	}
	iv, err := strconv.ParseUint(p.CancelCheckIntervalUs, 10, 64)
	if err != nil || iv < 1 {
		return c, errors.New("cancel_check_interval_us must be an integer >= 1")
	}
	c.CancelCheckIntervalUs = iv
	seed, err := strconv.ParseUint(p.Seed, 10, 64)
	if err != nil {
		return c, errors.New("seed must be an unsigned 64-bit integer")
	}
	c.Seed = seed
	return c, nil
}
