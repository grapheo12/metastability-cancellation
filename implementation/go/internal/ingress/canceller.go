// Source: abstraction/ingress/load_canceller/design.md (load_canceller v0.3.0)
package ingress

import (
	"bufio"
	"bytes"
	"io"
	"math"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"testbed/internal/h2client"
	"testbed/internal/protocol"
	"testbed/internal/sampling"
)

// API: abstraction/ingress/load_canceller/api.md#constants
const CancellerDrainTimeoutMs = 1000

// CancelOutcome of one canceller arrival.
//
// Design: abstraction/ingress/load_canceller/design.md#outcome-classification
type CancelOutcome string

const (
	CancelAccepted   CancelOutcome = "accepted"
	CancelRejected   CancelOutcome = "rejected"
	CancelBadRequest CancelOutcome = "bad_request"
	CancelError      CancelOutcome = "error"
	CancelTimeout    CancelOutcome = "timeout"
	CancelSkipped    CancelOutcome = "skipped"
)

// Record states. PENDING -> COMPLETING -> DONE splits the design's
// PENDING -> DONE so the drain never reads a record still being written.
const (
	statePending uint32 = iota
	stateCompleting
	stateDone
	stateTimedOut
)

// CancelRecord is one row of the cancels CSV.
//
// API: abstraction/ingress/load_canceller/api.md#cancelrecord
type CancelRecord struct {
	Arrival     uint64
	CancelID    uint64
	ClientID    uint32
	ServerID    uint32
	TargetSeq   uint32
	Skipped     bool
	Outcome     CancelOutcome
	HTTPStatus  int // 0 means empty
	ScheduledNs int64
	SendNs      int64
	EndNs       int64
	HasEnd      bool
	state       atomic.Uint32
}

// Canceller is the running LoadCanceller.
type Canceller struct {
	cfg         Config
	inflight    *InFlightMap
	rng         *sampling.Rng
	runStart    time.Time
	conns       []*h2client.Conn // one per backend, nil if the dial failed
	outstanding atomic.Int64
	stop        chan struct{}
	stopOnce    sync.Once
	senderDone  chan struct{}
	records     []*CancelRecord // written by the sender only
}

func (c *Canceller) now() int64 { return int64(time.Since(c.runStart)) }

// StartCanceller starts the sender; the caller does not wait.
//
// Design: abstraction/ingress/load_canceller/design.md#startup
func StartCanceller(cfg Config, inflight *InFlightMap, runStart time.Time) *Canceller {
	c := &Canceller{
		cfg:        cfg,
		inflight:   inflight,
		rng:        sampling.NewRng(sampling.DeriveSeed(cfg.Seed, sampling.StreamCanceller, 0)),
		runStart:   runStart,
		conns:      make([]*h2client.Conn, len(cfg.Backends)),
		stop:       make(chan struct{}),
		senderDone: make(chan struct{}),
	}
	go func() {
		defer close(c.senderDone)
		for s, addr := range cfg.Backends { // one /cancel-only connection per backend
			if conn, err := h2client.Dial(addr); err == nil {
				c.conns[s] = conn
			}
		}
		c.runSchedule()
	}()
	return c
}

// runSchedule is the only sender: an open-loop Poisson clock. Draws per
// arrival: victim index (only when something is in flight), then next gap.
//
// Design: abstraction/ingress/load_canceller/design.md#cancelschedule
func (c *Canceller) runSchedule() {
	d := c.cfg.DurationNs
	var arrival uint64
	t := addGap(0, c.rng.ExponentialNs(c.cfg.CancelRate))
	timer := time.NewTimer(0)
	<-timer.C
	for t < d {
		if wait := t - c.now(); wait > 0 {
			timer.Reset(time.Duration(wait))
			select {
			case <-timer.C:
			case <-c.stop:
				return
			}
		}
		select {
		case <-c.stop:
			return
		default:
		}
		rec := &CancelRecord{Arrival: arrival, ScheduledNs: t}
		if v, ok := chooseVictim(c.rng, c.inflight); !ok {
			rec.Skipped, rec.Outcome = true, CancelSkipped
			rec.state.Store(stateDone)
		} else {
			rec.CancelID, rec.ClientID, rec.ServerID = v.Entry.RequestID, v.ClientID, v.Entry.ServerID
			rec.TargetSeq, _ = protocol.SplitRequestID(v.Entry.RequestID)
			rec.SendNs = c.now()
			if conn := c.conns[v.Entry.ServerID]; conn == nil { // backend unreachable
				rec.EndNs, rec.HasEnd, rec.Outcome = c.now(), true, CancelError
				rec.state.Store(stateDone)
			} else {
				c.outstanding.Add(1)
				c.sendAsync(conn, rec)
			}
		}
		c.records = append(c.records, rec)
		arrival++
		t = addGap(t, c.rng.ExponentialNs(c.cfg.CancelRate))
	}
}

// chooseVictim draws one entry uniformly from an InFlightMap snapshot.
//
// Design: abstraction/ingress/load_canceller/design.md#inflightvictimchoice
func chooseVictim(rng *sampling.Rng, inflight *InFlightMap) (Victim, bool) {
	snap := inflight.Snapshot()
	if len(snap) == 0 {
		return Victim{}, false // nothing in flight: skipped, no draw
	}
	return snap[rng.UniformInt(0, uint64(len(snap))-1)], true
}

// sendAsync opens the stream on its own goroutine (RoundTrip blocks until the
// response headers); the sender never waits.
//
// Design: abstraction/ingress/load_canceller/design.md#cancelschedule
func (c *Canceller) sendAsync(conn *h2client.Conn, rec *CancelRecord) {
	body := protocol.BuildCancelBody(rec.CancelID)
	req, _ := http.NewRequest(protocol.CancelMethod, "http://"+conn.Authority+protocol.CancelPath, bytes.NewReader(body))
	req.Header.Set("content-type", protocol.ContentTypeJSON)
	req.ContentLength = int64(len(body))
	go func() {
		resp, err := conn.CC.RoundTrip(req)
		status := 0
		if err == nil {
			_, err = io.Copy(io.Discard, resp.Body) // the CancelReplyBody is not used
			resp.Body.Close()
			status = resp.StatusCode
		}
		c.complete(rec, status, err)
	}()
}

// complete records a response unless the drain already timed the record out.
//
// Design: abstraction/ingress/load_canceller/design.md#response-collection
func (c *Canceller) complete(rec *CancelRecord, status int, err error) {
	if !rec.state.CompareAndSwap(statePending, stateCompleting) {
		return
	}
	rec.EndNs, rec.HasEnd = c.now(), true
	rec.HTTPStatus = status
	if err != nil {
		rec.Outcome = CancelError
	} else {
		rec.Outcome = classifyCancel(status)
	}
	rec.state.Store(stateDone)
	c.outstanding.Add(-1)
}

// classifyCancel maps a /cancel status to an outcome.
//
// Design: abstraction/ingress/load_canceller/design.md#outcome-classification
// Design: abstraction/protocol/design.md#status-classification-load-canceller
func classifyCancel(status int) CancelOutcome {
	switch status {
	case protocol.StatusOK:
		return CancelAccepted
	case protocol.StatusRejected:
		return CancelRejected
	case protocol.StatusBadRequest:
		return CancelBadRequest
	}
	return CancelError
}

// Stop ends the sender, drains outstanding responses for up to
// CANCELLER_DRAIN_TIMEOUT_MS, times out the rest, closes the connections and
// returns the records in arrival order.
//
// Design: abstraction/ingress/load_canceller/design.md#cancellerdrain
func (c *Canceller) Stop() []*CancelRecord {
	c.stopOnce.Do(func() { close(c.stop) })
	<-c.senderDone
	deadline := c.now() + CancellerDrainTimeoutMs*1_000_000
	for c.outstanding.Load() > 0 && c.now() < deadline {
		time.Sleep(time.Millisecond)
	}
	for _, rec := range c.records {
		if rec.state.CompareAndSwap(statePending, stateTimedOut) {
			rec.Outcome = CancelTimeout
			continue
		}
		for rec.state.Load() == stateCompleting {
			runtime.Gosched()
		}
	}
	for _, conn := range c.conns {
		if conn != nil {
			conn.Close()
		}
	}
	return c.records
}

type cancelCounts struct {
	Scheduled  int `json:"scheduled"`
	Sent       int `json:"sent"`
	Accepted   int `json:"accepted"`
	Rejected   int `json:"rejected"`
	BadRequest int `json:"bad_request"`
	Error      int `json:"error"`
	Timeout    int `json:"timeout"`
	Skipped    int `json:"skipped"`
}

type cancelRates struct {
	Offered  float64 `json:"offered"`
	Sent     float64 `json:"sent"`
	Accepted float64 `json:"accepted"`
}

// summarizeCancels computes the canceller part of the Ingress summary.
// With the canceller disabled (cancel_rate 0) everything is zero.
//
// Design: abstraction/ingress/load_canceller/design.md#summary
func summarizeCancels(records []*CancelRecord, cfg Config) (cancelCounts, cancelRates, int) {
	var n cancelCounts
	targeted := map[uint32]struct{}{}
	for _, r := range records {
		switch r.Outcome {
		case CancelAccepted:
			n.Accepted++
		case CancelRejected:
			n.Rejected++
		case CancelBadRequest:
			n.BadRequest++
		case CancelError:
			n.Error++
		case CancelTimeout:
			n.Timeout++
		case CancelSkipped:
			n.Skipped++
		}
		if !r.Skipped {
			targeted[r.ClientID] = struct{}{}
		}
	}
	n.Scheduled = len(records)
	n.Sent = n.Scheduled - n.Skipped
	var rates cancelRates
	if cfg.DurationS > 0 {
		rates = cancelRates{float64(n.Scheduled) / cfg.DurationS, float64(n.Sent) / cfg.DurationS,
			float64(n.Accepted) / cfg.DurationS}
	}
	return n, rates, len(targeted)
}

// writeCancelsCSV writes records in arrival order (header only if none).
//
// API: abstraction/ingress/load_canceller/api.md#output_pathcancelscsv
func writeCancelsCSV(path string, records []*CancelRecord) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	w.WriteString("arrival,cancel_id,client_id,server_id,target_seq,outcome,http_status,scheduled_ns,send_ns,end_ns\n")
	for _, r := range records {
		cancelID, client, server, seq, status, send, end := "", "", "", "", "", "", ""
		if !r.Skipped {
			cancelID = strconv.FormatUint(r.CancelID, 10)
			client = strconv.FormatUint(uint64(r.ClientID), 10)
			server = strconv.FormatUint(uint64(r.ServerID), 10)
			seq = strconv.FormatUint(uint64(r.TargetSeq), 10)
			send = strconv.FormatInt(r.SendNs, 10)
		}
		if r.HTTPStatus != 0 {
			status = strconv.Itoa(r.HTTPStatus)
		}
		if r.HasEnd {
			end = strconv.FormatInt(r.EndNs, 10)
		}
		w.WriteString(strconv.FormatUint(r.Arrival, 10) + "," + cancelID + "," + client + "," + server + "," + seq + "," +
			string(r.Outcome) + "," + status + "," + strconv.FormatInt(r.ScheduledNs, 10) + "," + send + "," + end + "\n")
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func addGap(t int64, gap uint64) int64 {
	if gap >= uint64(math.MaxInt64-t) {
		return math.MaxInt64
	}
	return t + int64(gap)
}
