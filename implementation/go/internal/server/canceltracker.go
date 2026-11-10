// Source: abstraction/server/cancel_tracker/design.md (cancel_tracker v0.4.0)
package server

import (
	"errors"
	"sync"
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
// client id to a slot holding a CancelInbox (a Treiber stack head), a
// ClientCancelState and a FinishedWatermark. Slots are never deleted.
// Go atomics are sequentially consistent, which subsumes the acquire/release
// orderings the design requires.
//
// Design: abstraction/server/cancel_tracker/design.md#cancelregistry
type CancelRegistry struct {
	mask     uint64
	keys     []atomic.Uint64
	heads    []atomic.Pointer[inboxNode]
	finished []atomic.Uint64 // FinishedWatermark per slot: id + 1, 0 = none
	state    []clientCancelState
}

// ClientSlot is a client's registry slot index, or NoSlot when the registry
// was full. It stays valid for the life of the process.
//
// API: abstraction/server/cancel_tracker/api.md#clientslot
type ClientSlot int

// NoSlot is the design's slot "none".
const NoSlot ClientSlot = -1

// RecentFinished is RECENT_FINISHED: per client, how many finished requests
// CancelMatching remembers and how many finishes a stale /cancel id is kept for.
//
// API: abstraction/server/cancel_tracker/api.md#constants
const RecentFinished = 4096

type finishedEntry struct {
	id        uint64
	completed bool
}

// clientCancelState is shared by every worker that runs a request of the
// slot's client. Every field is used only with mu held. Maps and the ring are
// allocated on first use.
//
// Design: abstraction/server/cancel_tracker/design.md#clientcancelstate
type clientCancelState struct {
	mu          sync.Mutex
	pending     map[uint64]uint64   // drained /cancel id -> finishCount when drained
	unfinished  map[uint64]struct{} // started by a worker, not finished
	recent      []finishedEntry     // ring of up to RecentFinished entries
	recentNext  int                 // index overwritten next once the ring is full
	recentIndex map[uint64]bool     // ids now in recent -> completed
	finishCount uint64
}

func (st *clientCancelState) init() {
	if st.pending == nil {
		st.pending = map[uint64]uint64{}
		st.unfinished = map[uint64]struct{}{}
		st.recentIndex = map[uint64]bool{}
	}
}

// pushRecent adds a finished request to the ring, evicting the oldest when full.
func (st *clientCancelState) pushRecent(e finishedEntry) {
	if len(st.recent) < RecentFinished {
		st.recent = append(st.recent, e)
	} else {
		old := st.recent[st.recentNext]
		if c, ok := st.recentIndex[old.id]; ok && c == old.completed {
			delete(st.recentIndex, old.id)
		}
		st.recent[st.recentNext] = e
		st.recentNext = (st.recentNext + 1) % RecentFinished
	}
	st.recentIndex[e.id] = e.completed
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
		state:    make([]clientCancelState, capacity),
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

// Client finds clientID's slot, creating it if needed; NoSlot if the
// registry is full. Lock-free.
//
// Design: abstraction/server/cancel_tracker/design.md#cancelregistry
func (r *CancelRegistry) Client(clientID uint32) ClientSlot {
	i, ok := r.findSlot(clientID, true)
	if !ok {
		return NoSlot
	}
	return ClientSlot(i)
}

// Push adds id to clientID's inbox (multi-producer Treiber push). It never
// takes the client's lock.
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

// drain detaches the whole inbox of slot with one atomic Swap. The caller
// holds the slot's lock, so there is one consumer at a time. Detached nodes are
// reclaimed by the garbage collector, which stands in for the design's free.
//
// Design: abstraction/server/cancel_tracker/design.md#cancelinbox
func (r *CancelRegistry) drain(slot ClientSlot) []uint64 {
	var ids []uint64
	for h := r.heads[slot].Swap(nil); h != nil; h = h.next {
		ids = append(ids, h.id)
	}
	return ids
}

// Start is registry_start: a worker has started request id of slot's client.
//
// Design: abstraction/server/cancel_tracker/design.md#clientcancelstate
func (r *CancelRegistry) Start(slot ClientSlot, id uint64) {
	if slot == NoSlot {
		return
	}
	st := &r.state[slot]
	st.mu.Lock()
	defer st.mu.Unlock()
	st.init()
	st.unfinished[id] = struct{}{}
}

// PublishFinished is called by a worker when a request of slot's client
// finishes with any outcome (completed is true for 202). Under the slot's
// lock it moves id into the RecentFinished ring, resolves a pending /cancel
// for id, runs the aging sweep every RecentFinished finishes, and max-updates
// the FinishedWatermark. Slot NoSlot (registry full) does nothing.
//
// Design: abstraction/server/cancel_tracker/design.md#finishedwatermark
func (r *CancelRegistry) PublishFinished(slot ClientSlot, id uint64, completed bool, counters *CancelCounters) {
	if slot == NoSlot {
		return
	}
	st := &r.state[slot]
	st.mu.Lock()
	defer st.mu.Unlock()
	st.init()
	delete(st.unfinished, id)
	st.pushRecent(finishedEntry{id, completed})
	st.finishCount++
	if _, ok := st.pending[id]; ok { // the cancel came after the last CancelCheckPoint
		delete(st.pending, id)
		if completed {
			counters.CompletedBeforeCancel.Add(1)
		}
	}
	if st.finishCount%RecentFinished == 0 { // aging sweep
		for p, drainedAt := range st.pending {
			if _, live := st.unfinished[p]; !live && st.finishCount-drainedAt > RecentFinished {
				delete(st.pending, p) // stale
			}
		}
	}
	// One writer at a time (the lock), so load-compare-store needs no CAS.
	// id <= 2^64 - 2^32 - 1: id + 1 never wraps to 0.
	if id+1 > r.finished[slot].Load() {
		r.finished[slot].Store(id + 1)
	}
}

// Finished returns clientID's FinishedWatermark; ok is false for none. No lock.
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

// ConnectionCancelState is a connection's client binding. Only the
// connection's admission path touches it, under the admission lock.
//
// API: abstraction/server/cancel_tracker/api.md#connectioncancelstate
type ConnectionCancelState struct {
	bound    bool
	clientID uint32
}

// NewConnectionCancelState returns an unbound state.
func NewConnectionCancelState() *ConnectionCancelState {
	return &ConnectionCancelState{}
}

// resolve decides the fate of a just-drained /cancel id: keep it pending, or
// drop it (counting completed_before_cancel if its request completed).
//
// Design: abstraction/server/cancel_tracker/design.md#cancelmatching
func (st *clientCancelState) resolve(counters *CancelCounters, id uint64) (keep bool) {
	if _, ok := st.unfinished[id]; ok {
		return true // started, not finished: its own check will match
	}
	if completed, ok := st.recentIndex[id]; ok {
		if completed {
			counters.CompletedBeforeCancel.Add(1)
		}
		return false
	}
	return true // queued, future or stale (the aging sweep drops stale ones)
}

// ShouldCancel runs CancelMatching for the request targetID at a
// CancelCheckPoint, under the slot's lock. It matches by id only, so a
// client's requests may run and finish in any order. NoSlot never cancels.
//
// Design: abstraction/server/cancel_tracker/design.md#cancelmatching
// Design: abstraction/server/cancel_tracker/design.md#out-of-order-requests
func ShouldCancel(registry *CancelRegistry, slot ClientSlot, counters *CancelCounters, targetID uint64) bool {
	if slot == NoSlot {
		return false
	}
	st := &registry.state[slot]
	st.mu.Lock()
	defer st.mu.Unlock()
	st.init()
	for _, id := range registry.drain(slot) {
		if _, dup := st.pending[id]; dup {
			continue // a duplicate is stored once
		}
		if st.resolve(counters, id) {
			st.pending[id] = st.finishCount
		}
	}
	if _, ok := st.pending[targetID]; ok {
		delete(st.pending, targetID)
		return true // the caller counts before/during
	}
	return false
}

// HandleCancel serves a routed DELETE /cancel on the connection's handler,
// never on a worker, and takes no lock. A 202 carries the target
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
