// Source: abstraction/server/heartbeat/design.md (heartbeat v0.1.0)
package server

import (
	"net/http"
	"time"

	"testbed/internal/protocol"
)

// serveHeartbeat is the HeartbeatSender: 200 text/plain, then one
// HeartbeatLine every HeartbeatIntervalMs, flushed at once. FreezeWait before
// each line stops a frozen server's heartbeat; done is the stream's context
// (RST or connection close). It runs on the stream's handler goroutine.
//
// Design: abstraction/server/heartbeat/design.md#heartbeatsender
func serveHeartbeat(w http.ResponseWriter, done <-chan struct{}, task *StreamTask, cfg Config, q *WorkQueue) {
	interval := time.Duration(cfg.HeartbeatIntervalMs) * time.Millisecond
	w.Header().Set("content-type", protocol.ContentTypeText)
	w.WriteHeader(protocol.StatusStreamOK) // no content-length: x/net/http2 streams the body
	flusher, _ := w.(http.Flusher)
	for seq := uint64(0); ; seq++ {
		q.FreezeWait() // WorkerFreeze: blocks until Close once frozen
		if task.Cancelled.Load() || q.Closed() {
			return
		}
		// Design: abstraction/server/heartbeat/design.md#failure-and-edge-cases
		if _, err := w.Write(protocol.BuildHeartbeatLine(seq)); err != nil {
			return // stream reset or connection gone
		}
		if flusher != nil {
			flusher.Flush()
		}
		timer := time.NewTimer(interval) // the wait starts after each write
		select {
		case <-done:
			timer.Stop()
			task.Cancelled.Store(true)
			return
		case <-timer.C:
		}
	}
}
