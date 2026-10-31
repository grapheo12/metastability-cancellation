// Source: abstraction/load_generator/client/design.md (client v0.1.1)
package loadgen

import (
	"bytes"
	"context"
	"io"
	"math"
	"net/http"
	"strconv"
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

// RunClient runs one ClosedLoopPoissonClient and returns its records in seq order.
//
// Design: abstraction/load_generator/client/design.md#closedlooppoissonclient
// Design: abstraction/load_generator/client/design.md#state-machine
func RunClient(clientID uint32, conn *h2client.Conn, cfg Config, rng *sampling.Rng, runStart time.Time) []RequestRecord {
	var records []RequestRecord
	var seq uint64
	d := cfg.DurationNs
	tSched := addGap(0, rng.ExponentialNs(cfg.PerClientRate))
	for tSched < d {
		if seq > math.MaxUint32 {
			break // seq would leave u32: stop scheduling
		}
		// Design: abstraction/load_generator/client/design.md#gatedarrival
		sleepUntil(runStart, tSched)
		if nowNs(runStart) >= d {
			records = append(records, RequestRecord{
				RequestID: protocol.MakeRequestID(uint32(seq), clientID), ClientID: clientID, Seq: uint32(seq),
				Outcome: OutcomeNotSent, ScheduledNs: tSched,
			})
		} else {
			records = append(records, sendOne(clientID, uint32(seq), tSched, conn, cfg, runStart))
		}
		seq++
		tSched = addGap(tSched, rng.ExponentialNs(cfg.PerClientRate))
	}
	return records
}

type pingResult struct {
	status int
	body   []byte
	err    error
}

// sendOne sends one POST /ping and blocks until it resolves (response, SLO, or error).
//
// Design: abstraction/load_generator/client/design.md#send_one
func sendOne(clientID, seq uint32, tSched int64, conn *h2client.Conn, cfg Config, runStart time.Time) RequestRecord {
	id := protocol.MakeRequestID(seq, clientID)
	body := protocol.BuildBody(id, cfg.RequestBytes)
	rec := RequestRecord{RequestID: id, ClientID: clientID, Seq: seq, ScheduledNs: tSched, Sent: true}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, protocol.PingMethod, "http://"+conn.Authority+protocol.PingPath, bytes.NewReader(body))
	req.Header.Set("content-type", protocol.ContentTypeJSON)
	req.Header.Set("content-length", strconv.Itoa(len(body)))
	req.ContentLength = int64(len(body))

	done := make(chan pingResult, 1)
	rec.SendNs = nowNs(runStart)
	go func() {
		resp, err := conn.CC.RoundTrip(req)
		if err != nil {
			done <- pingResult{err: err}
			return
		}
		b, err := io.ReadAll(resp.Body) // full response: up to END_STREAM
		resp.Body.Close()
		done <- pingResult{status: resp.StatusCode, body: b, err: err}
	}()

	// Design: abstraction/load_generator/client/design.md#slocancellation
	timer := time.NewTimer(time.Duration(rec.SendNs + cfg.SloNs - nowNs(runStart)))
	defer timer.Stop()
	select {
	case r := <-done:
		rec.EndNs = nowNs(runStart)
		if r.err != nil {
			rec.Outcome = OutcomeError
		} else {
			rec.HTTPStatus = r.status
			rec.Outcome = classify(r.status, r.body, id)
		}
	case <-timer.C:
		cancel() // x/net/http2 sends RST_STREAM(CANCEL) for the open stream
		rec.EndNs = nowNs(runStart)
		rec.Outcome = OutcomeCancelled
	}
	return rec
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
