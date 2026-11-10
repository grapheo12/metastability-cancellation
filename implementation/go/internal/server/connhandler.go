// Source: abstraction/server/connection_handler/design.md (connection_handler v0.3.0)
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

// maxConcurrentStreams is advertised in SETTINGS. It bounds how many requests
// one connection can have admitted at once; the WorkQueue itself is unbounded.
const maxConcurrentStreams = 4096

// StreamTask is one fully received request. Cancelled is the RST flag.
//
// API: abstraction/server/connection_handler/api.md#streamtask
type StreamTask struct {
	Method    string
	Path      string
	Body      []byte
	Cancelled atomic.Bool
}

// Response is what the router or a worker hands to the stream for writing.
// Aborted means "write nothing".
type Response struct {
	Status  int
	Body    []byte
	Aborted bool
}

// connectionHandler owns one accepted connection. In Go the per-stream
// http.Handler goroutines of x/net/http2 play the FrameReader's per-request
// part and the stream responder; each holds its *StreamTask, which replaces
// the design's stream_table.
//
// Design: abstraction/server/connection_handler/design.md#shared-state
type connectionHandler struct {
	conn     net.Conn
	cfg      Config
	registry *CancelRegistry
	queue    *WorkQueue

	// mu is the admission lock: it guards rng, connState and closing, and
	// makes admit_ping + queue_push one step per connection.
	mu        sync.Mutex
	rng       *sampling.Rng
	connState *ConnectionCancelState
	closing   bool
	streams   sync.WaitGroup // live stream handler goroutines
}

func newConnectionHandler(conn net.Conn, cfg Config, rng *sampling.Rng, registry *CancelRegistry, queue *WorkQueue) *connectionHandler {
	return &connectionHandler{
		conn:      conn,
		cfg:       cfg,
		registry:  registry,
		queue:     queue,
		rng:       rng,
		connState: NewConnectionCancelState(),
	}
}

// run serves the connection until it ends, then waits for every stream
// handler (each ends once its reply is filled). Workers run on their own
// goroutines, so the HTTP/2 read loop keeps processing RST_STREAM and /cancel
// while they burn.
//
// Design: abstraction/server/connection_handler/design.md#concurrency-requirement
func (h *connectionHandler) run(h2s *http2.Server, base *http.Server) {
	h2s.ServeConn(h.conn, &http2.ServeConnOpts{BaseConfig: base, Handler: http.HandlerFunc(h.frameReader)})
	// ServeConn returned: peer GOAWAY/EOF, protocol error, or our close().
	h.close()
	h.streams.Wait()
}

// frameReader is the per-stream half of the FrameReader plus the stream
// responder: it runs once the request is fully received (END_STREAM), routes
// it, and for a queued /ping waits for the worker's reply, mapping a stream
// reset (or the connection closing) to the RST flag.
//
// Design: abstraction/server/connection_handler/design.md#framereader
// Design: abstraction/server/connection_handler/design.md#state-machine-per-ping-streamtask
func (h *connectionHandler) frameReader(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		panic(http.ErrAbortHandler) // reset before END_STREAM: never becomes a StreamTask
	}
	if !h.addStream() {
		panic(http.ErrAbortHandler)
	}
	defer h.streams.Done()
	task := &StreamTask{Method: r.Method, Path: r.URL.Path, Body: body}
	resp, job := h.route(task)
	if job != nil {
		select {
		case resp = <-job.reply:
		case <-r.Context().Done(): // RST_STREAM, or the connection is gone
			task.Cancelled.Store(true)
			resp = <-job.reply
		}
	}
	h.writeResponse(w, task, resp)
}

// addStream registers a live stream handler unless the connection is closing,
// so streams.Add never races with the Wait in run.
func (h *connectionHandler) addStream() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closing {
		return false
	}
	h.streams.Add(1)
	return true
}

// route answers everything except an admitted /ping, which it pushes onto
// the shared WorkQueue and returns as a job. It never waits for a worker.
//
// Design: abstraction/server/connection_handler/design.md#routing
func (h *connectionHandler) route(task *StreamTask) (Response, *PingJob) {
	switch task.Path {
	case protocol.PingPath:
		if task.Method != protocol.PingMethod {
			return Response{Status: protocol.StatusMethodNotAllowed}, nil
		}
		return h.admit(task)
	case protocol.CancelPath:
		if task.Method != protocol.CancelMethod {
			return Response{Status: protocol.StatusMethodNotAllowed}, nil
		}
		return HandleCancel(task.Body, h.cfg, h.registry), nil // never queued
	}
	return Response{Status: protocol.StatusNotFound}, nil
}

// admit runs admit_ping and queue_push as one step under the admission lock,
// because x/net/http2 runs the stream handlers of one connection concurrently.
//
// Design: abstraction/server/connection_handler/design.md#admission
func (h *connectionHandler) admit(task *StreamTask) (Response, *PingJob) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closing {
		return Response{Aborted: true}, nil
	}
	job, resp := admitPing(task, h.cfg, h.rng, h.connState)
	if job == nil {
		return resp, nil // 400: never queued, no sample drawn
	}
	if h.queue.Push(job) != nil {
		return Response{Aborted: true}, nil // server shutting down
	}
	return Response{}, job
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

// close stops admitting and closes the socket, which ends ServeConn and fires
// every live stream's context, setting its RST flag. A running burn is not
// interrupted.
//
// Design: abstraction/server/connection_handler/design.md#connection-close
func (h *connectionHandler) close() {
	h.mu.Lock()
	h.closing = true
	h.mu.Unlock()
	_ = h.conn.Close()
}
