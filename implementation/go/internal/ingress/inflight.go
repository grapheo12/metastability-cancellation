// Source: abstraction/ingress/inflight_map/design.md (inflight_map v0.2.0)
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

type inFlightState struct {
	entries map[uint32]*InFlightEntry
}

// InFlightMap is a lock-free map client_id -> *InFlightEntry. Every published
// state is immutable; updates copy the entries and swap the state pointer.
//
// Design: abstraction/ingress/inflight_map/design.md#inflightmap
type InFlightMap struct {
	numClients uint32
	state      atomic.Pointer[inFlightState]
}

// Victim is one snapshot element.
type Victim struct {
	ClientID uint32
	Entry    *InFlightEntry
}

// NewInFlightMap returns an empty map whose keys are less than numClients.
//
// Design: abstraction/ingress/inflight_map/design.md#inflightmap
func NewInFlightMap(numClients uint32) *InFlightMap {
	if numClients == 0 || numClients > MaxClients {
		panic("numClients out of range")
	}
	m := &InFlightMap{numClients: numClients}
	m.state.Store(&inFlightState{entries: make(map[uint32]*InFlightEntry)})
	return m
}

// Publish stores e as clientID's in-flight request.
//
// Design: abstraction/ingress/inflight_map/design.md#publish-and-clear
func (m *InFlightMap) Publish(clientID uint32, e *InFlightEntry) {
	m.requireClient(clientID)
	for {
		old := m.state.Load()
		next := &inFlightState{entries: cloneInFlightEntries(old.entries)}
		next.entries[clientID] = e
		if m.state.CompareAndSwap(old, next) {
			return
		}
	}
}

// Clear empties the slot only if it still holds exactly e (pointer identity).
//
// Design: abstraction/ingress/inflight_map/design.md#publish-and-clear
func (m *InFlightMap) Clear(clientID uint32, e *InFlightEntry) bool {
	m.requireClient(clientID)
	for {
		old := m.state.Load()
		current, ok := old.entries[clientID]
		if !ok || current != e {
			return false
		}
		next := &inFlightState{entries: cloneInFlightEntries(old.entries)}
		delete(next.entries, clientID)
		if m.state.CompareAndSwap(old, next) {
			return true
		}
	}
}

// Snapshot returns every observed entry in increasing client-id order.
//
// Design: abstraction/ingress/inflight_map/design.md#snapshot
func (m *InFlightMap) Snapshot() []Victim {
	state := m.state.Load()
	out := make([]Victim, 0, len(state.entries))
	for clientID, entry := range state.entries {
		if clientID < m.numClients {
			out = append(out, Victim{ClientID: clientID, Entry: entry})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ClientID < out[j].ClientID })
	return out
}

func (m *InFlightMap) requireClient(clientID uint32) {
	if clientID >= m.numClients {
		panic("clientID out of range")
	}
}

func cloneInFlightEntries(entries map[uint32]*InFlightEntry) map[uint32]*InFlightEntry {
	copy := make(map[uint32]*InFlightEntry, len(entries)+1)
	for clientID, entry := range entries {
		copy[clientID] = entry
	}
	return copy
}
