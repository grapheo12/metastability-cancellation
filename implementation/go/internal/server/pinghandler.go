// Source: abstraction/server/ping_handler/design.md (ping_handler v0.10.0)
package server

import (
	"sync/atomic"

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
	}
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
		if idCheck() {
			counters.CancelledBeforeProcessing.Add(1)
			finish(false)
			return Response{Status: protocol.StatusRejected, Body: protocol.BuildBody(id, 0, nil, cfg.ResponseBytes)}
		}

		var outcome BurnOutcome
		switch cfg.Workload {
		case WorkloadCPUBurn:
			outcome = Burn(job.ServiceUs, &job.Task.Cancelled, cfg.CancelCheckIntervalUs, idCheck, CheckIter)
		case WorkloadSleep:
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
			return Response{Status: protocol.StatusRejected, Body: protocol.BuildBody(id, 0, nil, cfg.ResponseBytes)}
		}
	} else {
		// Design: abstraction/server/ping_handler/design.md#cancelcheckpoints
		var freezeCheck func() bool // ignore mode: only a WorkerFreeze wait, never cancels
		if cfg.FreezeAfterMs > 0 {
			freezeCheck = func() bool { q.FreezeWait(); return false }
		}
		switch cfg.Workload {
		case WorkloadCPUBurn:
			Burn(job.ServiceUs, nil, cfg.CancelCheckIntervalUs, freezeCheck, CheckIter)
		case WorkloadSleep:
			Sleep(job.ServiceUs, nil, freezeCheck) // always BurnCompleted
		default:
			fillMessages(job, cfg, buf, nil, freezeCheck) // always BurnCompleted
		}
	}

	finish(true) // completed: a later /cancel counts completed_before_cancel
	// Design: abstraction/server/ping_handler/design.md#response-status
	return Response{Status: protocol.StatusOK, Body: protocol.BuildBody(id, 0, nil, cfg.ResponseBytes)}
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
