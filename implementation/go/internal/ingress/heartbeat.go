// Source: abstraction/ingress/heartbeat_monitor/design.md (heartbeat_monitor v0.1.0)
package ingress

import (
	"bufio"
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"testbed/internal/h2client"
	"testbed/internal/protocol"
)

// API: abstraction/ingress/heartbeat_monitor/api.md#constants
const (
	HeartbeatCheckMs  = 50
	HeartbeatRedialMs = 100
)

// LivenessRecord is one LivenessLog row: a change of a backend's liveness, or
// its state at start.
//
// API: abstraction/ingress/heartbeat_monitor/api.md#livenessrecord
type LivenessRecord struct {
	At       time.Time // monotonic reading
	ServerID uint32
	Live     bool
}

// HeartbeatMonitor holds BackendLiveness. lastHb is ns since base (the
// monitor's start, a monotonic reading); only reader s writes lastHb[s], only
// the checker writes live and log.
//
// API: abstraction/ingress/heartbeat_monitor/api.md#heartbeatmonitor
type HeartbeatMonitor struct {
	base      time.Time
	timeoutNs int64
	lastHb    []atomic.Int64
	live      []atomic.Bool
	log       []LivenessRecord
	ctx       context.Context // cancelled by Stop: ends dials, reads and waits
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	dial      func(target string) (*h2client.Conn, error) // h2client.Dial; replaceable in tests
}

// StartMonitor returns nil when heartbeat_timeout_ms is 0. Otherwise it logs
// every backend live at start, then starts one reader per backend and the
// checker.
//
// Design: abstraction/ingress/heartbeat_monitor/design.md#heartbeatreader
func StartMonitor(cfg Config, start time.Time) *HeartbeatMonitor {
	if cfg.HeartbeatTimeoutMs == 0 {
		return nil
	}
	b := len(cfg.Backends)
	ctx, cancel := context.WithCancel(context.Background())
	m := &HeartbeatMonitor{base: start, timeoutNs: int64(cfg.HeartbeatTimeoutMs) * 1_000_000,
		lastHb: make([]atomic.Int64, b), live: make([]atomic.Bool, b), ctx: ctx, cancel: cancel, dial: h2client.Dial}
	for s := 0; s < b; s++ {
		m.live[s].Store(true) // lastHb[s] = 0 = start
		m.log = append(m.log, LivenessRecord{At: start, ServerID: uint32(s), Live: true})
	}
	for s, addr := range cfg.Backends {
		m.wg.Add(1)
		go func(s int, addr string) { defer m.wg.Done(); m.reader(s, addr) }(s, addr)
	}
	m.wg.Add(1)
	go func() { defer m.wg.Done(); m.checker() }()
	return m
}

func (m *HeartbeatMonitor) nowNs() int64 { return time.Since(m.base).Nanoseconds() }

// reader keeps GET /heartbeat open on its own connection to backend s and
// stores the arrival time of every well-formed line; it redials after
// HEARTBEAT_REDIAL_MS until Stop.
//
// Design: abstraction/ingress/heartbeat_monitor/design.md#heartbeatreader
func (m *HeartbeatMonitor) reader(s int, addr string) {
	for m.ctx.Err() == nil {
		if conn, err := m.dial(addr); err == nil {
			m.readStream(s, conn)
			conn.Close()
		}
		select {
		case <-m.ctx.Done():
		case <-time.After(HeartbeatRedialMs * time.Millisecond):
		}
	}
}

// readStream reads one HeartbeatStream until it ends or Stop cancels it.
//
// Design: abstraction/ingress/heartbeat_monitor/design.md#heartbeatreader
func (m *HeartbeatMonitor) readStream(s int, conn *h2client.Conn) {
	req, _ := http.NewRequestWithContext(m.ctx, protocol.HeartbeatMethod, "http://"+conn.Authority+protocol.HeartbeatPath, nil)
	resp, err := conn.CC.RoundTrip(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	// Design: abstraction/ingress/heartbeat_monitor/design.md#failure-and-edge-cases
	if resp.StatusCode != protocol.StatusStreamOK { // e.g. 404 from a server without /heartbeat
		return
	}
	br := bufio.NewReader(resp.Body)
	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			return // stream ended; a partial line is dropped
		}
		if _, err := protocol.ParseHeartbeatLine(line[:len(line)-1]); err == nil {
			m.lastHb[s].Store(m.nowNs()) // a malformed line is ignored
		}
	}
}

// checker sets live[s] every HEARTBEAT_CHECK_MS and logs each change.
//
// Design: abstraction/ingress/heartbeat_monitor/design.md#backendliveness
func (m *HeartbeatMonitor) checker() {
	t := time.NewTicker(HeartbeatCheckMs * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-t.C:
		}
		m.check(time.Now())
	}
}

// check is one checker tick at now (a monotonic reading).
//
// Design: abstraction/ingress/heartbeat_monitor/design.md#backendliveness
func (m *HeartbeatMonitor) check(now time.Time) {
	nowNs := now.Sub(m.base).Nanoseconds()
	for s := range m.live {
		alive := nowNs-m.lastHb[s].Load() < m.timeoutNs
		if alive != m.live[s].Load() {
			m.live[s].Store(alive)
			m.log = append(m.log, LivenessRecord{At: now, ServerID: uint32(s), Live: alive})
		}
	}
}

// IsLive is true without a monitor, else live[s]. One atomic load.
//
// Design: abstraction/ingress/heartbeat_monitor/design.md#backendliveness
func (m *HeartbeatMonitor) IsLive(s uint64) bool {
	return m == nil || m.live[s].Load()
}

// Stop cancels every reader and the checker, waits for them and returns the
// LivenessLog. Nil-safe: a nil monitor returns nil.
//
// Design: abstraction/ingress/heartbeat_monitor/design.md#backendliveness
func (m *HeartbeatMonitor) Stop() []LivenessRecord {
	if m == nil {
		return nil
	}
	m.cancel()
	m.wg.Wait()
	return m.log
}
