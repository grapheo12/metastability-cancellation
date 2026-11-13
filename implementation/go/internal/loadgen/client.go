// Source: abstraction/load_generator/client/design.md (client v0.5.0)
package loadgen

import (
	"bytes"
	"context"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"testbed/internal/h2client"
	"testbed/internal/protocol"
	"testbed/internal/sampling"
)

// Outcome of one scheduled arrival.
//
// API: abstraction/load_generator/client/api.md#outcome
type Outcome string

const (
	OutcomeOK        Outcome = "ok"
	OutcomeRejected  Outcome = "rejected"
	OutcomeCancelled Outcome = "cancelled"
	OutcomeDropped   Outcome = "dropped"
	OutcomeError     Outcome = "error"
	OutcomeNotSent   Outcome = "not_sent"
)

// clientEvent is what the receiver consumes: a not_sent arrival, a resolved
// request (both carry a record), or the sender's end.
type clientEvent struct {
	rec        RequestRecord
	resolved   bool // a sent request resolved: release its slot
	senderDone bool
}

// RunClient runs one SemiOpenLoopClient and returns its records in seq order.
// The sender runs on the calling goroutine; the receiver on its own.
// outstanding is the fill level of the slots channel (a counting semaphore of
// size n_waiting_requests): the sender fills a slot to send, the receiver
// empties one per resolved request.
//
// Design: abstraction/load_generator/client/design.md#semiopenloopclient
// Design: abstraction/load_generator/client/design.md#state-machine
func RunClient(clientID uint32, conn *h2client.Conn, cfg Config, rng *sampling.Rng, runStart time.Time) []RequestRecord {
	slots := make(chan struct{}, cfg.NWaitingRequests)
	// Buffered so that settle and the sender rarely wait; the receiver always drains it.
	events := make(chan clientEvent, int(cfg.NWaitingRequests)+1)
	var records []RequestRecord
	recvDone := make(chan struct{})
	go func() { // receiver
		defer close(recvDone)
		senderDone := false
		for ev := range events {
			switch {
			case ev.senderDone:
				senderDone = true
			case ev.resolved:
				records = append(records, ev.rec)
				<-slots
			default:
				records = append(records, ev.rec) // not_sent
			}
			if senderDone && len(slots) == 0 {
				return
			}
		}
	}()

	d := cfg.DurationNs
	tSched := addGap(0, rng.ExponentialNs(cfg.PerClientRate))
	for seq := uint64(0); tSched < d; seq++ {
		sleepUntil(runStart, tSched)
		// Design: abstraction/load_generator/client/design.md#gatedarrival
		slots <- struct{}{} // blocks while n_waiting_requests are outstanding
		if nowNs(runStart) >= d {
			<-slots
			events <- clientEvent{rec: RequestRecord{
				RequestID: protocol.MakeRequestID(uint32(seq), clientID), ClientID: clientID, Seq: uint32(seq),
				Outcome: OutcomeNotSent, ScheduledNs: tSched,
			}}
		} else {
			startRequest(clientID, uint32(seq), tSched, conn, cfg, runStart, events)
		}
		if seq == math.MaxUint32 {
			break // seq would leave u32: stop scheduling
		}
		tSched = addGap(tSched, rng.ExponentialNs(cfg.PerClientRate))
	}
	events <- clientEvent{senderDone: true}
	<-recvDone
	sort.Slice(records, func(i, j int) bool { return records[i].Seq < records[j].Seq })
	return records
}

// pendingRequest is one sent logical request: its RequestCopies, the newest
// copy's index and the first-event-wins flag.
type pendingRequest struct {
	id       uint64
	body     []byte
	rec      RequestRecord // id, client, seq, scheduled and first-send times
	conn     *h2client.Conn
	cfg      Config
	runStart time.Time
	events   chan<- clientEvent
	sloTimer *time.Timer
	stall    bool // StallWindowRequest: first sent before initial_stall_ns, never retries

	mu       sync.Mutex           // guards current, cancels, resetAll
	current  uint32               // index of the newest RequestCopy
	cancels  []context.CancelFunc // per copy opened: cancelling sends RST_STREAM(CANCEL)
	resetAll bool                 // SLOCancellation has reset every copy
	settled  atomic.Bool
}

// startRequest sends copy 0 of one request and returns at once. Responses,
// RetryTimers and the SLO timer race through settle; the first wins.
//
// Design: abstraction/load_generator/client/design.md#start_request
func startRequest(clientID, seq uint32, tSched int64, conn *h2client.Conn, cfg Config, runStart time.Time, events chan<- clientEvent) {
	id := protocol.MakeRequestID(seq, clientID)
	r := &pendingRequest{id: id, body: protocol.BuildBody(id, cfg.RequestBytes), conn: conn, cfg: cfg,
		runStart: runStart, events: events,
		rec: RequestRecord{RequestID: id, ClientID: clientID, Seq: seq, ScheduledNs: tSched, Sent: true}}
	r.rec.SendNs = nowNs(runStart)
	// Design: abstraction/load_generator/client/design.md#stallwindow
	r.stall = r.rec.SendNs < cfg.InitialStallNs
	r.rec.InStallWindow = r.stall
	// Design: abstraction/load_generator/client/design.md#slocancellation
	r.sloTimer = time.AfterFunc(time.Duration(r.rec.SendNs+cfg.SloNs-nowNs(runStart)), func() {
		if !r.settle(OutcomeCancelled, 0) {
			return
		}
		r.mu.Lock()
		r.resetAll = true
		cancels := append([]context.CancelFunc(nil), r.cancels...)
		r.mu.Unlock()
		for _, cancel := range cancels {
			cancel() // x/net/http2 sends RST_STREAM(CANCEL) for each open copy
		}
	})
	r.sendCopy(0)
}

// settle records the request once; later calls return false.
//
// Design: abstraction/load_generator/client/design.md#start_request
func (r *pendingRequest) settle(outcome Outcome, status int) bool {
	if !r.settled.CompareAndSwap(false, true) {
		return false
	}
	rec := r.rec
	rec.EndNs, rec.Outcome, rec.HTTPStatus = nowNs(r.runStart), outcome, status
	r.mu.Lock()
	rec.Copies = r.current + 1
	r.mu.Unlock()
	if outcome != OutcomeCancelled {
		r.sloTimer.Stop()
	}
	r.events <- clientEvent{rec: rec, resolved: true}
	return true
}

// newest reports whether copy k is still the copy whose answer counts.
func (r *pendingRequest) newest(k uint32) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return k == r.current
}

// sendCopy opens RequestCopy k (r.current == k already) and starts its
// RetryTimer. Called without r.mu. RoundTrip opens the stream and waits for
// the response on the copy's own goroutine; an earlier copy's answer or error
// is ignored, and its stream stays open until it ends or SLOCancellation.
//
// Design: abstraction/load_generator/client/design.md#retrytimer
func (r *pendingRequest) sendCopy(k uint32) {
	tCopy := nowNs(r.runStart)
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, protocol.PingMethod, "http://"+r.conn.Authority+protocol.PingPath, bytes.NewReader(r.body))
	req.Header.Set("content-type", protocol.ContentTypeJSON)
	req.Header.Set("content-length", strconv.Itoa(len(r.body)))
	req.ContentLength = int64(len(r.body))
	r.mu.Lock()
	r.cancels = append(r.cancels, cancel)
	late := r.resetAll
	r.mu.Unlock()
	if late { // SLOCancellation already ran
		cancel()
		return
	}
	go func() {
		defer cancel()
		resp, err := r.conn.CC.RoundTrip(req)
		if err != nil {
			if r.newest(k) {
				r.settle(OutcomeError, 0)
			}
			return
		}
		b, err := io.ReadAll(resp.Body) // full response: up to END_STREAM
		resp.Body.Close()
		if !r.newest(k) {
			return // superseded copy: ignored
		}
		if err != nil {
			r.settle(OutcomeError, 0)
			return
		}
		r.settle(classify(resp.StatusCode, b, r.id), resp.StatusCode)
	}()
	if r.stall {
		return // StallWindowRequest: no RetryTimer
	}
	// RetryTimer of copy k.
	// Design: abstraction/load_generator/client/design.md#retrytimer
	time.AfterFunc(time.Duration(tCopy+r.cfg.RetryTimeoutNs-nowNs(r.runStart)), func() {
		r.mu.Lock()
		if r.settled.Load() || k != r.current {
			r.mu.Unlock()
			return
		}
		last := k == r.cfg.MaxRetries
		if !last {
			r.current = k + 1
		}
		r.mu.Unlock()
		if last {
			r.settle(OutcomeDropped, 0) // no reset: the copies are abandoned
		} else {
			r.sendCopy(k + 1)
		}
	})
}

// classify maps a full /ping response to an Outcome.
//
// Design: abstraction/load_generator/client/design.md#outcome-classification
// Design: abstraction/protocol/design.md#status-classification-load-generator-client
func classify(status int, respBody []byte, id uint64) Outcome {
	parsed, err := protocol.ParseBody(respBody)
	if err != nil || parsed != id {
		return OutcomeError
	}
	switch status {
	case protocol.StatusOK:
		return OutcomeOK
	case protocol.StatusRejected:
		return OutcomeRejected
	}
	return OutcomeError
}
