// Source: abstraction/ingress/frontend/design.md (frontend v0.7.0)
package ingress

import (
	"bytes"
	"context"
	"io"
	"math"
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
	workers uint32 // the backend's x-worker-threads (WorkerCount), 0 if none; not relayed
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

// handlePing runs one ForwardTask: attempt 0, then the LbRetryLoop.
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

	deadline, hasDeadline := protocol.ParseDeadline(body) // RequestDeadline, for the DeadlineFilter and the RetryState
	task := &forwardTask{requestID: id, clientID: clientID, body: body, deadline: deadline, hasDeadline: hasDeadline,
		results: make(chan attemptResult, 1+int(ing.cfg.LbMaxRetries))} // capacity covers every attempt: sends never block
	// FilterWaitQueue: sent now, or wait until a scan sends or drops it.
	// Design: abstraction/ingress/frontend/design.md#forwardtask
	ticket, e := ing.lb.Admit(deadline, hasDeadline)
	if e != nil {
		select {
		case <-ctx.Done(): // client reset while waiting
			if ing.lb.Gone(e) {
				return fwdResponse{aborted: true} // removed at the next scan; nothing written
			}
			<-e.signal // a scan sent or dropped it just now: handle that below (the loop sees the reset)
		case <-e.signal:
		}
		t, sent := ing.lb.Result(e)
		if !sent { // past its deadline while waiting: 503 {"id"}, never forwarded
			return fwdResponse{status: protocol.StatusRejected, body: protocol.BuildCancelBody(id)}
		}
		ticket = t
	}
	ing.startAttempt(task, 0, &ticket) // attempt 0, already picked
	return ing.lbRetryLoop(ctx, task)
}

// forwardTask is one client /ping stream and its ForwardAttempts (oldest first).
// Only the ForwardTask's own goroutine touches attempts and their running/outcome.
//
// Design: abstraction/ingress/frontend/design.md#forwardtask
type forwardTask struct {
	requestID   uint64
	clientID    uint32
	body        []byte
	deadline    uint64
	hasDeadline bool
	attempts    []*forwardAttempt
	results     chan attemptResult
}

// forwardAttempt is one upstream forward of a ForwardTask.
//
// Design: abstraction/ingress/frontend/design.md#forwardattempt
type forwardAttempt struct {
	k       uint32
	ticket  ForwardTicket
	cancel  context.CancelFunc // the attempt's cancel signal: resets its upstream stream
	running bool
	outcome ForwardOutcome
}

// attemptResult is what an attempt reports once it has cleared and finished.
type attemptResult struct {
	a       *forwardAttempt
	resp    fwdResponse
	outcome ForwardOutcome
}

// remainingNs is the request's time left on this Ingress's wall clock.
//
// Design: abstraction/ingress/frontend/design.md#forwardtask
func (task *forwardTask) remainingNs() (int64, bool) {
	if !task.hasDeadline {
		return 0, false
	}
	return remaining(task.deadline, time.Now().UnixNano()), true
}

// startAttempt starts attempt k with the given ticket (attempt 0) or a new
// pick (an LbRetry), publishes its (replacing) InFlightEntry and starts its
// activity; false if the DeadlineFilter refused the retry.
//
// Design: abstraction/ingress/frontend/design.md#forwardattempt
func (ing *Ingress) startAttempt(task *forwardTask, k uint32, picked *ForwardTicket) bool {
	var ticket ForwardTicket
	if picked != nil { // attempt 0: already picked by Admit or a FilterWaitQueue scan
		ticket = *picked
	} else { // an LbRetry: pick now
		rem, has := task.remainingNs()
		t, ok := ing.lb.PickAttempt(rem, has, k) // ForwardTicket (Attempt = k): given back to Finish
		if !ok {
			ing.lbRetryRefused.Add(1) // summary lb_retry_refused; no attempt starts
			return false
		}
		ticket = t
	}
	if k > 0 {
		ing.lbRetries.Add(1) // summary lb_retries
	}
	actx, cancel := context.WithCancel(context.Background()) // not the client's: older attempts outlive the task
	a := &forwardAttempt{k: k, ticket: ticket, cancel: cancel, running: true}
	entry := &InFlightEntry{RequestID: task.requestID, ServerID: ticket.ServerID, Attempt: k}
	ing.inflight.Publish(entry) // replaces the previous attempt's entry
	task.attempts = append(task.attempts, a)
	go ing.attemptActivity(actx, task, a, entry)
	return true
}

// attemptActivity forwards, clears its own entry, finishes its own ticket and
// reports; it runs to the end even after the task has answered the client.
//
// Design: abstraction/ingress/frontend/design.md#forwardattempt
func (ing *Ingress) attemptActivity(ctx context.Context, task *forwardTask, a *forwardAttempt, entry *InFlightEntry) {
	resp, outcome := ing.forward(ctx, task.clientID, a.ticket.ServerID, task.body)
	a.cancel()                // release the context; the forward has ended
	ing.inflight.Clear(entry) // identity: a no-op once a newer attempt replaced it
	var execNs uint64
	var hasExec bool
	workers := uint32(0)
	if outcome == OutcomeOK || outcome == OutcomeRejected {
		// ExecReport of the relayed body (relayed unchanged); StartSlack unused here.
		execNs, hasExec, _, _ = protocol.ParseExecReport(resp.body)
		workers = resp.workers // WorkerCount, set by forward for relayed 202/503
	}
	// Design: abstraction/ingress/load_balancer/design.md#forwardticket
	ing.lb.Finish(a.ticket, outcome, execNs, hasExec, workers) // after Clear: an ok latency ends when the answer is in
	task.results <- attemptResult{a: a, resp: resp, outcome: outcome}
}

// LbRetryTick is LB_RETRY_TICK_MS.
//
// API: abstraction/ingress/frontend/api.md#constants
const LbRetryTick = 10 * time.Millisecond

// lbRetryLoop waits for the newest attempt's answer, consulting the RetryPolicy
// on its 502 and on every tick while budget remains; a client reset cancels
// every running attempt.
//
// Design: abstraction/ingress/frontend/design.md#lbretryloop
func (ing *Ingress) lbRetryLoop(ctx context.Context, task *forwardTask) fwdResponse {
	var tick <-chan time.Time
	if ing.cfg.LbRetryPolicy != RetryPolicyNone && ing.cfg.LbMaxRetries > 0 { // off: no ticker at all
		t := time.NewTicker(LbRetryTick)
		defer t.Stop()
		tick = t.C
	}
	for {
		newest := task.attempts[len(task.attempts)-1]
		budget := uint32(len(task.attempts)-1) < ing.cfg.LbMaxRetries
		select {
		case <-ctx.Done(): // client reset
			for _, a := range task.attempts {
				if a.running {
					a.cancel() // each ends reset and finishes itself
				}
			}
			return fwdResponse{aborted: true}
		case r := <-task.results:
			r.a.running, r.a.outcome = false, r.outcome
			if r.a != newest {
				continue // an older attempt: answer ignored
			}
			if r.outcome != OutcomeBadGateway {
				return r.resp // 202 / 503 / 400 relayed
			}
			if budget && ing.retryPolicy.Decide(ing.retryState(task), RetryEvent{Kind: EventAttemptEnded, Outcome: r.outcome}) == Retry {
				if ing.startAttempt(task, uint32(len(task.attempts)), nil) {
					continue
				}
			}
			return r.resp // 502 relayed
		case <-tick:
			if !budget || !newest.running {
				continue
			}
			if ing.retryPolicy.Decide(ing.retryState(task), RetryEvent{Kind: EventTick}) == Retry {
				ing.startAttempt(task, uint32(len(task.attempts)), nil) // refused: keep waiting on the newest
			}
		}
	}
}

// retryState builds the RetryState right before a decide.
//
// Design: abstraction/ingress/retry_policy/design.md#retrystate
func (ing *Ingress) retryState(task *forwardTask) RetryState {
	views := make([]AttemptView, len(task.attempts))
	for i, a := range task.attempts {
		views[i] = AttemptView{ServerID: a.ticket.ServerID, Start: a.ticket.Start, Running: a.running, Outcome: a.outcome}
	}
	rem, has := task.remainingNs()
	return RetryState{RequestID: task.requestID, RetriesSoFar: uint32(len(task.attempts) - 1),
		MaxRetries: ing.cfg.LbMaxRetries, Now: time.Now(), FirstStart: task.attempts[0].ticket.Start,
		RemainingNs: rem, HasDeadline: has, Attempts: views, Backends: ing.lb.backendViews()}
}

// forward sends the body unchanged on the pair's upstream connection and
// waits for the full answer or the attempt's cancel signal (ctx). Cancelling
// ctx makes the HTTP/2 transport reset the upstream stream with CANCEL.
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
	workers, _ := protocol.ParseWorkerCount(resp.Header.Get(protocol.WorkerThreadsHeader)) // WorkerCount; 0 if none
	switch resp.StatusCode {
	case protocol.StatusOK:
		return fwdResponse{status: resp.StatusCode, body: respBody, workers: workers}, OutcomeOK
	case protocol.StatusRejected:
		return fwdResponse{status: resp.StatusCode, body: respBody, workers: workers}, OutcomeRejected
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
	ing.runStart = time.Now() // one read: monotonic for run_start, wall clock for run_start_unix_ns
	if ing.cfg.CancelRate > 0 {
		ing.canceller = StartCanceller(ing.cfg, ing.inflight, ing.runStart)
	}
}

// remaining is deadline - now in exact signed arithmetic, saturated to int64.
//
// Design: abstraction/ingress/frontend/design.md#forwardtask
func remaining(deadline uint64, now int64) int64 {
	if now < 0 {
		now = 0
	}
	if deadline > math.MaxInt64 {
		if d := deadline - uint64(now); d <= math.MaxInt64 {
			return int64(d)
		}
		return math.MaxInt64
	}
	return int64(deadline) - now
}
