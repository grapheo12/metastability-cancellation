// Source: abstraction/load_generator/client/design.md (client v0.3.0)
package loadgen

import (
	"bytes"
	"context"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
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

// startRequest opens one POST /ping stream and returns at once. The response
// goroutine and the SLO timer race through settle; the first wins.
//
// Design: abstraction/load_generator/client/design.md#start_request
func startRequest(clientID, seq uint32, tSched int64, conn *h2client.Conn, cfg Config, runStart time.Time, events chan<- clientEvent) {
	id := protocol.MakeRequestID(seq, clientID)
	body := protocol.BuildBody(id, cfg.RequestBytes)
	rec := RequestRecord{RequestID: id, ClientID: clientID, Seq: seq, ScheduledNs: tSched, Sent: true}

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, protocol.PingMethod, "http://"+conn.Authority+protocol.PingPath, bytes.NewReader(body))
	req.Header.Set("content-type", protocol.ContentTypeJSON)
	req.Header.Set("content-length", strconv.Itoa(len(body)))
	req.ContentLength = int64(len(body))

	var settled atomic.Bool
	settle := func(outcome Outcome, status int) bool {
		if !settled.CompareAndSwap(false, true) {
			return false
		}
		r := rec
		r.EndNs, r.Outcome, r.HTTPStatus = nowNs(runStart), outcome, status
		events <- clientEvent{rec: r, resolved: true}
		return true
	}

	rec.SendNs = nowNs(runStart)
	// Design: abstraction/load_generator/client/design.md#slocancellation
	timer := time.AfterFunc(time.Duration(rec.SendNs+cfg.SloNs-nowNs(runStart)), func() {
		if settle(OutcomeCancelled, 0) {
			cancel() // x/net/http2 sends RST_STREAM(CANCEL) for the open stream
		}
	})
	go func() {
		defer cancel()
		resp, err := conn.CC.RoundTrip(req)
		if err != nil {
			settle(OutcomeError, 0)
			return
		}
		b, err := io.ReadAll(resp.Body) // full response: up to END_STREAM
		resp.Body.Close()
		if err != nil {
			settle(OutcomeError, 0)
			return
		}
		if settle(classify(resp.StatusCode, b, id), resp.StatusCode) {
			timer.Stop()
		}
	}()
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
