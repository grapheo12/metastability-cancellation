// Source: abstraction/ingress/frontend/design.md (frontend v0.2.0)
package ingress

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strconv"
	"time"

	"testbed/internal/protocol"
)

// fwdResponse is a Response, or Aborted when the client reset its stream.
type fwdResponse struct {
	status  int
	body    []byte
	aborted bool
}

// handle is the per-stream entry point. ServeConn runs it on its own
// goroutine once the request is complete (END_STREAM), while the
// connection's frame reader keeps running, so a client RST cancels
// r.Context() at once.
//
// Design: abstraction/ingress/frontend/design.md#client-connections
func (ing *Ingress) handle(w http.ResponseWriter, r *http.Request) {
	var resp fwdResponse
	switch {
	case r.URL.Path != protocol.PingPath:
		resp = fwdResponse{status: protocol.StatusNotFound}
	case r.Method != protocol.PingMethod:
		resp = fwdResponse{status: protocol.StatusMethodNotAllowed}
	default:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			panic(http.ErrAbortHandler) // reset before END_STREAM
		}
		resp = ing.handlePing(r.Context(), body)
	}
	if resp.aborted || r.Context().Err() != nil {
		return // reset: nothing is written
	}
	if len(resp.body) > 0 {
		w.Header().Set("content-type", protocol.ContentTypeJSON)
	}
	w.Header().Set("content-length", strconv.Itoa(len(resp.body)))
	w.WriteHeader(resp.status)
	_, _ = w.Write(resp.body) // errors on a reset stream are ignored
}

// handlePing runs one ForwardTask.
//
// Design: abstraction/ingress/frontend/design.md#forwardtask
func (ing *Ingress) handlePing(ctx context.Context, body []byte) fwdResponse {
	id, err := protocol.ParseBody(body)
	if err != nil {
		return fwdResponse{status: protocol.StatusBadRequest}
	}
	_, clientID := protocol.SplitRequestID(id)
	if clientID >= ing.cfg.NumClients {
		return fwdResponse{status: protocol.StatusBadRequest}
	}
	ing.startOnce()

	ticket := ing.lb.Pick()
	s := ticket.ServerID
	entry := &InFlightEntry{RequestID: id, ServerID: s}
	ing.inflight.Publish(entry) // keyed by the RequestId
	resp, outcome := ing.forward(ctx, clientID, s, body)
	ing.inflight.Clear(entry) // before relaying the answer
	ing.lb.Finish(ticket, outcome)
	return resp
}

// forward sends the body unchanged on the pair's upstream connection and
// waits for the full answer or the client's reset. Cancelling ctx makes the
// HTTP/2 transport reset the upstream stream with CANCEL.
//
// Design: abstraction/ingress/frontend/design.md#forwardtask
func (ing *Ingress) forward(ctx context.Context, c, s uint32, body []byte) (fwdResponse, ForwardOutcome) {
	badGateway := fwdResponse{status: protocol.StatusBadGateway}
	conn, err := ing.pool.Get(c, s)
	if err != nil {
		return badGateway, OutcomeBadGateway
	}
	req, _ := http.NewRequestWithContext(ctx, protocol.PingMethod, "http://"+conn.Authority+protocol.PingPath,
		bytes.NewReader(body))
	req.Header.Set("content-type", protocol.ContentTypeJSON)
	req.ContentLength = int64(len(body))
	fail := func() (fwdResponse, ForwardOutcome) {
		if ctx.Err() != nil { // the client reset first: reset wins
			return fwdResponse{aborted: true}, OutcomeReset
		}
		if !conn.CC.CanTakeNewRequest() { // the whole connection is unusable
			ing.pool.Discard(c, s, conn)
		}
		return badGateway, OutcomeBadGateway
	}
	resp, err := conn.CC.RoundTrip(req)
	if err != nil {
		return fail()
	}
	respBody, err := io.ReadAll(resp.Body) // "completes" = END_STREAM received
	resp.Body.Close()
	if err != nil {
		return fail()
	}
	switch resp.StatusCode {
	case protocol.StatusOK:
		return fwdResponse{status: resp.StatusCode, body: respBody}, OutcomeOK
	case protocol.StatusRejected:
		return fwdResponse{status: resp.StatusCode, body: respBody}, OutcomeRejected
	case protocol.StatusBadRequest:
		return fwdResponse{status: resp.StatusCode, body: respBody}, OutcomeBadRequest
	}
	return badGateway, OutcomeBadGateway
}

// startOnce sets run_start and starts the Load Canceller on the first /ping.
//
// Design: abstraction/ingress/frontend/design.md#startonce
// Design: abstraction/ingress/design.md#time-base
func (ing *Ingress) startOnce() {
	if !ing.started.CompareAndSwap(false, true) {
		return
	}
	ing.startMu.Lock()
	defer ing.startMu.Unlock()
	ing.runStart = time.Now()
	if ing.cfg.CancelRate > 0 {
		ing.canceller = StartCanceller(ing.cfg, ing.inflight, ing.runStart)
	}
}
