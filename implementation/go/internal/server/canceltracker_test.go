// Source: abstraction/server/cancel_tracker/design.md (cancel_tracker v0.4.0)
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
	for id := range reg.state[slot].pending {
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

// fixture: recent = [(3, completed), (5, RST)], unfinished = {6, 7}.
func matchingFixture(t *testing.T) (*CancelRegistry, ClientSlot, *CancelCounters) {
	reg, ctr := NewCancelRegistry(16), &CancelCounters{}
	slot := slotOf(t, reg, 0)
	for _, id := range []uint64{3, 5, 6, 7} {
		reg.Start(slot, id)
	}
	reg.PublishFinished(slot, 3, true, ctr)
	reg.PublishFinished(slot, 5, false, ctr)
	return reg, slot, ctr
}

// Worked examples from the CancelMatching table.
//
// Design: abstraction/server/cancel_tracker/design.md#cancelmatching
func TestCancelMatchingWorkedExamples(t *testing.T) {
	cases := []struct {
		pendingBefore, inbox []uint64
		target               uint64
		want                 bool
		pendingAfter         []uint64
		completedDelta       uint64
	}{
		{nil, []uint64{3}, 6, false, nil, 1},
		{nil, []uint64{5}, 6, false, nil, 0},
		{nil, []uint64{7}, 6, false, []uint64{7}, 0},
		{[]uint64{7}, nil, 7, true, nil, 0},
		{nil, []uint64{9}, 6, false, []uint64{9}, 0},
		{nil, []uint64{6}, 6, true, nil, 0},
	}
	for i, c := range cases {
		reg, slot, ctr := matchingFixture(t)
		for _, id := range c.pendingBefore {
			reg.state[slot].pending[id] = 0
		}
		for _, id := range c.inbox {
			_ = reg.Push(0, id)
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
	if ShouldCancel(NewCancelRegistry(16), NoSlot, &CancelCounters{}, 0) {
		t.Fatal("NoSlot must not cancel")
	}
}

// The Out-of-order requests table: concurrent requests, out-of-order start,
// a cancel after the last check point, and a stale id swept after R finishes.
//
// Design: abstraction/server/cancel_tracker/design.md#out-of-order-requests
func TestOutOfOrderRequests(t *testing.T) {
	reg, ctr := NewCancelRegistry(16), &CancelCounters{}
	slot := slotOf(t, reg, 0)
	// 10 and 11 run at once; /cancel 10 is drained by 11's check.
	reg.Start(slot, 10)
	reg.Start(slot, 11)
	_ = reg.Push(0, 10)
	if ShouldCancel(reg, slot, ctr, 11) {
		t.Fatal("11 must not be cancelled")
	}
	if !ShouldCancel(reg, slot, ctr, 10) {
		t.Fatal("10's own check must match")
	}
	// 21 starts before 20; /cancel 20 drained by 21's check is kept, then matched.
	reg.Start(slot, 21)
	_ = reg.Push(0, 20)
	if ShouldCancel(reg, slot, ctr, 21) {
		t.Fatal("21 must not be cancelled")
	}
	reg.Start(slot, 20)
	if !ShouldCancel(reg, slot, ctr, 20) {
		t.Fatal("20 must match once started")
	}
	// /cancel 12 after 12's last check point, before its 202.
	reg.Start(slot, 12)
	_ = reg.Push(0, 12)
	ShouldCancel(reg, slot, ctr, 11) // another request's check drains it
	reg.PublishFinished(slot, 12, true, ctr)
	if ctr.CompletedBeforeCancel.Load() != 1 {
		t.Fatalf("completed_before_cancel %d, want 1", ctr.CompletedBeforeCancel.Load())
	}
	// /cancel 13 for a request never started: swept after more than R finishes.
	_ = reg.Push(0, 13)
	ShouldCancel(reg, slot, ctr, 11)
	if _, ok := reg.state[slot].pending[13]; !ok {
		t.Fatal("13 must be pending")
	}
	for i := uint64(0); i < 2*RecentFinished; i++ {
		reg.Start(slot, 1000+i)
		reg.PublishFinished(slot, 1000+i, false, ctr)
	}
	if _, ok := reg.state[slot].pending[13]; ok {
		t.Fatal("stale 13 must be swept")
	}
	if n := len(reg.state[slot].recent); n != RecentFinished || len(reg.state[slot].recentIndex) != RecentFinished {
		t.Fatalf("ring %d, index %d, want %d", n, len(reg.state[slot].recentIndex), RecentFinished)
	}
	if ctr.CompletedBeforeCancel.Load() != 1 {
		t.Fatal("RST/503 finishes and sweeps count nothing")
	}
}

// Many workers run requests of one client concurrently while cancels arrive:
// every cancelled id is matched exactly once and none is lost (run with -race
// where available).
//
// Design: abstraction/server/cancel_tracker/design.md#clientcancelstate
func TestConcurrentSameClientMatching(t *testing.T) {
	reg, ctr := NewCancelRegistry(16), &CancelCounters{}
	slot := slotOf(t, reg, 1)
	const n = 400
	for id := uint64(0); id < n; id++ {
		reg.Start(slot, id)
		_ = reg.Push(1, id)
	}
	var matched sync.Map
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for id := uint64(n - 1 - uint64(w)); ; id -= 4 { // descending: out of order on purpose
				if ShouldCancel(reg, slot, ctr, id) {
					if _, dup := matched.LoadOrStore(id, true); dup {
						t.Errorf("id %d matched twice", id)
					}
				}
				reg.PublishFinished(slot, id, false, ctr)
				if id < 4 {
					return
				}
			}
		}(w)
	}
	wg.Wait()
	count := 0
	matched.Range(func(_, _ any) bool { count++; return true })
	if count != n {
		t.Fatalf("matched %d of %d cancels", count, n)
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

// Worked example table: seq 0 (202), 1 (503), 2 (RST); a 400 publishes
// nothing; out-of-order finishes keep the max.
//
// Design: abstraction/server/cancel_tracker/design.md#finishedwatermark
func TestFinishedWatermark(t *testing.T) {
	reg, ctr := NewCancelRegistry(16), &CancelCounters{}
	if _, ok := reg.Finished(3); ok {
		t.Fatal("no slot yet: want none")
	}
	slot := slotOf(t, reg, 3)
	for seq := uint32(0); seq < 3; seq++ {
		id := uint64(3)<<32 | uint64(seq)
		reg.PublishFinished(slot, id, seq == 0, ctr)
		if got, ok := reg.Finished(3); !ok || got != id {
			t.Fatalf("after seq %d: %d %v", seq, got, ok)
		}
	}
	id4, id5 := uint64(3)<<32|4, uint64(3)<<32|5
	reg.PublishFinished(slot, id5, true, ctr)
	reg.PublishFinished(slot, id4, true, ctr)
	if got, ok := reg.Finished(3); !ok || got != id5 {
		t.Fatalf("watermark %d %v, want id(5)", got, ok)
	}
	coop := Config{CancelMode: CancelCooperative}
	if r := HandleCancel([]byte(`{"id":"12884901894"}`), coop, reg); string(r.Body) != `{"finished":"12884901893"}` {
		t.Fatalf("reply %s", r.Body)
	}
	reg.PublishFinished(slotOf(t, reg, 0), 0, true, ctr) // id 0 is distinguishable from none
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
	reg.Start(slot, uint64(2)<<32)
	reg.PublishFinished(slot, uint64(2)<<32, true, &CancelCounters{})
	if _, ok := reg.Finished(2); ok || slot != NoSlot {
		t.Fatal("full registry: nothing published")
	}
}
