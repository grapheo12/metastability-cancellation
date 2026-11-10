// Source: abstraction/ingress/inflight_map/design.md (inflight_map v0.3.0)
package ingress

import (
	"sync"
	"testing"
)

func rid(client, seq uint32) uint64 { return uint64(client)<<32 | uint64(seq) }

// The publish/clear table: requests of one client coexist; a late clear of a
// replaced (reused-id) entry never removes the newer one.
//
// Design: abstraction/ingress/inflight_map/design.md#publish-and-clear
func TestPublishClearTable(t *testing.T) {
	m := NewInFlightMap(4)
	a := &InFlightEntry{RequestID: rid(3, 7), ServerID: 0}
	b := &InFlightEntry{RequestID: rid(3, 8), ServerID: 1}
	m.Publish(a)
	m.Publish(b)
	if s := m.Snapshot(); len(s) != 2 {
		t.Fatalf("both requests of client 3 must be in flight: %+v", s)
	}
	if !m.Clear(a) {
		t.Fatal("clear A")
	}
	b2 := &InFlightEntry{RequestID: rid(3, 8), ServerID: 0} // reused id, different record
	m.Publish(b2)
	if m.Clear(b) {
		t.Fatal("stale clear of B must fail")
	}
	if s := m.Snapshot(); len(s) != 1 || s[0].Entry != b2 {
		t.Fatalf("B' must remain: %+v", s)
	}
	if !m.Clear(b2) || len(m.Snapshot()) != 0 {
		t.Fatal("clear B'")
	}
}

// Design: abstraction/ingress/inflight_map/design.md#publish-and-clear
func TestConcurrentPublishBeatsStaleClear(t *testing.T) {
	m := NewInFlightMap(1)
	for i := 0; i < 1000; i++ {
		a := &InFlightEntry{RequestID: uint64(i)}
		b := &InFlightEntry{RequestID: uint64(i)} // same id, newer publication
		m.Publish(a)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); m.Clear(a) }()
		go func() { defer wg.Done(); m.Publish(b) }()
		wg.Wait()
		s := m.Snapshot()
		if len(s) != 1 || s[0].Entry != b {
			t.Fatalf("iteration %d: newest publication lost: %+v", i, s)
		}
		m.Clear(b)
	}
}

// Snapshot worked example: 2.9 @ 0, 0.17 @ 1, 2.8 @ 1 come back in RequestId
// order; concurrent churn from many clients and many requests per client.
//
// Design: abstraction/ingress/inflight_map/design.md#snapshot
func TestSnapshotOrderAndChurn(t *testing.T) {
	m := NewInFlightMap(4)
	m.Publish(&InFlightEntry{RequestID: rid(2, 9), ServerID: 0})
	m.Publish(&InFlightEntry{RequestID: rid(0, 17), ServerID: 1})
	m.Publish(&InFlightEntry{RequestID: rid(2, 8), ServerID: 1})
	s := m.Snapshot()
	want := []uint64{rid(0, 17), rid(2, 8), rid(2, 9)}
	if len(s) != 3 {
		t.Fatalf("snapshot %+v", s)
	}
	for i, v := range s {
		if v.Entry.RequestID != want[i] || v.ClientID != uint32(want[i]>>32) {
			t.Fatalf("snapshot[%d] = %+v, want id %d", i, v, want[i])
		}
	}
	big := NewInFlightMap(16)
	var wg sync.WaitGroup
	for c := uint32(0); c < 16; c++ {
		for k := uint32(0); k < 4; k++ { // 4 concurrent streams per client
			wg.Add(1)
			go func(c, k uint32) {
				defer wg.Done()
				for i := uint32(0); i < 500; i++ {
					e := &InFlightEntry{RequestID: rid(c, i*4+k)}
					big.Publish(e)
					big.Clear(e)
				}
			}(c, k)
		}
	}
	for i := 0; i < 200; i++ {
		for _, v := range big.Snapshot() {
			if uint32(v.Entry.RequestID>>32) != v.ClientID {
				t.Fatalf("entry in wrong shard: %+v", v)
			}
		}
	}
	wg.Wait()
	if len(big.Snapshot()) != 0 {
		t.Fatal("all cleared")
	}
}

// Design: abstraction/ingress/inflight_map/design.md#inflightmap
func TestMaximumKeySpaceStartsEmpty(t *testing.T) {
	m := NewInFlightMap(MaxClients)
	if len(*m.active.Load()) != 0 || len(m.Snapshot()) != 0 {
		t.Fatal("a new map has no entries")
	}
	e := &InFlightEntry{RequestID: rid(MaxClients-1, 1)}
	m.Publish(e)
	if s := m.Snapshot(); len(s) != 1 || s[0].ClientID != MaxClients-1 || s[0].Entry != e {
		t.Fatalf("maximum valid key: %+v", s)
	}
}
