// Package server implements the HTTP/2 /ping + /cancel server. Each file maps
// to one component of abstraction/server.
//
// Source: abstraction/server/design.md (server v0.7.2)
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

// Workload is the ServerWorkload: what a /ping costs the server.
//
// Design: abstraction/server/design.md#why-a-messagebuffer-workload
type Workload int

const (
	WorkloadCPUBurn Workload = iota // zero value: the default
	WorkloadMessageBuffer
)

// Config is the validated, immutable ServerConfig shared by all connection handlers and workers.
//
// Design: abstraction/server/design.md#configuration-validation
type Config struct {
	ListenAddr            string
	ResponseRate          float64
	ResponseBytes         int
	CancelMode            CancelMode
	CancelCheckIntervalUs uint64
	Seed                  uint64
	NWorkerThreads        uint32
	QueueLogPath          string // empty: no QueueLengthLog
	QueueSampleIntervalMs uint64
	InitialStallMs        uint64 // StallGate; 0: no stall
	Workload              Workload
	WindowSize            uint32 // MessageBuffer slots
	NGCBinaryTreeSize     int    // nodes per MessageTree
	NMessagesPerRequest   int
}

// Params holds the raw command-line values, named after the API parameters.
type Params struct {
	ListenAddr            string
	ResponseRate          string
	ResponseBytes         string
	CancelMode            string
	CancelCheckIntervalUs string
	Seed                  string
	NWorkerThreads        string
	QueueLogPath          string
	QueueSampleIntervalMs string
	InitialStallMs        string
	Workload              string
	WindowSize            string
	NGCBinaryTreeSize     string
	NMessagesPerRequest   string
}

// DefaultParams returns the defaults from abstraction/server/api.md#program-parameters.
func DefaultParams() Params {
	return Params{
		ListenAddr:            "0.0.0.0:8080",
		CancelMode:            "cooperative",
		CancelCheckIntervalUs: "50",
		Seed:                  "0",
		NWorkerThreads:        "1",
		QueueSampleIntervalMs: "10",
		InitialStallMs:        "1000",
		Workload:              "cpu_burn",
		WindowSize:            "200000",
		NGCBinaryTreeSize:     "15",
		NMessagesPerRequest:   "50",
	}
}

// Validate turns Params into a Config or returns a one-line error.
//
// Design: abstraction/server/design.md#configuration-validation
func Validate(p Params) (Config, error) {
	var c Config
	c.ListenAddr = p.ListenAddr
	switch p.Workload {
	case "cpu_burn":
		c.Workload = WorkloadCPUBurn
	case "message_buffer":
		c.Workload = WorkloadMessageBuffer
	default:
		return c, errors.New("workload must be cpu_burn or message_buffer")
	}
	if c.Workload == WorkloadCPUBurn && p.ResponseRate == "" {
		return c, errors.New("response_rate is required")
	}
	if p.ResponseRate != "" { // validated whenever given
		rate, err := strconv.ParseFloat(p.ResponseRate, 64)
		if err != nil || !(rate > 0) || math.IsInf(rate, 0) {
			return c, errors.New("response_rate must be a finite number > 0")
		}
		c.ResponseRate = rate
	}
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
	nw, err := strconv.ParseUint(p.NWorkerThreads, 10, 32)
	if err != nil || nw < 1 {
		return c, errors.New("n_worker_threads must be an integer in 1..=4294967295")
	}
	c.NWorkerThreads = uint32(nw)
	qi, err := strconv.ParseUint(p.QueueSampleIntervalMs, 10, 64)
	if err != nil || qi < 1 {
		return c, errors.New("queue_sample_interval_ms must be an integer >= 1")
	}
	c.QueueSampleIntervalMs = qi
	c.QueueLogPath = p.QueueLogPath
	st, err := strconv.ParseUint(p.InitialStallMs, 10, 64)
	if err != nil {
		return c, errors.New("initial_stall_ms must be an integer >= 0")
	}
	c.InitialStallMs = st
	ws, err := strconv.ParseUint(p.WindowSize, 10, 32)
	if err != nil || ws < 1 {
		return c, errors.New("window_size must be an integer in 1..=4294967295")
	}
	c.WindowSize = uint32(ws)
	ts, err := strconv.Atoi(p.NGCBinaryTreeSize)
	if err != nil || ts < 1 || ts > 65536 {
		return c, errors.New("n_gc_binary_tree_size must be an integer in 1..=65536")
	}
	c.NGCBinaryTreeSize = ts
	nm, err := strconv.Atoi(p.NMessagesPerRequest)
	if err != nil || nm < 1 {
		return c, errors.New("n_messages_per_request must be an integer >= 1")
	}
	c.NMessagesPerRequest = nm
	return c, nil
}
