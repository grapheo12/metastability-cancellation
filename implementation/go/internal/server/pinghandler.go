// Source: abstraction/server/ping_handler/design.md (ping_handler v0.14.0)
package server

import (
	"math"
	"sync/atomic"
	"time"

	"testbed/internal/protocol"
	"testbed/internal/sampling"
)

// admitPing runs on the connection's handler, under its admission lock: parse,
// bind, draw the ServiceTime. It returns a PingJob, or nil and a 400.
//
// Design: abstraction/server/ping_handler/design.md#handle_ping
// Design: abstraction/server/ping_handler/design.md#admit_ping
func admitPing(task *StreamTask, cfg Config, rng *sampling.Rng, st *ConnectionCancelState) (*PingJob, Response) {
	id, err := protocol.ParseBody(task.Body)
	if err != nil {
		return nil, Response{Status: protocol.StatusBadRequest}
	}

	_, clientID := protocol.SplitRequestID(id)
	// Design: abstraction/server/ping_handler/design.md#client-binding
	if !st.bound {
		st.bound = true
		st.clientID = clientID
	} else if st.clientID != clientID {
		return nil, Response{Status: protocol.StatusBadRequest}
	}

	var serviceUs uint64
	// Design: abstraction/server/ping_handler/design.md#sampling-order
	if cfg.Workload == WorkloadCPUBurn || cfg.Workload == WorkloadSleep {
		serviceUs = rng.ExponentialUs(cfg.ResponseRate)
	} else if cfg.Workload == WorkloadSleepConstant && cfg.Scenario == nil {
		// Design: abstraction/server/ping_handler/design.md#constantservicetime
		serviceUs = constantServiceUs(cfg.ResponseRate) // no draw: the rng is not advanced
	}
	// Design: abstraction/server/ping_handler/design.md#scenarioservicetime
	// With a ServiceScenario serviceUs stays 0 here: WorkQueue.Pop sets it from the state at the pop.
	// RequestDeadline for the edf QueueDiscipline; missing or malformed is NoDeadline, never a 400.
	deadline, ok := protocol.ParseDeadline(task.Body)
	if !ok {
		deadline = NoDeadline
	}
	return newPingJob(task, id, clientID, serviceUs, deadline), Response{}
}

// runPing runs one admitted /ping on a worker. Any worker may run any
// client's job; the client's state is reached through its registry slot.
//
// Design: abstraction/server/ping_handler/design.md#run_ping
// Design: abstraction/server/design.md#cancellation-semantics
func runPing(job *PingJob, cfg Config, registry *CancelRegistry, counters *CancelCounters, buf *MessageBuffer, q *WorkQueue) Response {
	id := job.RequestID
	report := startExecReport(job) // ExecReport: stamped first, right after the pop
	body := func() []byte { return protocol.BuildBody(id, report.execNs(), report.startSlackNs, cfg.ResponseBytes) }
	slot := registry.Client(job.ClientID) // NoSlot if the registry is full
	registry.Start(slot, id)              // CancelMatching: started, not finished
	// Design: abstraction/server/ping_handler/design.md#finishedwatermark
	finish := func(completed bool) { registry.PublishFinished(slot, id, completed, counters) }

	if cfg.CancelMode == CancelCooperative {
		// WorkerFreeze first at every check: returns at once unless frozen.
		idCheck := func() bool { q.FreezeWait(); return ShouldCancel(registry, slot, counters, id) }

		// Design: abstraction/server/ping_handler/design.md#cancelcheckpoints
		q.FreezeWait()
		if job.Task.Cancelled.Load() {
			finish(false)
			return Response{Aborted: true} // RST wins
		}
		if pastDeadline(cfg, report) { // PastDeadlineRejection: after RST, before the /cancel match
			counters.RejectedPastDeadline.Add(1)
			finish(false)
			return Response{Status: protocol.StatusRejected, Body: body()}
		}
		if idCheck() {
			counters.CancelledBeforeProcessing.Add(1)
			finish(false)
			return Response{Status: protocol.StatusRejected, Body: body()}
		}

		var outcome BurnOutcome
		switch cfg.Workload {
		case WorkloadCPUBurn:
			outcome = Burn(job.ServiceUs, &job.Task.Cancelled, cfg.CancelCheckIntervalUs, idCheck, CheckIter)
		case WorkloadSleep, WorkloadSleepConstant:
			outcome = Sleep(job.ServiceUs, &job.Task.Cancelled, idCheck) // QuantumSleep
		default:
			outcome = fillMessages(job, cfg, buf, &job.Task.Cancelled, idCheck)
		}
		switch outcome {
		case BurnCancelledRST:
			finish(false)
			return Response{Aborted: true}
		case BurnCancelledID:
			counters.CancelledDuringProcessing.Add(1)
			finish(false)
			return Response{Status: protocol.StatusRejected, Body: body()}
		}
	} else {
		// Design: abstraction/server/ping_handler/design.md#pastdeadlinerejection
		if pastDeadline(cfg, report) { // the only check in ignore mode, first after the pop
			counters.RejectedPastDeadline.Add(1)
			finish(false)
			return Response{Status: protocol.StatusRejected, Body: body()}
		}
		// Design: abstraction/server/ping_handler/design.md#cancelcheckpoints
		var freezeCheck func() bool // ignore mode: only a WorkerFreeze or StalledState wait, never cancels
		if cfg.FreezeAfterMs > 0 || cfg.Scenario != nil {
			freezeCheck = func() bool { q.FreezeWait(); return false }
		}
		switch cfg.Workload {
		case WorkloadCPUBurn:
			Burn(job.ServiceUs, nil, cfg.CancelCheckIntervalUs, freezeCheck, CheckIter)
		case WorkloadSleep, WorkloadSleepConstant:
			Sleep(job.ServiceUs, nil, freezeCheck) // always BurnCompleted
		default:
			fillMessages(job, cfg, buf, nil, freezeCheck) // always BurnCompleted
		}
	}

	finish(true) // completed: a later /cancel counts completed_before_cancel
	// Design: abstraction/server/ping_handler/design.md#response-status
	return Response{Status: protocol.StatusOK, Body: body()}
}

// pastDeadline is past_deadline: reject_past_deadline is on and the request was
// popped after its RequestDeadline (StartSlack < 0; none never qualifies).
//
// Design: abstraction/server/ping_handler/design.md#pastdeadlinerejection
func pastDeadline(cfg Config, r execReport) bool {
	return cfg.RejectPastDeadline && r.startSlackNs != nil && *r.startSlackNs < 0
}

// execReport holds what run_ping stamps when it starts: the monotonic start
// (time.Time carries Go's monotonic reading) and the StartSlack, nil for none.
//
// Design: abstraction/server/ping_handler/design.md#execreport
type execReport struct {
	t0           time.Time
	startSlackNs *int64
}

// startExecReport reads the monotonic and wall clocks once and computes the
// StartSlack = deadline_unix_ns - wall, exact and saturated to i64.
//
// Design: abstraction/server/ping_handler/design.md#execreport
func startExecReport(job *PingJob) execReport {
	t0 := time.Now()
	r := execReport{t0: t0}
	if job.DeadlineUnixNs != NoDeadline {
		v := startSlack(job.DeadlineUnixNs, t0.UnixNano())
		r.startSlackNs = &v
	}
	return r
}

// startSlack returns deadline - wall as exact signed arithmetic, saturated to
// [math.MinInt64, math.MaxInt64]. wall is a Unix time in ns (>= 0 until 2262).
//
// Design: abstraction/server/ping_handler/design.md#execreport
func startSlack(deadline uint64, wall int64) int64 {
	if wall < 0 { // before 1970: never on a real clock; keep the arithmetic exact anyway
		wall = 0
	}
	if deadline > math.MaxInt64 {
		d := deadline - uint64(wall) // > 0 and fits in a u64
		if d > math.MaxInt64 {
			return math.MaxInt64
		}
		return int64(d)
	}
	return int64(deadline) - wall // both in [0, MaxInt64]: no overflow
}

// execNs is the ExecutionTime: monotonic time since run_ping started, read when
// the body is built.
//
// Design: abstraction/server/ping_handler/design.md#execreport
func (r execReport) execNs() uint64 {
	d := time.Since(r.t0)
	if d < 0 {
		return 0
	}
	return uint64(d)
}

// fillMessages is the MessageWork: create every message (checking RST, then
// /cancel, after each), then write them all with one Push. A cancelled
// request never touches the buffer.
//
// Design: abstraction/server/ping_handler/design.md#messagework
func fillMessages(job *PingJob, cfg Config, buf *MessageBuffer, cancelled *atomic.Bool, idCheck func() bool) BurnOutcome {
	seq, clientID := protocol.SplitRequestID(job.RequestID)
	rng := sampling.NewRng(sampling.DeriveSeed( // the request's own MessageTree stream
		sampling.DeriveSeed(cfg.Seed, sampling.StreamServerMessages, clientID), sampling.StreamServerMessages, seq))
	msgs := make([]*TreeNode, 0, cfg.NMessagesPerRequest)
	for k := 0; k < cfg.NMessagesPerRequest; k++ {
		msgs = append(msgs, makeTree(rng, cfg.NGCBinaryTreeSize)) // MessageTree k, in order
		if cancelled != nil && cancelled.Load() {
			return BurnCancelledRST
		}
		if idCheck != nil && idCheck() {
			return BurnCancelledID
		}
	}
	buf.Push(job.ClientID, msgs) // created before the lock
	return BurnCompleted
}

// constantServiceUs is constant_service_us: round(1e6 / response_rate) µs,
// half up (binary64 division; rate > 0 is validated).
//
// Design: abstraction/server/ping_handler/design.md#constantservicetime
func constantServiceUs(rate float64) uint64 {
	return uint64(math.Floor(1_000_000/rate + 0.5))
}
