// Source: abstraction/ingress/design.md (ingress v0.1.14)
package ingress

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
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
	h2s      *http2.Server
	base     *http.Server // only carries the shutdown hook that sends GOAWAY

	started   atomic.Bool
	startMu   sync.Mutex // guards runStart and canceller after started is set
	runStart  time.Time
	canceller *Canceller

	mu           sync.Mutex
	conns        map[net.Conn]struct{}
	wg           sync.WaitGroup
	shuttingDown atomic.Bool
}

// Listen creates the shared state, binds listen_addr and prints
// "listening on <addr>". No backend is contacted.
//
// API: abstraction/ingress/api.md#lifecycle
// Design: abstraction/ingress/design.md#structure
func Listen(cfg Config) (*Ingress, error) {
	ing := &Ingress{
		cfg:      cfg,
		inflight: NewInFlightMap(cfg.NumClients),
		lb:       NewLoadBalancer(cfg),
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
	ing.pool.CloseAll()
	ing.mu.Lock()
	for c := range ing.conns {
		_ = c.Close() // abandons forwards still in progress
	}
	ing.mu.Unlock()
	ing.wg.Wait()

	runEnd := 0.0
	if started {
		runEnd = time.Since(runStart).Seconds()
	}
	if err := writeCancelsCSV(ing.cfg.OutputPath+".cancels.csv", records); err != nil {
		return err
	}
	return ing.writeSummary(records, runEnd)
}

type summaryConfig struct {
	ListenAddr     string   `json:"listen_addr"`
	Backends       []string `json:"backends"`
	NumClients     uint32   `json:"num_clients"`
	LbPolicy       string   `json:"lb_policy"`
	EwmaBeta       float64  `json:"ewma_beta"`
	CancelRate     float64  `json:"cancel_rate"`
	DurationS      float64  `json:"duration_s"`
	InitialStallMs uint64   `json:"initial_stall_ms"`
	Seed           uint64   `json:"seed"`
	OutputPath     string   `json:"output_path"`
}

// Summary has the field order of the Ingress summary JSON.
//
// API: abstraction/ingress/api.md#output_pathsummaryjson
type Summary struct {
	Config          summaryConfig       `json:"config"`
	RunEndS         float64             `json:"run_end_s"`
	Counts          cancelCounts        `json:"counts"`
	RatesPerS       cancelRates         `json:"rates_per_s"`
	ClientsTargeted int                 `json:"clients_targeted"`
	Backends        []BackendStatsValue `json:"backends"`
}

func (ing *Ingress) writeSummary(records []*CancelRecord, runEndS float64) error {
	c := ing.cfg
	counts, rates, targeted := summarizeCancels(records, c)
	s := Summary{
		Config:  summaryConfig{c.ListenAddr, c.Backends, c.NumClients, c.LbPolicy, c.EwmaBeta, c.CancelRate, c.DurationS, c.InitialStallMs, c.Seed, c.OutputPath},
		RunEndS: runEndS, Counts: counts, RatesPerS: rates, ClientsTargeted: targeted, Backends: ing.lb.Snapshot(),
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.OutputPath+".summary.json", append(b, '\n'), 0o644)
}
