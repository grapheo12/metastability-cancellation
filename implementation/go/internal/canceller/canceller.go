// Package canceller implements the LoadCanceller: a single-sender, open-loop
// Poisson source of DELETE /cancel requests.
//
// Source: abstraction/load_canceller/design.md (load_canceller v0.2.0)
package canceller

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
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

// API: abstraction/load_canceller/api.md#constants
const CancellerDrainTimeoutMs = 1000

// Config is the validated CancellerConfig.
//
// API: abstraction/load_canceller/api.md#cancellerconfig
type Config struct {
	Target      string
	NumClients  uint32
	CancelRate  float64
	IncrRandMax uint64
	DurationS   float64
	Seed        uint64
	OutputPath  string
	DurationNs  int64
}

// Params holds the raw command-line values, named after the API parameters.
type Params struct {
	Target      string
	NumClients  string
	CancelRate  string
	IncrRandMax string
	DurationS   string
	Seed        string
	OutputPath  string
}

// DefaultParams returns the defaults from abstraction/load_canceller/api.md#program-parameters.
func DefaultParams() Params {
	return Params{Target: "127.0.0.1:8080", IncrRandMax: "4", Seed: "0", OutputPath: "cancels"}
}

// Validate turns Params into a Config or returns a one-line error.
//
// Design: abstraction/load_canceller/design.md#configuration-validation
func Validate(p Params) (Config, error) {
	var c Config
	n, err := strconv.ParseUint(p.NumClients, 10, 32)
	if p.NumClients == "" || err != nil || n < 1 {
		return c, errors.New("num_clients must be an integer in 1 ..= 2^32-1")
	}
	c.NumClients = uint32(n)
	if c.CancelRate, err = parsePositive(p.CancelRate); err != nil {
		return c, errors.New("cancel_rate " + err.Error())
	}
	m, err := strconv.ParseUint(p.IncrRandMax, 10, 32)
	if err != nil || m < 1 {
		return c, errors.New("incr_rand_max must be an integer in 1 ..= 2^32-1")
	}
	c.IncrRandMax = m
	if c.DurationS, err = parsePositive(p.DurationS); err != nil {
		return c, errors.New("duration_s " + err.Error())
	}
	if c.Seed, err = strconv.ParseUint(p.Seed, 10, 64); err != nil {
		return c, errors.New("seed must be an unsigned 64-bit integer")
	}
	if _, port, err := net.SplitHostPort(p.Target); err != nil || port == "" {
		return c, errors.New("target must be host:port")
	}
	c.Target = p.Target
	c.OutputPath = p.OutputPath
	c.DurationNs = int64(math.Round(c.DurationS * 1e9))
	return c, nil
}

func parsePositive(s string) (float64, error) {
	if s == "" {
		return 0, errors.New("is required")
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || !(v > 0) || math.IsInf(v, 0) {
		return 0, errors.New("must be a finite number > 0")
	}
	return v, nil
}

// Outcome of one cancel arrival.
//
// Design: abstraction/load_canceller/design.md#outcome-classification
type Outcome string

const (
	OutcomeAccepted   Outcome = "accepted"
	OutcomeRejected   Outcome = "rejected"
	OutcomeBadRequest Outcome = "bad_request"
	OutcomeError      Outcome = "error"
	OutcomeTimeout    Outcome = "timeout"
	OutcomeSkipped    Outcome = "skipped"
)

// Record states. The design's PENDING -> DONE CAS is split into
// PENDING -> COMPLETING -> DONE so that drain never reads a record whose
// fields are still being written by a response goroutine.
const (
	statePending uint32 = iota
	stateCompleting
	stateDone
	stateTimedOut
)

// CancelRecord is one row of the cancels CSV.
//
// API: abstraction/load_canceller/api.md#cancelrecord
type CancelRecord struct {
	Arrival     uint64
	CancelID    uint64
	ClientID    uint32
	TargetSeq   uint32
	Skipped     bool
	Outcome     Outcome
	HTTPStatus  int // 0 means empty
	ScheduledNs int64
	SendNs      int64
	EndNs       int64
	HasEnd      bool
	// ReportedFinished is the FinishedWatermark (a RequestId) from a 202 reply.
	ReportedFinished uint64
	HasReported      bool
	state            atomic.Uint32
}

// finishedTable holds the largest reported finished seq per client. The
// response goroutines write it and the sender reads it, under one mutex.
//
// Design: abstraction/load_canceller/design.md#canceltargetchoice
type finishedTable struct {
	mu  sync.Mutex
	seq map[uint32]uint32
}

func (f *finishedTable) get(clientID uint32) (uint32, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.seq[clientID]
	return v, ok
}

// note is note_finished: keep the max, ignore replies about another client.
//
// Design: abstraction/load_canceller/design.md#canceltargetchoice
func (f *finishedTable) note(clientID uint32, finished uint64) {
	fseq, fclient := protocol.SplitRequestID(finished)
	if fclient != clientID {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if old, ok := f.seq[clientID]; !ok || fseq > old {
		f.seq[clientID] = fseq
	}
}

type sender struct {
	cfg         Config
	conn        *h2client.Conn
	rng         *sampling.Rng
	runStart    time.Time
	outstanding atomic.Int64
	finished    finishedTable
	records     []*CancelRecord
}

func (s *sender) now() int64 { return int64(time.Since(s.runStart)) }

// Run is the program body after validation.
//
// Design: abstraction/load_canceller/design.md#startup
func Run(cfg Config) error {
	conn, err := h2client.Dial(cfg.Target)
	if err != nil {
		return err
	}
	s := &sender{
		cfg:  cfg,
		conn: conn,
		rng:  sampling.NewRng(sampling.DeriveSeed(cfg.Seed, sampling.StreamCanceller, 0)),
	}
	s.finished.seq = map[uint32]uint32{}
	s.runStart = time.Now()
	s.runSchedule()
	s.drain()
	runEnd := s.now()
	if err := writeCancelsCSV(cfg.OutputPath+".cancels.csv", s.records); err != nil {
		return err
	}
	return writeSummaryJSON(cfg.OutputPath+".summary.json", summarize(s.records, cfg, runEnd))
}

// runSchedule is the only sender. Per arrival the draws are: client, step,
// next gap (the first gap is drawn before the loop).
//
// Design: abstraction/load_canceller/design.md#cancelschedule
func (s *sender) runSchedule() {
	d := s.cfg.DurationNs
	var arrival uint64
	t := addGap(0, s.rng.ExponentialNs(s.cfg.CancelRate))
	for t < d {
		if wait := t - s.now(); wait > 0 {
			time.Sleep(time.Duration(wait))
		}
		clientID, seq, ok := s.chooseTarget()
		rec := &CancelRecord{Arrival: arrival, ClientID: clientID, ScheduledNs: t}
		if !ok {
			rec.Skipped, rec.Outcome = true, OutcomeSkipped
			rec.state.Store(stateDone)
		} else {
			rec.CancelID = protocol.MakeRequestID(seq, clientID)
			rec.TargetSeq = seq
			s.outstanding.Add(1)
			rec.SendNs = s.now()
			s.sendAsync(rec)
		}
		s.records = append(s.records, rec)
		arrival++
		t = addGap(t, s.rng.ExponentialNs(s.cfg.CancelRate))
	}
}

// sendAsync opens the stream on its own goroutine, because RoundTrip blocks
// until the response headers arrive. The sender itself never waits.
//
// Design: abstraction/load_canceller/design.md#cancelschedule
func (s *sender) sendAsync(rec *CancelRecord) {
	body := protocol.BuildCancelBody(rec.CancelID)
	req, _ := http.NewRequest(protocol.CancelMethod, "http://"+s.conn.Authority+protocol.CancelPath, bytes.NewReader(body))
	req.Header.Set("content-type", protocol.ContentTypeJSON)
	req.ContentLength = int64(len(body))
	go func() {
		resp, err := s.conn.CC.RoundTrip(req)
		status := 0
		var respBody []byte
		if err == nil {
			respBody, err = io.ReadAll(resp.Body) // a 202 carries the CancelReplyBody
			resp.Body.Close()
			status = resp.StatusCode
		}
		s.complete(rec, status, respBody, err)
	}()
}

// chooseTarget draws a uniform client, then a uniform step in [1, incr_rand_max],
// and aims the step past the client's last reported FinishedWatermark.
//
// Design: abstraction/load_canceller/design.md#canceltargetchoice
func (s *sender) chooseTarget() (clientID, seq uint32, ok bool) {
	clientID = uint32(s.rng.UniformInt(0, uint64(s.cfg.NumClients)-1))
	step := s.rng.UniformInt(1, s.cfg.IncrRandMax)
	base, hasBase := s.finished.get(clientID)
	seq, ok = nextTarget(base, hasBase, step)
	return clientID, seq, ok
}

// nextTarget is base + step, or step - 1 with no reply yet. A target past
// 2^32 - 1 is "skipped". u64 arithmetic: base, step <= 2^32 - 1, no wrap.
//
// Design: abstraction/load_canceller/design.md#canceltargetchoice
func nextTarget(base uint32, hasBase bool, step uint64) (uint32, bool) {
	t := step - 1
	if hasBase {
		t = uint64(base) + step
	}
	if t > math.MaxUint32 {
		return 0, false
	}
	return uint32(t), true
}

// complete records a response unless drain already timed the record out.
//
// Design: abstraction/load_canceller/design.md#response-collection
func (s *sender) complete(rec *CancelRecord, status int, body []byte, err error) {
	if !rec.state.CompareAndSwap(statePending, stateCompleting) {
		return
	}
	rec.EndNs, rec.HasEnd = s.now(), true
	if err != nil {
		rec.Outcome = OutcomeError
		rec.HTTPStatus = status
	} else {
		rec.HTTPStatus = status
		rec.Outcome = classify(status)
		if status == protocol.StatusOK {
			if f, ok, perr := protocol.ParseCancelReply(body); perr == nil && ok {
				rec.ReportedFinished, rec.HasReported = f, true
				s.finished.note(rec.ClientID, f) // CancelTargetChoice
			}
		}
	}
	rec.state.Store(stateDone)
	s.outstanding.Add(-1)
}

// classify maps a /cancel status to an Outcome.
//
// Design: abstraction/load_canceller/design.md#outcome-classification
// Design: abstraction/protocol/design.md#status-classification-load-canceller
func classify(status int) Outcome {
	switch status {
	case protocol.StatusOK:
		return OutcomeAccepted
	case protocol.StatusRejected:
		return OutcomeRejected
	case protocol.StatusBadRequest:
		return OutcomeBadRequest
	}
	return OutcomeError
}

// drain waits up to CANCELLER_DRAIN_TIMEOUT_MS for outstanding responses,
// times out the rest, then closes the connection.
//
// Design: abstraction/load_canceller/design.md#cancellerdrain
func (s *sender) drain() {
	deadline := s.now() + CancellerDrainTimeoutMs*1_000_000
	for s.outstanding.Load() > 0 && s.now() < deadline {
		time.Sleep(time.Millisecond)
	}
	for _, rec := range s.records {
		if rec.state.CompareAndSwap(statePending, stateTimedOut) {
			rec.Outcome = OutcomeTimeout
			continue
		}
		for rec.state.Load() == stateCompleting {
			runtime.Gosched()
		}
	}
	s.conn.Close()
}

type summaryConfig struct {
	Target      string  `json:"target"`
	NumClients  uint32  `json:"num_clients"`
	CancelRate  float64 `json:"cancel_rate"`
	IncrRandMax uint64  `json:"incr_rand_max"`
	DurationS   float64 `json:"duration_s"`
	Seed        uint64  `json:"seed"`
	OutputPath  string  `json:"output_path"`
}

type summaryCounts struct {
	Scheduled  int `json:"scheduled"`
	Sent       int `json:"sent"`
	Accepted   int `json:"accepted"`
	Rejected   int `json:"rejected"`
	BadRequest int `json:"bad_request"`
	Error      int `json:"error"`
	Timeout    int `json:"timeout"`
	Skipped    int `json:"skipped"`
}

type summaryRates struct {
	Offered  float64 `json:"offered"`
	Sent     float64 `json:"sent"`
	Accepted float64 `json:"accepted"`
}

// Summary has the field order of the canceller summary JSON.
//
// API: abstraction/load_canceller/api.md#output_pathsummaryjson
type Summary struct {
	Config          summaryConfig `json:"config"`
	RunEndS         float64       `json:"run_end_s"`
	Counts          summaryCounts `json:"counts"`
	RatesPerS       summaryRates  `json:"rates_per_s"`
	ClientsTargeted int           `json:"clients_targeted"`
}

// Design: abstraction/load_canceller/design.md#summary
func summarize(records []*CancelRecord, cfg Config, runEndNs int64) Summary {
	var s Summary
	s.Config = summaryConfig{cfg.Target, cfg.NumClients, cfg.CancelRate, cfg.IncrRandMax, cfg.DurationS, cfg.Seed, cfg.OutputPath}
	s.RunEndS = float64(runEndNs) / 1e9
	targeted := map[uint32]struct{}{}
	for _, r := range records {
		switch r.Outcome {
		case OutcomeAccepted:
			s.Counts.Accepted++
		case OutcomeRejected:
			s.Counts.Rejected++
		case OutcomeBadRequest:
			s.Counts.BadRequest++
		case OutcomeError:
			s.Counts.Error++
		case OutcomeTimeout:
			s.Counts.Timeout++
		case OutcomeSkipped:
			s.Counts.Skipped++
		}
		if !r.Skipped {
			targeted[r.ClientID] = struct{}{}
		}
	}
	s.Counts.Scheduled = len(records)
	s.Counts.Sent = s.Counts.Scheduled - s.Counts.Skipped
	s.RatesPerS = summaryRates{
		Offered:  float64(s.Counts.Scheduled) / cfg.DurationS,
		Sent:     float64(s.Counts.Sent) / cfg.DurationS,
		Accepted: float64(s.Counts.Accepted) / cfg.DurationS,
	}
	s.ClientsTargeted = len(targeted)
	return s
}

// writeCancelsCSV writes records in arrival order.
//
// API: abstraction/load_canceller/api.md#output_pathcancelscsv
func writeCancelsCSV(path string, records []*CancelRecord) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	w.WriteString("arrival,cancel_id,client_id,target_seq,outcome,http_status,scheduled_ns,send_ns,end_ns,reported_finished\n")
	for _, r := range records {
		cancelID, seq, status, send, end, reported := "", "", "", "", "", ""
		if !r.Skipped {
			cancelID, seq = strconv.FormatUint(r.CancelID, 10), strconv.FormatUint(uint64(r.TargetSeq), 10)
			send = strconv.FormatInt(r.SendNs, 10)
		}
		if r.HTTPStatus != 0 {
			status = strconv.Itoa(r.HTTPStatus)
		}
		if r.HasEnd {
			end = strconv.FormatInt(r.EndNs, 10)
		}
		if r.HasReported {
			reported = strconv.FormatUint(r.ReportedFinished, 10)
		}
		w.WriteString(strconv.FormatUint(r.Arrival, 10) + "," + cancelID + "," + strconv.FormatUint(uint64(r.ClientID), 10) + "," +
			seq + "," + string(r.Outcome) + "," + status + "," + strconv.FormatInt(r.ScheduledNs, 10) + "," + send + "," + end + "," +
			reported + "\n")
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func writeSummaryJSON(path string, s Summary) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func addGap(t int64, gap uint64) int64 {
	if gap >= uint64(math.MaxInt64-t) {
		return math.MaxInt64
	}
	return t + int64(gap)
}
