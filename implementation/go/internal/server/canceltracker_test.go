// Source: abstraction/server/cancel_tracker/design.md (cancel_tracker v0.1.0)
package server

import (
	"sort"
	"sync"
	"testing"
)

func boundState(clientID uint32) *ConnectionCancelState {
	st := NewConnectionCancelState()
	st.bound, st.clientID = true, clientID
	return st
}

func pendingSorted(st *ConnectionCancelState) []uint64 {
	var out []uint64
	for id := range st.pending.members {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Worked examples from the CancelMatching table.
//
// Design: abstraction/server/cancel_tracker/design.md#cancelmatching
func TestCancelMatchingWorkedExamples(t *testing.T) {
	cases := []struct {
		pending, inbox []uint64
		lastCompleted  uint64
		target         uint64
		want           bool
		pendingAfter   []uint64
		completedDelta uint64
	}{
		{[]uint64{3, 5, 9}, nil, 3, 5, true, []uint64{9}, 1},
		{[]uint64{4}, nil, 2, 6, false, nil, 0},
		{[]uint64{8}, nil, 5, 6, false, []uint64{8}, 0},
		{nil, []uint64{6}, 5, 6, true, nil, 0},
	}
	for i, c := range cases {
		reg := NewCancelRegistry(16)
		ctr := &CancelCounters{}
		st := boundState(0)
		st.hasLastCompleted, st.lastCompletedID = true, c.lastCompleted
		for _, id := range c.pending {
			st.pending.insert(id)
		}
		for _, id := range c.inbox {
			if err := reg.Push(0, id); err != nil {
				t.Fatal(err)
			}
		}
		if got := ShouldCancel(st, reg, ctr, c.target); got != c.want {
			t.Errorf("case %d: got %v want %v", i, got, c.want)
		}
		if got := pendingSorted(st); len(got) != len(c.pendingAfter) || (len(got) > 0 && got[0] != c.pendingAfter[0]) {
			t.Errorf("case %d: pending after %v want %v", i, got, c.pendingAfter)
		}
		if ctr.CompletedBeforeCancel.Load() != c.completedDelta {
			t.Errorf("case %d: completed_before_cancel %d", i, ctr.CompletedBeforeCancel.Load())
		}
	}
}

// Design: abstraction/server/cancel_tracker/design.md#cancelmatching
func TestUnboundNeverCancels(t *testing.T) {
	reg := NewCancelRegistry(16)
	_ = reg.Push(0, 0)
	if ShouldCancel(NewConnectionCancelState(), reg, &CancelCounters{}, 0) {
		t.Fatal("unbound state must not cancel")
	}
}

// Design: abstraction/server/cancel_tracker/design.md#cancelinbox
func TestConcurrentPushThenDrain(t *testing.T) {
	reg := NewCancelRegistry(1024)
	var wg sync.WaitGroup
	for p := 0; p < 8; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				_ = reg.Push(uint32(i%50), uint64(p*1000+i))
			}
		}(p)
	}
	wg.Wait()
	total := 0
	for c := uint32(0); c < 50; c++ {
		total += len(reg.Drain(boundState(c)))
	}
	if total != 8000 {
		t.Fatalf("drained %d ids, want 8000", total)
	}
}

// Design: abstraction/server/cancel_tracker/design.md#cancelregistry
func TestRegistryFull(t *testing.T) {
	reg := NewCancelRegistry(4)
	for c := uint32(0); c < 4; c++ {
		if err := reg.Push(c, 1); err != nil {
			t.Fatal(err)
		}
	}
	if reg.Push(4, 1) != ErrRegistryFull {
		t.Fatal("fifth client must not fit")
	}
	if reg.Push(2, 9) != nil {
		t.Fatal("existing client must still accept pushes")
	}
	if ids := reg.Drain(boundState(7)); ids != nil {
		t.Fatal("absent client drains nothing")
	}
}

// Design: abstraction/server/cancel_tracker/design.md#handle_cancel
func TestHandleCancel(t *testing.T) {
	reg := NewCancelRegistry(16)
	coop := Config{CancelMode: CancelCooperative}
	if r := HandleCancel([]byte(`{"id":"12884901889"}`), coop, reg); r.Status != 202 || len(r.Body) != 0 {
		t.Fatalf("got %+v", r)
	}
	if ids := reg.Drain(boundState(3)); len(ids) != 1 || ids[0] != 12884901889 {
		t.Fatalf("registry content %v", ids)
	}
	if r := HandleCancel([]byte(`nope`), coop, reg); r.Status != 400 {
		t.Fatalf("got %+v", r)
	}
	if r := HandleCancel([]byte(`{"id":"5"}`), Config{CancelMode: CancelIgnore}, reg); r.Status != 202 {
		t.Fatalf("got %+v", r)
	}
	if ids := reg.Drain(boundState(0)); ids != nil {
		t.Fatal("ignore mode must discard ids")
	}
}
