// Source: abstraction/ingress/inflight_map/design.md (inflight_map v0.1.0)
package ingress

import "sync/atomic"

// InFlightEntry is immutable once published.
//
// API: abstraction/ingress/inflight_map/api.md#inflightentry
type InFlightEntry struct {
	RequestID uint64
	ServerID  uint32
}

// InFlightMap is a lock-free map client_id -> *InFlightEntry: one atomic
// pointer per client. Go atomics are sequentially consistent, which subsumes
// the release/acquire the design asks for.
//
// Design: abstraction/ingress/inflight_map/design.md#inflightmap
type InFlightMap struct {
	slots []atomic.Pointer[InFlightEntry]
}

// Victim is one snapshot element.
type Victim struct {
	ClientID uint32
	Entry    *InFlightEntry
}

// NewInFlightMap returns numClients empty slots.
//
// Design: abstraction/ingress/inflight_map/design.md#inflightmap
func NewInFlightMap(numClients uint32) *InFlightMap {
	return &InFlightMap{slots: make([]atomic.Pointer[InFlightEntry], numClients)}
}

// Publish stores e as clientID's in-flight request.
//
// Design: abstraction/ingress/inflight_map/design.md#publish-and-clear
func (m *InFlightMap) Publish(clientID uint32, e *InFlightEntry) {
	m.slots[clientID].Store(e)
}

// Clear empties the slot only if it still holds exactly e (pointer identity).
//
// Design: abstraction/ingress/inflight_map/design.md#publish-and-clear
func (m *InFlightMap) Clear(clientID uint32, e *InFlightEntry) bool {
	return m.slots[clientID].CompareAndSwap(e, nil)
}

// Snapshot returns every non-empty slot in increasing client-id order.
//
// Design: abstraction/ingress/inflight_map/design.md#snapshot
func (m *InFlightMap) Snapshot() []Victim {
	var out []Victim
	for c := range m.slots {
		if p := m.slots[c].Load(); p != nil {
			out = append(out, Victim{ClientID: uint32(c), Entry: p})
		}
	}
	return out
}
