// Source: abstraction/server/cancel_tracker/design.md (cancel_tracker v0.2.0)
package server

import (
	"container/heap"
	"errors"
	"sync/atomic"

	"testbed/internal/protocol"
	"testbed/internal/sampling"
)

// emptyKey marks an unused registry slot; stored keys are clientID + 1.
//
// Design: abstraction/server/cancel_tracker/design.md#cancelregistry
const emptyKey uint64 = 0

// ErrRegistryFull is returned by Push when a new client id finds no free slot.
var ErrRegistryFull = errors.New("cancel registry full")

// inboxNode is immutable once published by a successful CompareAndSwap.
//
// Design: abstraction/server/cancel_tracker/design.md#cancelinbox
type inboxNode struct {
	id   uint64
	next *inboxNode
}

// CancelRegistry is a fixed-capacity, lock-free, open-addressing map from
// client id to a CancelInbox (a Treiber stack head). Slots are never deleted.
// Go atomics are sequentially consistent, which subsumes the acquire/release
// orderings the design requires.
//
// Design: abstraction/server/cancel_tracker/design.md#cancelregistry
type CancelRegistry struct {
	mask     uint64
	keys     []atomic.Uint64
	heads    []atomic.Pointer[inboxNode]
	finished []atomic.Uint64 // FinishedWatermark per slot: id + 1, 0 = none
}

// NewCancelRegistry allocates capacity slots; capacity must be a power of two.
//
// Design: abstraction/server/cancel_tracker/design.md#cancelregistry
func NewCancelRegistry(capacity int) *CancelRegistry {
	if capacity <= 0 || capacity&(capacity-1) != 0 {
		panic("cancel registry capacity must be a power of two")
	}
	return &CancelRegistry{
		mask:     uint64(capacity - 1),
		keys:     make([]atomic.Uint64, capacity),
		heads:    make([]atomic.Pointer[inboxNode], capacity),
		finished: make([]atomic.Uint64, capacity),
	}
}

// findSlot probes linearly from Mix64(clientID). With insert it claims an
// empty slot by CAS; without insert, reaching an empty slot proves absence.
//
// Design: abstraction/server/cancel_tracker/design.md#cancelregistry
func (r *CancelRegistry) findSlot(clientID uint32, insert bool) (int, bool) {
	key := uint64(clientID) + 1
	i := sampling.Mix64(uint64(clientID)) & r.mask
	for n := 0; n < len(r.keys); n++ {
		k := r.keys[i].Load()
		if k == key {
			return int(i), true
		}
		if k == emptyKey {
			if !insert {
				return -1, false
			}
			if r.keys[i].CompareAndSwap(emptyKey, key) {
				return int(i), true
			}
			if r.keys[i].Load() == key {
				return int(i), true // another goroutine inserted the same key
			}
		}
		i = (i + 1) & r.mask
	}
	return -1, false
}

// Push adds id to clientID's inbox (multi-producer Treiber push).
//
// Design: abstraction/server/cancel_tracker/design.md#cancelinbox
func (r *CancelRegistry) Push(clientID uint32, id uint64) error {
	i, ok := r.findSlot(clientID, true)
	if !ok {
		return ErrRegistryFull
	}
	node := &inboxNode{id: id}
	for {
		h := r.heads[i].Load()
		node.next = h
		if r.heads[i].CompareAndSwap(h, node) {
			return nil
		}
	}
}

// Drain detaches the whole inbox of st's client with one atomic Swap. Only
// the worker bound to that client calls it. Detached nodes are reclaimed by
// the garbage collector, which stands in for the design's explicit free.
//
// Design: abstraction/server/cancel_tracker/design.md#cancelinbox
func (r *CancelRegistry) Drain(st *ConnectionCancelState) []uint64 {
	if st.slot < 0 {
		s, ok := r.findSlot(st.clientID, false)
		if !ok {
			return nil
		}
		st.slot = s
	}
	var ids []uint64
	for h := r.heads[st.slot].Swap(nil); h != nil; h = h.next {
		ids = append(ids, h.id)
	}
	return ids
}

// PublishFinished records id as st's client's FinishedWatermark. Only the
// worker bound to that client calls it; ids increase, so a plain Store suffices.
// If the registry is full the call does nothing (replies then say null).
//
// Design: abstraction/server/cancel_tracker/design.md#finishedwatermark
func (r *CancelRegistry) PublishFinished(st *ConnectionCancelState, id uint64) {
	if st.slot < 0 {
		s, ok := r.findSlot(st.clientID, true)
		if !ok {
			return
		}
		st.slot = s // Drain reuses the cached slot
	}
	r.finished[st.slot].Store(id + 1) // id <= 2^64 - 2^32 - 1: no wrap to 0
}

// Finished returns clientID's FinishedWatermark; ok is false for none.
//
// Design: abstraction/server/cancel_tracker/design.md#finishedwatermark
func (r *CancelRegistry) Finished(clientID uint32) (id uint64, ok bool) {
	i, found := r.findSlot(clientID, false)
	if !found {
		return 0, false
	}
	v := r.finished[i].Load()
	if v == 0 {
		return 0, false
	}
	return v - 1, true
}

// CancelCounters are the three process-wide /cancel outcome counters.
//
// Design: abstraction/server/cancel_tracker/design.md#cancelcounters
type CancelCounters struct {
	CancelledBeforeProcessing atomic.Uint64
	CancelledDuringProcessing atomic.Uint64
	CompletedBeforeCancel     atomic.Uint64
}

// Snapshot reads the counters in the order of the CancelCounters table.
//
// Design: abstraction/server/cancel_tracker/design.md#cancelcounters
func (c *CancelCounters) Snapshot() (before, during, completed uint64) {
	return c.CancelledBeforeProcessing.Load(), c.CancelledDuringProcessing.Load(), c.CompletedBeforeCancel.Load()
}

// ConnectionCancelState is owned by one connection's worker; no locking.
//
// API: abstraction/server/cancel_tracker/api.md#connectioncancelstate
type ConnectionCancelState struct {
	bound            bool
	clientID         uint32
	slot             int // -1 until found in the registry
	hasLastCompleted bool
	lastCompletedID  uint64
	pending          pendingSet
}

// NewConnectionCancelState returns an unbound state with an empty pending set.
func NewConnectionCancelState() *ConnectionCancelState {
	return &ConnectionCancelState{slot: -1, pending: pendingSet{members: map[uint64]struct{}{}}}
}

// pendingSet is the "local set": an ordered set (min-heap plus membership map).
//
// Design: abstraction/server/cancel_tracker/design.md#cancelmatching
type pendingSet struct {
	h       uint64Heap
	members map[uint64]struct{}
}

func (p *pendingSet) insert(id uint64) {
	if _, dup := p.members[id]; dup {
		return
	}
	p.members[id] = struct{}{}
	heap.Push(&p.h, id)
}

func (p *pendingSet) len() int    { return p.h.Len() }
func (p *pendingSet) min() uint64 { return p.h[0] }

func (p *pendingSet) popMin() uint64 {
	id := heap.Pop(&p.h).(uint64)
	delete(p.members, id)
	return id
}

type uint64Heap []uint64

func (h uint64Heap) Len() int           { return len(h) }
func (h uint64Heap) Less(i, j int) bool { return h[i] < h[j] }
func (h uint64Heap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *uint64Heap) Push(x any)        { *h = append(*h, x.(uint64)) }
func (h *uint64Heap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

// ShouldCancel runs CancelMatching for the request targetID at a CancelCheckPoint.
//
// Design: abstraction/server/cancel_tracker/design.md#cancelmatching
func ShouldCancel(st *ConnectionCancelState, registry *CancelRegistry, counters *CancelCounters, targetID uint64) bool {
	if !st.bound {
		return false
	}
	for _, id := range registry.Drain(st) {
		st.pending.insert(id)
	}
	for st.pending.len() > 0 && st.pending.min() <= targetID {
		id := st.pending.popMin()
		if st.hasLastCompleted && id <= st.lastCompletedID {
			counters.CompletedBeforeCancel.Add(1) // already done: nothing to cancel
		} else if id == targetID {
			return true // the caller counts before/during
		}
		// else stale: an earlier request that was stopped, rejected or never received
	}
	return false // ids > targetID stay pending
}

// HandleCancel serves a routed DELETE /cancel. A 202 carries the target
// client's FinishedWatermark in a CancelReplyBody; 400 and 503 are empty.
//
// Design: abstraction/server/cancel_tracker/design.md#handle_cancel
func HandleCancel(body []byte, cfg Config, registry *CancelRegistry) Response {
	id, err := protocol.ParseBody(body)
	if err != nil {
		return Response{Status: protocol.StatusBadRequest}
	}
	_, clientID := protocol.SplitRequestID(id)
	if cfg.CancelMode == CancelCooperative { // ignore: accepted, discarded
		if registry.Push(clientID, id) != nil {
			return Response{Status: protocol.StatusRejected}
		}
	}
	// Design: abstraction/server/cancel_tracker/design.md#finishedwatermark
	finished, ok := registry.Finished(clientID) // read after the push
	return Response{Status: protocol.StatusOK, Body: protocol.BuildCancelReply(finished, ok)}
}
