// Source: abstraction/server/cancel_tracker/design.md (cancel_tracker v0.2.0)
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
	if r := HandleCancel([]byte(`{"id":"12884901889"}`), coop, reg); r.Status != 202 || string(r.Body) != `{"finished":null}` {
		t.Fatalf("got %+v", r)
	}
	if ids := reg.Drain(boundState(3)); len(ids) != 1 || ids[0] != 12884901889 {
		t.Fatalf("registry content %v", ids)
	}
	if r := HandleCancel([]byte(`nope`), coop, reg); r.Status != 400 || len(r.Body) != 0 {
		t.Fatalf("got %+v", r)
	}
	if r := HandleCancel([]byte(`{"id":"5"}`), Config{CancelMode: CancelIgnore}, reg); r.Status != 202 {
		t.Fatalf("got %+v", r)
	}
	if ids := reg.Drain(boundState(0)); ids != nil {
		t.Fatal("ignore mode must discard ids")
	}
}

// Worked example table: client 3 finishes seq 0 (202), 1 (503), 2 (RST); a 400 publishes nothing.
//
// Design: abstraction/server/cancel_tracker/design.md#finishedwatermark
func TestFinishedWatermark(t *testing.T) {
	reg := NewCancelRegistry(16)
	if _, ok := reg.Finished(3); ok {
		t.Fatal("no slot yet: want none")
	}
	st := boundState(3)
	for seq := uint32(0); seq < 3; seq++ {
		id := uint64(3)<<32 | uint64(seq)
		reg.PublishFinished(st, id)
		if got, ok := reg.Finished(3); !ok || got != id {
			t.Fatalf("after seq %d: %d %v", seq, got, ok)
		}
	}
	if st.slot < 0 {
		t.Fatal("publishing must cache the slot")
	}
	coop := Config{CancelMode: CancelCooperative}
	if r := HandleCancel([]byte(`{"id":"12884901891"}`), coop, reg); string(r.Body) != `{"finished":"12884901890"}` {
		t.Fatalf("reply %s", r.Body)
	}
	if r := HandleCancel([]byte(`{"id":"12884901891"}`), Config{CancelMode: CancelIgnore}, reg); r.Status != 202 ||
		string(r.Body) != `{"finished":"12884901890"}` {
		t.Fatalf("ignore mode reply %d %s", r.Status, r.Body)
	}
	// Id 0 must be distinguishable from none (stored as 1).
	st0 := boundState(0)
	reg.PublishFinished(st0, 0)
	if got, ok := reg.Finished(0); !ok || got != 0 {
		t.Fatalf("id 0: %d %v", got, ok)
	}
}

// Design: abstraction/server/cancel_tracker/design.md#finishedwatermark
func TestPublishFinishedRegistryFull(t *testing.T) {
	reg := NewCancelRegistry(1)
	if reg.Push(1, 7) != nil {
		t.Fatal("first client must fit")
	}
	st := boundState(2)
	reg.PublishFinished(st, uint64(2)<<32)
	if _, ok := reg.Finished(2); ok || st.slot >= 0 {
		t.Fatal("full registry: nothing published")
	}
	if r := HandleCancel([]byte(`{"id":"8589934593"}`), Config{CancelMode: CancelIgnore}, reg); string(r.Body) != `{"finished":null}` {
		t.Fatalf("reply %s", r.Body)
	}
}
