// Source: abstraction/server/ping_handler/design.md (ping_handler v0.4.0)
package server

import (
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

	// Design: abstraction/server/ping_handler/design.md#sampling-order
	serviceUs := rng.ExponentialUs(cfg.ResponseRate)
	return newPingJob(task, id, clientID, serviceUs), Response{}
}

// runPing runs one admitted /ping on a worker. Any worker may run any
// client's job; the client's state is reached through its registry slot.
//
// Design: abstraction/server/ping_handler/design.md#run_ping
// Design: abstraction/server/design.md#cancellation-semantics
func runPing(job *PingJob, cfg Config, registry *CancelRegistry, counters *CancelCounters) Response {
	id := job.RequestID
	slot := registry.Client(job.ClientID) // NoSlot if the registry is full
	// Design: abstraction/server/ping_handler/design.md#finishedwatermark
	finish := func(completed bool) { registry.PublishFinished(slot, id, completed) }

	if cfg.CancelMode == CancelCooperative {
		idCheck := func() bool { return ShouldCancel(registry, slot, counters, id) }

		// Design: abstraction/server/ping_handler/design.md#cancelcheckpoints
		if job.Task.Cancelled.Load() {
			finish(false)
			return Response{Aborted: true} // RST wins
		}
		if idCheck() {
			counters.CancelledBeforeProcessing.Add(1)
			finish(false)
			return Response{Status: protocol.StatusRejected, Body: protocol.BuildBody(id, cfg.ResponseBytes)}
		}

		switch Burn(job.ServiceUs, &job.Task.Cancelled, cfg.CancelCheckIntervalUs, idCheck, CheckIter) {
		case BurnCancelledRST:
			finish(false)
			return Response{Aborted: true}
		case BurnCancelledID:
			counters.CancelledDuringProcessing.Add(1)
			finish(false)
			return Response{Status: protocol.StatusRejected, Body: protocol.BuildBody(id, cfg.ResponseBytes)}
		}
	} else {
		Burn(job.ServiceUs, nil, cfg.CancelCheckIntervalUs, nil, CheckIter)
	}

	// Design: abstraction/server/ping_handler/design.md#last_completed_id
	finish(true)
	// Design: abstraction/server/ping_handler/design.md#response-status
	return Response{Status: protocol.StatusOK, Body: protocol.BuildBody(id, cfg.ResponseBytes)}
}
