// Source: abstraction/server/ping_handler/design.md (ping_handler v0.2.1)
package server

import (
	"testbed/internal/protocol"
	"testbed/internal/sampling"
)

// cancelContext bundles the connection's cancel state with the process-wide
// registry and counters.
//
// Design: abstraction/server/connection_handler/design.md#shared-state
type cancelContext struct {
	state    *ConnectionCancelState
	registry *CancelRegistry
	counters *CancelCounters
}

// handlePing serves one routed POST /ping on the connection's single worker.
//
// Design: abstraction/server/ping_handler/design.md#handle_ping
// Design: abstraction/server/design.md#cancellation-semantics
func handlePing(task *StreamTask, cfg Config, rng *sampling.Rng, cc *cancelContext) Response {
	st := cc.state
	id, err := protocol.ParseBody(task.Body)
	if err != nil {
		return Response{Status: protocol.StatusBadRequest}
	}

	_, clientID := protocol.SplitRequestID(id)
	// Design: abstraction/server/ping_handler/design.md#client-binding
	if !st.bound {
		st.bound = true
		st.clientID = clientID
	} else if st.clientID != clientID {
		return Response{Status: protocol.StatusBadRequest}
	}

	// Design: abstraction/server/ping_handler/design.md#sampling-order
	serviceUs := rng.ExponentialUs(cfg.ResponseRate)

	if cfg.CancelMode == CancelCooperative {
		idCheck := func() bool { return ShouldCancel(st, cc.registry, cc.counters, id) }

		// Design: abstraction/server/ping_handler/design.md#cancelcheckpoints
		if task.Cancelled.Load() {
			return Response{Aborted: true} // RST wins
		}
		if idCheck() {
			cc.counters.CancelledBeforeProcessing.Add(1)
			return Response{Status: protocol.StatusRejected, Body: protocol.BuildBody(id, cfg.ResponseBytes)}
		}

		switch Burn(serviceUs, &task.Cancelled, cfg.CancelCheckIntervalUs, idCheck, CheckIter) {
		case BurnCancelledRST:
			return Response{Aborted: true}
		case BurnCancelledID:
			cc.counters.CancelledDuringProcessing.Add(1)
			return Response{Status: protocol.StatusRejected, Body: protocol.BuildBody(id, cfg.ResponseBytes)}
		}
	} else {
		Burn(serviceUs, nil, cfg.CancelCheckIntervalUs, nil, CheckIter)
	}

	// Design: abstraction/server/ping_handler/design.md#last_completed_id
	st.hasLastCompleted = true
	st.lastCompletedID = id
	// Design: abstraction/server/ping_handler/design.md#response-status
	return Response{Status: protocol.StatusOK, Body: protocol.BuildBody(id, cfg.ResponseBytes)}
}
