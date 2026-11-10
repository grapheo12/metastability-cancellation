// Source: abstraction/ingress/inflight_map/design.md (inflight_map v0.3.0)
package ingress

import (
	"sort"
	"sync/atomic"
)

// InFlightEntry is immutable once published.
//
// API: abstraction/ingress/inflight_map/api.md#inflightentry
type InFlightEntry struct {
	RequestID uint64
	ServerID  uint32
}

// clientShard holds one client's in-flight entries, keyed by RequestId. It is
// immutable once published; updates copy it and swap the client's pointer.
type clientShard struct {
	entries map[uint64]*InFlightEntry
}

// InFlightMap is a lock-free map request_id -> *InFlightEntry, sharded by
// client id (request_id >> 32): one atomic pointer per client to an immutable
// copy-on-write shard. A publish or clear copies only that client's entries
// (at most n_waiting_requests) and retries its CAS only against the same
// client's concurrent updates.
//
// Design: abstraction/ingress/inflight_map/design.md#inflightmap
type InFlightMap struct {
	numClients uint32
	shards     []atomic.Pointer[clientShard] // nil until the client's first publish
	// active lists the clients that have ever published (copy-on-write, append
	// only), so Snapshot visits only those instead of all numClients shards.
	active atomic.Pointer[[]uint32]
}

// Victim is one snapshot element.
type Victim struct {
	ClientID uint32
	Entry    *InFlightEntry
}

// NewInFlightMap returns an empty map whose keys have client ids below numClients.
//
// Design: abstraction/ingress/inflight_map/design.md#inflightmap
func NewInFlightMap(numClients uint32) *InFlightMap {
	if numClients == 0 || numClients > MaxClients {
		panic("numClients out of range")
	}
	m := &InFlightMap{numClients: numClients, shards: make([]atomic.Pointer[clientShard], numClients)}
	m.active.Store(&[]uint32{})
	return m
}

// Publish stores e under e.RequestID.
//
// Design: abstraction/ingress/inflight_map/design.md#publish-and-clear
func (m *InFlightMap) Publish(e *InFlightEntry) {
	p := m.shard(e.RequestID)
	for {
		old := p.Load()
		next := &clientShard{entries: cloneShard(old, 1)}
		next.entries[e.RequestID] = e
		if p.CompareAndSwap(old, next) {
			if old == nil {
				m.markActive(uint32(e.RequestID >> 32))
			}
			return
		}
	}
}

// Clear removes e.RequestID only if it still maps to exactly e (pointer identity).
//
// Design: abstraction/ingress/inflight_map/design.md#publish-and-clear
func (m *InFlightMap) Clear(e *InFlightEntry) bool {
	p := m.shard(e.RequestID)
	for {
		old := p.Load()
		if old == nil || old.entries[e.RequestID] != e {
			return false
		}
		next := &clientShard{entries: cloneShard(old, 0)}
		delete(next.entries, e.RequestID)
		if p.CompareAndSwap(old, next) {
			return true
		}
	}
}

// Snapshot returns every observed entry in increasing RequestId order. Each
// shard is read atomically; shards are read one after another (weakly
// consistent across clients, as the design allows).
//
// Design: abstraction/ingress/inflight_map/design.md#snapshot
func (m *InFlightMap) Snapshot() []Victim {
	var out []Victim
	for _, c := range *m.active.Load() {
		if sh := m.shards[c].Load(); sh != nil {
			for _, e := range sh.entries {
				out = append(out, Victim{ClientID: c, Entry: e})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Entry.RequestID < out[j].Entry.RequestID })
	return out
}

// markActive appends c to the active list once (CAS loop). A shard becomes
// non-nil only once, so each client is appended at most once.
func (m *InFlightMap) markActive(c uint32) {
	for {
		old := m.active.Load()
		next := append(append(make([]uint32, 0, len(*old)+1), *old...), c)
		if m.active.CompareAndSwap(old, &next) {
			return
		}
	}
}

func (m *InFlightMap) shard(requestID uint64) *atomic.Pointer[clientShard] {
	clientID := requestID >> 32
	if clientID >= uint64(m.numClients) {
		panic("client id out of range")
	}
	return &m.shards[clientID]
}

func cloneShard(sh *clientShard, extra int) map[uint64]*InFlightEntry {
	if sh == nil {
		return make(map[uint64]*InFlightEntry, extra)
	}
	copy := make(map[uint64]*InFlightEntry, len(sh.entries)+extra)
	for id, e := range sh.entries {
		copy[id] = e
	}
	return copy
}
