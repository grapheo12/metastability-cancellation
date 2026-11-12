// Source: abstraction/server/design.md (server v0.7.3)
package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/http2"

	"testbed/internal/sampling"
)

// Server ties the Listener to the process-wide CancelRegistry, CancelCounters,
// WorkQueue and WorkerPool.
//
// Design: abstraction/server/design.md#structure
type Server struct {
	cfg      Config
	ln       net.Listener
	registry *CancelRegistry
	counters *CancelCounters
	queue    *WorkQueue
	pool     *WorkerPool
	buf      *MessageBuffer // nil unless workload = message_buffer
	queueLog *os.File       // nil: no QueueLengthLog
	sampler  *QueueSampler  // started after "listening on"
	h2s      *http2.Server
	base     *http.Server // only carries the shutdown hook that sends GOAWAY

	mu           sync.Mutex
	handlers     map[*connectionHandler]struct{}
	wg           sync.WaitGroup
	shuttingDown atomic.Bool
}

// Listen creates the registry, counters, WorkQueue and WorkerPool, binds
// listen_addr and prints
// "listening on <addr>".
//
// Design: abstraction/server/design.md#listener
func Listen(cfg Config) (*Server, error) {
	if cfg.NWorkerThreads < 1 {
		return nil, errors.New("n_worker_threads must be >= 1") // an unvalidated Config would hang
	}
	s := &Server{
		cfg:      cfg,
		registry: NewCancelRegistry(CancelRegistryCapacity),
		counters: &CancelCounters{},
		// Design: abstraction/server/design.md#why-one-shared-workqueue
		queue:    NewWorkQueue(cfg.InitialStallMs),
		h2s:      &http2.Server{MaxConcurrentStreams: maxConcurrentStreams},
		base:     &http.Server{},
		handlers: map[*connectionHandler]struct{}{},
	}
	if err := http2.ConfigureServer(s.base, s.h2s); err != nil {
		return nil, err
	}
	if cfg.Workload == WorkloadMessageBuffer {
		// Design: abstraction/server/design.md#listener
		s.buf = NewMessageBuffer(cfg.WindowSize, cfg.NGCBinaryTreeSize, cfg.Seed) // full before the bind
	}
	if cfg.QueueLogPath != "" {
		f, err := os.Create(cfg.QueueLogPath)
		if err != nil {
			return nil, err
		}
		s.queueLog = f
	}
	ln, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		if s.queueLog != nil {
			_ = s.queueLog.Close()
		}
		return nil, err
	}
	s.ln = ln
	// Design: abstraction/server/design.md#concurrency-model
	s.pool = StartWorkerPool(cfg.NWorkerThreads, s.queue, cfg, s.registry, s.counters, s.buf)
	fmt.Println("listening on " + cfg.ListenAddr)
	if s.queueLog != nil {
		// Design: abstraction/server/design.md#listener
		s.sampler = StartQueueSampler(s.queue, s.queueLog, cfg.QueueSampleIntervalMs)
	}
	return s, nil
}

// Addr returns the bound address (useful when listen_addr uses port 0).
func (s *Server) Addr() net.Addr { return s.ln.Addr() }

// Counters exposes the CancelCounters (read-only use).
func (s *Server) Counters() *CancelCounters { return s.counters }

// Serve accepts connections until Shutdown, giving connection conn_index
// (accept order, from 0) its own derived Rng.
//
// Design: abstraction/server/design.md#listener
func (s *Server) Serve() {
	var connIndex uint32
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if s.shuttingDown.Load() || errors.Is(err, net.ErrClosed) {
				return
			}
			fmt.Fprintln(os.Stderr, "accept:", err)
			continue
		}
		seed := sampling.DeriveSeed(s.cfg.Seed, sampling.StreamServerConnection, connIndex)
		h := newConnectionHandler(conn, s.cfg, sampling.NewRng(seed), s.registry, s.queue)
		if !s.track(h) {
			_ = conn.Close()
			return
		}
		go func() {
			defer s.wg.Done()
			defer s.untrack(h)
			h.run(s.h2s, s.base)
		}()
		connIndex++
	}
}

func (s *Server) track(h *connectionHandler) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shuttingDown.Load() {
		return false
	}
	s.handlers[h] = struct{}{}
	s.wg.Add(1)
	return true
}

func (s *Server) untrack(h *connectionHandler) {
	s.mu.Lock()
	delete(s.handlers, h)
	s.mu.Unlock()
}

// Shutdown stops accepting, closes the WorkQueue (queued jobs are answered
// Aborted), sends GOAWAY (best effort, via the base server's shutdown hook),
// closes every connection, waits for all workers and handlers to return and
// prints the CancelCounters line.
//
// Design: abstraction/server/design.md#shutdown
func (s *Server) Shutdown() {
	s.mu.Lock()
	s.shuttingDown.Store(true)
	s.mu.Unlock()
	_ = s.ln.Close()
	if s.sampler != nil {
		s.sampler.Stop() // no extra row; the file is closed
	}
	s.queue.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	_ = s.base.Shutdown(ctx) // triggers GOAWAY on every http2 connection
	cancel()

	s.mu.Lock()
	for h := range s.handlers {
		h.close()
	}
	s.mu.Unlock()
	s.pool.Join()
	s.wg.Wait()

	// API: abstraction/server/api.md#cancelcounters-output
	before, during, completed := s.counters.Snapshot()
	fmt.Printf("{\"cancelled_before_processing\":%d,\"cancelled_during_processing\":%d,\"completed_before_cancel\":%d}\n",
		before, during, completed)
}

// ProcessStartLine is the server's first stdout line: its wall-clock start in
// Unix nanoseconds, so process-relative times (the GcTrace) can be aligned.
//
// Design: abstraction/server/design.md#processstart
// API: abstraction/server/api.md#processstart-output
func ProcessStartLine(now time.Time) string {
	return "process_start_unix_ns " + strconv.FormatInt(now.UnixNano(), 10)
}
