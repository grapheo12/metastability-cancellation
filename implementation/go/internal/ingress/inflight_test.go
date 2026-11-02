// Source: abstraction/ingress/inflight_map/design.md (inflight_map v0.1.0)
package ingress

import (
	"sync"
	"testing"
)

// The publish/clear table: a late clear of A never removes B.
//
// Design: abstraction/ingress/inflight_map/design.md#publish-and-clear
func TestPublishClearTable(t *testing.T) {
	m := NewInFlightMap(4)
	a, b := &InFlightEntry{RequestID: 1}, &InFlightEntry{RequestID: 1} // equal fields, different records
	m.Publish(3, a)
	if !m.Clear(3, a) || len(m.Snapshot()) != 0 {
		t.Fatal("clear A")
	}
	m.Publish(3, a)
	m.Publish(3, b)
	if m.Clear(3, a) {
		t.Fatal("stale clear of A must fail")
	}
	if s := m.Snapshot(); len(s) != 1 || s[0].Entry != b {
		t.Fatal("B must remain")
	}
	if !m.Clear(3, b) {
		t.Fatal("clear B")
	}
}

// Design: abstraction/ingress/inflight_map/design.md#snapshot
func TestSnapshotOrderAndChurn(t *testing.T) {
	m := NewInFlightMap(4)
	a, b := &InFlightEntry{RequestID: 10}, &InFlightEntry{RequestID: 30, ServerID: 1}
	m.Publish(3, b)
	m.Publish(0, a)
	s := m.Snapshot()
	if len(s) != 2 || s[0].ClientID != 0 || s[1].ClientID != 3 || s[1].Entry.ServerID != 1 {
		t.Fatalf("snapshot %+v", s)
	}
	// Concurrent publish/clear with snapshots: no panics, every seen entry is well formed.
	big := NewInFlightMap(64)
	var wg sync.WaitGroup
	for c := uint32(0); c < 64; c++ {
		wg.Add(1)
		go func(c uint32) {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				e := &InFlightEntry{RequestID: uint64(c)<<32 | uint64(i)}
				big.Publish(c, e)
				big.Clear(c, e)
			}
		}(c)
	}
	for i := 0; i < 200; i++ {
		for _, v := range big.Snapshot() {
			if uint32(v.Entry.RequestID>>32) != v.ClientID {
				t.Fatalf("entry in wrong slot: %+v", v)
			}
		}
	}
	wg.Wait()
	if len(big.Snapshot()) != 0 {
		t.Fatal("all cleared")
	}
}
