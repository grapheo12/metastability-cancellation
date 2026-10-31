// Source: abstraction/server/connection_handler/design.md (connection_handler v0.2.1)
package server

import (
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"

	"golang.org/x/net/http2"

	"testbed/internal/protocol"
	"testbed/internal/sampling"
)

// maxConcurrentStreams is advertised in SETTINGS. The queue capacity equals it,
// so a push from a live stream handler never blocks.
const maxConcurrentStreams = 4096

// StreamTask is one fully received request. Cancelled is the RST flag.
//
// API: abstraction/server/connection_handler/api.md#streamtask
type StreamTask struct {
	Method    string
	Path      string
	Body      []byte
	Cancelled atomic.Bool
	result    chan Response // capacity 1: worker -> stream handler goroutine
}

// Response is what the worker hands back to the stream for writing.
// Aborted means "write nothing".
type Response struct {
	Status  int
	Body    []byte
	Aborted bool
}

// connectionHandler owns one accepted connection. In Go the per-stream
// http.Handler goroutines of x/net/http2 play the FrameReader role and each
// holds its *StreamTask, which replaces the design's stream_table.
//
// Design: abstraction/server/connection_handler/design.md#shared-state
type connectionHandler struct {
	conn      net.Conn
	cfg       Config
	rng       *sampling.Rng
	cancelCtx *cancelContext
	queue     chan *StreamTask
	mu        sync.Mutex // guards closing against concurrent enqueue
	closing   bool
	closed    chan struct{}
}

func newConnectionHandler(conn net.Conn, cfg Config, rng *sampling.Rng, registry *CancelRegistry, counters *CancelCounters) *connectionHandler {
	return &connectionHandler{
		conn: conn,
		cfg:  cfg,
		rng:  rng,
		cancelCtx: &cancelContext{
			state:    NewConnectionCancelState(),
			registry: registry,
			counters: counters,
		},
		queue:  make(chan *StreamTask, maxConcurrentStreams),
		closed: make(chan struct{}),
	}
}

// run serves the connection until it ends. The Go scheduler preempts
// goroutines asynchronously and runs them on several OS threads, so the HTTP/2
// read loop keeps processing RST_STREAM while the worker burns CPU.
//
// Design: abstraction/server/connection_handler/design.md#concurrency-requirement
func (h *connectionHandler) run(h2s *http2.Server, base *http.Server) {
	workerDone := make(chan struct{})
	go func() {
		h.serialStreamWorker()
		close(workerDone)
	}()
	h2s.ServeConn(h.conn, &http2.ServeConnOpts{BaseConfig: base, Handler: http.HandlerFunc(h.frameReader)})
	// ServeConn returned: peer GOAWAY/EOF, protocol error, or our close().
	h.close()
	<-workerDone
}

// frameReader is the per-stream half of the FrameReader: it runs once the
// request is fully received (END_STREAM) and maps a stream reset to the RST flag.
//
// Design: abstraction/server/connection_handler/design.md#framereader
// Design: abstraction/server/connection_handler/design.md#state-machine-per-ping-streamtask
func (h *connectionHandler) frameReader(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		panic(http.ErrAbortHandler) // reset before END_STREAM: never becomes a StreamTask
	}
	task := &StreamTask{Method: r.Method, Path: r.URL.Path, Body: body, result: make(chan Response, 1)}
	if !h.enqueue(task) {
		panic(http.ErrAbortHandler)
	}
	var resp Response
	select {
	case resp = <-task.result:
	case <-r.Context().Done(): // RST_STREAM (or connection gone)
		task.Cancelled.Store(true)
		resp = <-task.result
	}
	h.writeResponse(w, task, resp)
}

func (h *connectionHandler) enqueue(task *StreamTask) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closing {
		return false
	}
	h.queue <- task // never blocks: at most maxConcurrentStreams handlers are live
	return true
}

// serialStreamWorker runs tasks one at a time in the order they were queued.
//
// Design: abstraction/server/connection_handler/design.md#serialstreamworker
func (h *connectionHandler) serialStreamWorker() {
	for {
		select {
		case <-h.closed:
			h.dropQueued()
			return
		case task := <-h.queue:
			if h.isClosing() {
				task.result <- Response{Aborted: true}
				h.dropQueued()
				return
			}
			task.result <- h.route(task)
		}
	}
}

// Design: abstraction/server/connection_handler/design.md#routing
func (h *connectionHandler) route(task *StreamTask) Response {
	switch task.Path {
	case protocol.PingPath:
		if task.Method != protocol.PingMethod {
			return Response{Status: protocol.StatusMethodNotAllowed}
		}
		return handlePing(task, h.cfg, h.rng, h.cancelCtx)
	case protocol.CancelPath:
		if task.Method != protocol.CancelMethod {
			return Response{Status: protocol.StatusMethodNotAllowed}
		}
		return HandleCancel(task.Body, h.cfg, h.cancelCtx.registry)
	}
	return Response{Status: protocol.StatusNotFound}
}

// writeResponse runs on the stream's handler goroutine, as x/net/http2
// requires. "Write nothing" is a panic with http.ErrAbortHandler, which
// resets the stream instead of letting the library send an empty 200.
//
// Design: abstraction/server/connection_handler/design.md#response-writing
func (h *connectionHandler) writeResponse(w http.ResponseWriter, task *StreamTask, resp Response) {
	if resp.Aborted {
		panic(http.ErrAbortHandler)
	}
	if h.cfg.CancelMode == CancelCooperative && task.Cancelled.Load() {
		panic(http.ErrAbortHandler)
	}
	hdr := w.Header()
	if len(resp.Body) > 0 {
		hdr.Set("content-type", protocol.ContentTypeJSON)
	}
	hdr.Set("content-length", strconv.Itoa(len(resp.Body)))
	w.WriteHeader(resp.Status)
	_, _ = w.Write(resp.Body) // errors on a reset stream are ignored
}

// close stops accepting tasks, wakes the worker and closes the socket, which
// ends ServeConn. A running burn is not interrupted.
//
// Design: abstraction/server/connection_handler/design.md#connection-close
func (h *connectionHandler) close() {
	h.mu.Lock()
	if !h.closing {
		h.closing = true
		close(h.closed)
	}
	h.mu.Unlock()
	_ = h.conn.Close()
}

func (h *connectionHandler) isClosing() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closing
}

// dropQueued answers every queued task with Aborted so its handler returns.
//
// Design: abstraction/server/connection_handler/design.md#connection-close
func (h *connectionHandler) dropQueued() {
	for {
		select {
		case t := <-h.queue:
			t.result <- Response{Aborted: true}
		default:
			return
		}
	}
}
