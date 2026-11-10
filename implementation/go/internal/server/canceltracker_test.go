// Source: abstraction/server/cancel_tracker/design.md (cancel_tracker v0.3.0)
package server

import (
	"sort"
	"sync"
	"testing"
)

func slotOf(t *testing.T, reg *CancelRegistry, clientID uint32) ClientSlot {
	t.Helper()
	slot := reg.Client(clientID)
	if slot == NoSlot {
		t.Fatal("registry full")
	}
	return slot
}

func pendingSorted(reg *CancelRegistry, slot ClientSlot) []uint64 {
	var out []uint64
	for id := range reg.state[slot].pending.members {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func lockedDrain(reg *CancelRegistry, slot ClientSlot) []uint64 {
	reg.state[slot].mu.Lock()
	defer reg.state[slot].mu.Unlock()
	return reg.drain(slot)
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
		slot := slotOf(t, reg, 0)
		st := &reg.state[slot]
		st.hasLastCompleted, st.lastCompletedID = true, c.lastCompleted
		for _, id := range c.pending {
			st.pending.insert(id)
		}
		for _, id := range c.inbox {
			if err := reg.Push(0, id); err != nil {
				t.Fatal(err)
			}
		}
		if got := ShouldCancel(reg, slot, ctr, c.target); got != c.want {
			t.Errorf("case %d: got %v want %v", i, got, c.want)
		}
		if got := pendingSorted(reg, slot); len(got) != len(c.pendingAfter) || (len(got) > 0 && got[0] != c.pendingAfter[0]) {
			t.Errorf("case %d: pending after %v want %v", i, got, c.pendingAfter)
		}
		if ctr.CompletedBeforeCancel.Load() != c.completedDelta {
			t.Errorf("case %d: completed_before_cancel %d", i, ctr.CompletedBeforeCancel.Load())
		}
	}
}

// Design: abstraction/server/cancel_tracker/design.md#cancelmatching
func TestNoSlotNeverCancels(t *testing.T) {
	reg := NewCancelRegistry(16)
	_ = reg.Push(0, 0)
	if ShouldCancel(reg, NoSlot, &CancelCounters{}, 0) {
		t.Fatal("NoSlot must not cancel")
	}
	// Empty pending, no last_completed_id, target 0: false, nothing counted.
	ctr := &CancelCounters{}
	if ShouldCancel(reg, slotOf(t, reg, 5), ctr, 0) || ctr.CompletedBeforeCancel.Load() != 0 {
		t.Fatal("empty state must not cancel")
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
		total += len(lockedDrain(reg, slotOf(t, reg, c)))
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
	if reg.Client(7) != NoSlot {
		t.Fatal("a new client must not get a slot in a full registry")
	}
	if reg.Client(2) == NoSlot {
		t.Fatal("an existing client keeps its slot")
	}
}

// Design: abstraction/server/cancel_tracker/design.md#handle_cancel
func TestHandleCancel(t *testing.T) {
	reg := NewCancelRegistry(16)
	coop := Config{CancelMode: CancelCooperative}
	if r := HandleCancel([]byte(`{"id":"12884901889"}`), coop, reg); r.Status != 202 || string(r.Body) != `{"finished":null}` {
		t.Fatalf("got %+v", r)
	}
	if ids := lockedDrain(reg, slotOf(t, reg, 3)); len(ids) != 1 || ids[0] != 12884901889 {
		t.Fatalf("registry content %v", ids)
	}
	if r := HandleCancel([]byte(`nope`), coop, reg); r.Status != 400 || len(r.Body) != 0 {
		t.Fatalf("got %+v", r)
	}
	if r := HandleCancel([]byte(`{"id":"5"}`), Config{CancelMode: CancelIgnore}, reg); r.Status != 202 {
		t.Fatalf("got %+v", r)
	}
	if ids := lockedDrain(reg, slotOf(t, reg, 0)); ids != nil {
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
	slot := slotOf(t, reg, 3)
	for seq := uint32(0); seq < 3; seq++ {
		id := uint64(3)<<32 | uint64(seq)
		reg.PublishFinished(slot, id, seq == 0) // 202, 503, RST
		if got, ok := reg.Finished(3); !ok || got != id {
			t.Fatalf("after seq %d: %d %v", seq, got, ok)
		}
	}
	if st := &reg.state[slot]; !st.hasLastCompleted || st.lastCompletedID != uint64(3)<<32 {
		t.Fatal("last_completed_id must stay at seq 0 (only 202 raises it)")
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
	reg.PublishFinished(slotOf(t, reg, 0), 0, true)
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
	slot := reg.Client(2)
	reg.PublishFinished(slot, uint64(2)<<32, true)
	if _, ok := reg.Finished(2); ok || slot != NoSlot {
		t.Fatal("full registry: nothing published")
	}
	if r := HandleCancel([]byte(`{"id":"8589934593"}`), Config{CancelMode: CancelIgnore}, reg); string(r.Body) != `{"finished":null}` {
		t.Fatalf("reply %s", r.Body)
	}
}

// Two requests of one client finish out of order (seq 5 before seq 4): both
// values are max-updates and never go down.
//
// Design: abstraction/server/cancel_tracker/design.md#finishedwatermark
func TestOutOfOrderFinishKeepsMax(t *testing.T) {
	reg := NewCancelRegistry(16)
	slot := slotOf(t, reg, 3)
	id4, id5 := uint64(3)<<32|4, uint64(3)<<32|5
	reg.PublishFinished(slot, id5, true)
	reg.PublishFinished(slot, id4, true)
	if got, ok := reg.Finished(3); !ok || got != id5 {
		t.Fatalf("watermark %d %v, want id(5)", got, ok)
	}
	if st := &reg.state[slot]; st.lastCompletedID != id5 {
		t.Fatalf("last_completed_id %d, want id(5)", st.lastCompletedID)
	}
}

// Worked example: /cancel 4 and 5 while reset request 4 still runs; the check
// for 5 drops 4 as stale and cancels 5; 4's late finish lowers nothing.
//
// Design: abstraction/server/cancel_tracker/design.md#overlap-and-ordering
func TestOverlapWorkedExample(t *testing.T) {
	reg := NewCancelRegistry(16)
	ctr := &CancelCounters{}
	slot := slotOf(t, reg, 0)
	reg.PublishFinished(slot, 3, true) // state before: last_completed_id 3
	_ = reg.Push(0, 4)
	_ = reg.Push(0, 5)
	if !ShouldCancel(reg, slot, ctr, 5) {
		t.Fatal("5 must be cancelled")
	}
	reg.PublishFinished(slot, 5, false) // 503
	if got := pendingSorted(reg, slot); len(got) != 0 {
		t.Fatalf("pending %v, want empty", got)
	}
	reg.PublishFinished(slot, 4, false) // 4 aborted by RST, finishes late
	if f, _ := reg.Finished(0); f != 5 || reg.state[slot].lastCompletedID != 3 || ctr.CompletedBeforeCancel.Load() != 0 {
		t.Fatalf("watermark %d last_completed %d completed_before_cancel %d", f, reg.state[slot].lastCompletedID, ctr.CompletedBeforeCancel.Load())
	}
}

// Many workers check one client at once while cancels are pushed: every
// cancelled id is matched exactly once (run with -race).
//
// Design: abstraction/server/cancel_tracker/design.md#clientcancelstate
func TestConcurrentShouldCancel(t *testing.T) {
	reg := NewCancelRegistry(16)
	ctr := &CancelCounters{}
	slot := slotOf(t, reg, 1)
	const n = 2000
	var matched sync.Map
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for id := uint64(0); id < n; id++ {
			_ = reg.Push(1, id)
		}
	}()
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for id := uint64(w); id < n; id += 4 {
				for spin := 0; spin < 1000; spin++ { // checks until pushed or dropped
					if ShouldCancel(reg, slot, ctr, id) {
						if _, dup := matched.LoadOrStore(id, true); dup {
							t.Errorf("id %d matched twice", id)
						}
						break
					}
				}
			}
		}(w)
	}
	wg.Wait()
	if ctr.CompletedBeforeCancel.Load() != 0 {
		t.Fatal("nothing completed: no completed_before_cancel")
	}
}
