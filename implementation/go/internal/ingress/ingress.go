// Source: abstraction/ingress/design.md (ingress v0.13.0)
package ingress

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/http2"
)

// Ingress holds the shared state of one process.
//
// Design: abstraction/ingress/design.md#shared-state
type Ingress struct {
	cfg      Config
	ln       net.Listener
	inflight *InFlightMap
	lb       *LoadBalancer
	pool     *UpstreamPool
	monitor  *HeartbeatMonitor // nil when heartbeat_timeout_ms = 0
	h2s      *http2.Server
	base     *http.Server // only carries the shutdown hook that sends GOAWAY

	started   atomic.Bool
	startMu   sync.Mutex // guards runStart and canceller after started is set
	runStart  time.Time
	canceller *Canceller

	lbRetries      atomic.Uint64 // LbRetries started (summary lb_retries)
	lbRetryRefused atomic.Uint64 // LbRetries the DeadlineFilter refused (summary lb_retry_refused)
	retryPolicy    RetryPolicy   // from lb_retry_policy, built once

	mu           sync.Mutex
	conns        map[net.Conn]struct{}
	wg           sync.WaitGroup
	shuttingDown atomic.Bool
}

// Listen creates the shared state, binds listen_addr and prints
// "listening on <addr>". Only the Heartbeat Monitor (if on) contacts the backends.
//
// API: abstraction/ingress/api.md#lifecycle
// Design: abstraction/ingress/design.md#structure
func Listen(cfg Config) (*Ingress, error) {
	ing := &Ingress{
		cfg:      cfg,
		inflight: NewInFlightMap(cfg.NumClients),
		pool:     NewUpstreamPool(cfg.Backends),
		h2s:      &http2.Server{MaxConcurrentStreams: 1000},
		base:     &http.Server{},
		conns:    map[net.Conn]struct{}{},
	}
	if err := http2.ConfigureServer(ing.base, ing.h2s); err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return nil, err
	}
	ing.ln = ln
	ing.monitor = StartMonitor(cfg, time.Now()) // dials every backend for GET /heartbeat; nil when off
	ing.lb = NewLoadBalancer(cfg, ing.monitor)
	ing.lb.startFilterQueue() // FilterWaitQueue scanner; not started with lb_filter = none
	name := cfg.LbRetryPolicy
	if name == "" {
		name = RetryPolicyNone // a Config built by hand, not by Validate
	}
	if ing.retryPolicy, err = NewRetryPolicy(name, cfg); err != nil {
		_ = ln.Close()
		return nil, err
	}
	// Design: abstraction/ingress/design.md#shared-state
	attachPolicy(ing.retryPolicy, func(s uint32) bool { return ing.monitor.IsLive(uint64(s)) }) // nil monitor: all live
	fmt.Println("listening on " + cfg.ListenAddr)
	return ing, nil
}

// Addr is the bound address (useful with port 0 in tests).
func (ing *Ingress) Addr() net.Addr { return ing.ln.Addr() }

// Serve accepts client connections until Shutdown; each is served by its own
// goroutine and every stream by another.
//
// Design: abstraction/ingress/design.md#concurrency-model
// Design: abstraction/ingress/frontend/design.md#client-connections
func (ing *Ingress) Serve() {
	for {
		conn, err := ing.ln.Accept()
		if err != nil {
			if ing.shuttingDown.Load() || errors.Is(err, net.ErrClosed) {
				return
			}
			fmt.Fprintln(os.Stderr, "accept:", err)
			continue
		}
		ing.mu.Lock()
		if ing.shuttingDown.Load() {
			ing.mu.Unlock()
			_ = conn.Close()
			return
		}
		ing.conns[conn] = struct{}{}
		ing.wg.Add(1)
		ing.mu.Unlock()
		go func() {
			defer ing.wg.Done()
			ing.h2s.ServeConn(conn, &http2.ServeConnOpts{BaseConfig: ing.base, Handler: http.HandlerFunc(ing.handle)})
			ing.mu.Lock()
			delete(ing.conns, conn)
			ing.mu.Unlock()
		}()
	}
}

// Shutdown stops accepting, sends GOAWAY, waits up to INGRESS_DRAIN_TIMEOUT_MS
// for forwards in progress, stops the Load Canceller, closes every
// connection and writes the outputs.
//
// Design: abstraction/ingress/design.md#shutdown
func (ing *Ingress) Shutdown() error {
	ing.mu.Lock()
	ing.shuttingDown.Store(true)
	ing.mu.Unlock()
	_ = ing.ln.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	_ = ing.base.Shutdown(ctx) // GOAWAY on every client connection
	cancel()

	ing.lb.stopFilterQueue()
	deadline := time.Now().Add(IngressDrainTimeoutMs * time.Millisecond)
	for ing.lb.totalOutstanding() > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	ing.startMu.Lock()
	canceller, runStart, started := ing.canceller, ing.runStart, ing.started.Load()
	ing.startMu.Unlock()
	var records []*CancelRecord
	if canceller != nil {
		records = canceller.Stop()
	}
	// Design: abstraction/ingress/design.md#shutdown
	if ing.retryPolicy != nil {
		stopPolicy(ing.retryPolicy) // ends an observing policy's own goroutine; later observations are discarded
	}
	ing.pool.CloseAll()
	ing.mu.Lock()
	for c := range ing.conns {
		_ = c.Close() // abandons forwards still in progress
	}
	ing.mu.Unlock()
	ing.wg.Wait()
	liveness := ing.monitor.Stop() // nil-safe

	runEnd := 0.0
	if started {
		runEnd = time.Since(runStart).Seconds()
	}
	if err := writeCancelsCSV(ing.cfg.OutputPath+".cancels.csv", records); err != nil {
		return err
	}
	if err := writeForwardsCSV(ing.cfg.OutputPath+".forwards.csv", ing.lb.ForwardLog(), runStart); err != nil {
		return err
	}
	base := runStart
	if !started && ing.monitor != nil {
		base = ing.monitor.base // no /ping: times from the monitor's start
	}
	if err := writePicksCSV(ing.cfg.OutputPath+".picks.csv", ing.lb.PickLog(), runStart); err != nil {
		return err
	}
	if err := writeLivenessCSV(ing.cfg.OutputPath+".liveness.csv", liveness, base); err != nil {
		return err
	}
	var runStartUnixNs uint64
	if started {
		// Design: abstraction/ingress/design.md#time-base
		runStartUnixNs = uint64(runStart.UnixNano()) // reported only
	}
	return ing.writeSummary(records, runEnd, runStartUnixNs)
}

type summaryConfig struct {
	ListenAddr          string   `json:"listen_addr"`
	Backends            []string `json:"backends"`
	NumClients          uint32   `json:"num_clients"`
	LbPolicy            string   `json:"lb_policy"`
	LbFilter            string   `json:"lb_filter"`
	EwmaBeta            float64  `json:"ewma_beta"`
	CancelRate          float64  `json:"cancel_rate"`
	DurationS           float64  `json:"duration_s"`
	InitialStallMs      uint64   `json:"initial_stall_ms"`
	HeartbeatTimeoutMs  uint64   `json:"heartbeat_timeout_ms"`
	LbRetryPolicy       string   `json:"lb_retry_policy"`
	LbMaxRetries        uint32   `json:"lb_max_retries"`
	LbRetryTimeoutMs    float64  `json:"lb_retry_timeout_ms"`
	LbRetryAnxietyRatio float64  `json:"lb_retry_anxiety_ratio"`
	LbRetryLateFactor   float64  `json:"lb_retry_late_factor"`
	Seed                uint64   `json:"seed"`
	OutputPath          string   `json:"output_path"`
}

// Summary has the field order of the Ingress summary JSON.
//
// API: abstraction/ingress/api.md#output_pathsummaryjson
type Summary struct {
	Config          summaryConfig       `json:"config"`
	RunEndS         float64             `json:"run_end_s"`
	RunStartUnixNs  uint64              `json:"run_start_unix_ns"` // 0 without /ping
	Counts          cancelCounts        `json:"counts"`
	RatesPerS       cancelRates         `json:"rates_per_s"`
	ClientsTargeted int                 `json:"clients_targeted"`
	FilterDropped   uint64              `json:"filter_dropped"` // dropped past the deadline while waiting
	FilterQueued    uint64              `json:"filter_queued"`
	FilterSentFromQ uint64              `json:"filter_sent_from_queue"`
	FilterQueueMax  uint64              `json:"filter_queue_max"`
	LbRetryRefused  uint64              `json:"lb_retry_refused"`
	LbRetries       uint64              `json:"lb_retries"` // attempts beyond each ForwardTask's first
	Backends        []BackendStatsValue `json:"backends"`
}

func (ing *Ingress) writeSummary(records []*CancelRecord, runEndS float64, runStartUnixNs uint64) error {
	c := ing.cfg
	counts, rates, targeted := summarizeCancels(records, c)
	fQueued, fSent, fDropped, fMax := ing.lb.filterQueueCounts()
	s := Summary{
		Config:  summaryConfig{c.ListenAddr, c.Backends, c.NumClients, c.LbPolicy, c.LbFilter, c.EwmaBeta, c.CancelRate, c.DurationS, c.InitialStallMs, c.HeartbeatTimeoutMs, c.LbRetryPolicy, c.LbMaxRetries, c.LbRetryTimeoutMs, c.LbRetryAnxietyRatio, c.LbRetryLateFactor, c.Seed, c.OutputPath},
		RunEndS: runEndS, RunStartUnixNs: runStartUnixNs, Counts: counts, RatesPerS: rates, ClientsTargeted: targeted, FilterDropped: fDropped, FilterQueued: fQueued, FilterSentFromQ: fSent, FilterQueueMax: fMax, LbRetryRefused: ing.lbRetryRefused.Load(), LbRetries: ing.lbRetries.Load(), Backends: ing.lb.Snapshot(),
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.OutputPath+".summary.json", append(b, '\n'), 0o644)
}

// writeForwardsCSV writes the ForwardLog with t_ns = end - run_start, sorted
// stably by t_ns (header only if no /ping was forwarded).
//
// API: abstraction/ingress/api.md#output_pathforwardscsv
// Design: abstraction/ingress/design.md#shutdown
func writeForwardsCSV(path string, recs []ForwardRecord, runStart time.Time) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	tNs := make([]int64, len(recs))
	order := make([]int, len(recs))
	for k, r := range recs {
		tNs[k] = r.End.Sub(runStart).Nanoseconds() // run_start is set before the first pick
		order[k] = k
	}
	sort.SliceStable(order, func(a, b int) bool { return tNs[order[a]] < tNs[order[b]] })
	w := bufio.NewWriter(f)
	w.WriteString("t_ns,server_id,outcome,latency_ns,ewma_latency_ns,exec_ns,ewma_exec_ns,attempt\n")
	for _, k := range order {
		r := recs[k]
		exec, ewmaExec := "", "" // empty when none
		if r.HasExec {
			exec = strconv.FormatUint(r.ExecNs, 10)
		}
		if r.HasEwmaExec {
			ewmaExec = strconv.FormatFloat(r.EwmaExecNs, 'f', -1, 64)
		}
		w.WriteString(strconv.FormatInt(tNs[k], 10) + "," + strconv.FormatUint(uint64(r.ServerID), 10) + "," +
			outcomeNames[r.Outcome] + "," + strconv.FormatUint(r.LatencyNs, 10) + "," +
			strconv.FormatFloat(r.EwmaLatencyNs, 'f', -1, 64) + "," + // shortest round-trip, no exponent
			exec + "," + ewmaExec + "," + strconv.FormatUint(uint64(r.Attempt), 10) + "\n")
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// writePicksCSV writes the PickLog with t_ns = t - run_start, sorted stably
// by t_ns (header only if nothing was picked).
//
// API: abstraction/ingress/api.md#output_pathpickscsv
// Design: abstraction/ingress/design.md#shutdown
func writePicksCSV(path string, recs []PickRecord, runStart time.Time) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	tNs := make([]int64, len(recs))
	order := make([]int, len(recs))
	for k, r := range recs {
		tNs[k] = r.T.Sub(runStart).Nanoseconds() // run_start is set before the first pick
		order[k] = k
	}
	sort.SliceStable(order, func(a, b int) bool { return tNs[order[a]] < tNs[order[b]] })
	w := bufio.NewWriter(f)
	w.WriteString("t_ns,server_id,outstanding,ewma_exec_ns,worker_count\n")
	for _, k := range order {
		r := recs[k]
		ewma := "" // empty before the backend's first ExecutionTime sample
		if r.HasEwmaExec {
			ewma = strconv.FormatFloat(r.EwmaExecNs, 'f', -1, 64)
		}
		w.WriteString(strconv.FormatInt(tNs[k], 10) + "," + strconv.FormatUint(uint64(r.ServerID), 10) + "," +
			strconv.FormatInt(r.Outstanding, 10) + "," + ewma + "," + strconv.FormatUint(r.WorkerCount, 10) + "\n")
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// writeLivenessCSV writes the LivenessLog with t_ns = at - base (signed; base
// is run_start, or the monitor's start without a /ping). Header only when the
// monitor is off.
//
// API: abstraction/ingress/heartbeat_monitor/api.md#output_pathlivenesscsv
// Design: abstraction/ingress/design.md#shutdown
func writeLivenessCSV(path string, recs []LivenessRecord, base time.Time) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	w.WriteString("t_ns,server_id,live\n")
	for _, r := range recs {
		live := "0"
		if r.Live {
			live = "1"
		}
		w.WriteString(strconv.FormatInt(r.At.Sub(base).Nanoseconds(), 10) + "," +
			strconv.FormatUint(uint64(r.ServerID), 10) + "," + live + "\n")
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
