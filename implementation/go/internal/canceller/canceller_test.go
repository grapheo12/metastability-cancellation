// Source: abstraction/load_canceller/design.md (load_canceller v0.2.0)
package canceller

import (
	"math"
	"testing"

	"testbed/internal/protocol"
)

// Worked example from the CancelTargetChoice table (incr_rand_max = 4).
//
// Design: abstraction/load_canceller/design.md#canceltargetchoice
func TestCancelTargetChoiceWorkedExample(t *testing.T) {
	f := finishedTable{seq: map[uint32]uint32{}}
	target := func(client uint32, step uint64) uint32 {
		base, has := f.get(client)
		seq, ok := nextTarget(base, has, step)
		if !ok {
			t.Fatalf("client %d step %d skipped", client, step)
		}
		return seq
	}
	if got := target(3, 2); got != 1 { // arrival 0: step - 1
		t.Fatalf("arrival 0: %d", got)
	}
	f.note(3, protocol.MakeRequestID(4, 3)) // reply to arrival 0
	if got := target(3, 1); got != 5 {      // arrival 1: 4 + 1
		t.Fatalf("arrival 1: %d", got)
	}
	if got := target(0, 4); got != 3 { // arrival 2: no reply for client 0
		t.Fatalf("arrival 2: %d", got)
	}
	if got := target(3, 3); got != 7 { // arrival 3: 4 + 3
		t.Fatalf("arrival 3: %d", got)
	}
	f.note(3, protocol.MakeRequestID(6, 3)) // reply to arrival 3
	f.note(3, protocol.MakeRequestID(5, 3)) // reply to arrival 1, read late: max keeps 6
	if base, _ := f.get(3); base != 6 {
		t.Fatalf("max not kept: %d", base)
	}
	f.note(0, protocol.MakeRequestID(9, 1)) // reply about another client: ignored
	if _, has := f.get(0); has {
		t.Fatal("reply for client 1 must not set client 0")
	}
}

// Design: abstraction/load_canceller/design.md#canceltargetchoice
func TestNextTargetBoundary(t *testing.T) {
	if _, ok := nextTarget(math.MaxUint32-1, true, 3); ok {
		t.Fatal("2^32 - 2 + 3 must be skipped")
	}
	if seq, ok := nextTarget(math.MaxUint32-1, true, 1); !ok || seq != math.MaxUint32 {
		t.Fatalf("2^32 - 1 is the last valid seq: %d %v", seq, ok)
	}
	if seq, ok := nextTarget(0, false, math.MaxUint32); !ok || seq != math.MaxUint32-1 {
		t.Fatalf("no reply, max step: %d %v", seq, ok)
	}
}

// Design: abstraction/load_canceller/design.md#response-collection
func TestCompleteParsesReply(t *testing.T) {
	s := &sender{}
	s.finished.seq = map[uint32]uint32{}
	s.outstanding.Add(3)
	ok := &CancelRecord{ClientID: 2}
	s.complete(ok, 202, protocol.BuildCancelReply(protocol.MakeRequestID(10, 2), true), nil)
	if ok.Outcome != OutcomeAccepted || !ok.HasReported || ok.ReportedFinished != protocol.MakeRequestID(10, 2) {
		t.Fatalf("202 with watermark: %+v", ok)
	}
	if base, has := s.finished.get(2); !has || base != 10 {
		t.Fatalf("watermark not noted: %d %v", base, has)
	}
	null := &CancelRecord{ClientID: 3}
	s.complete(null, 202, protocol.BuildCancelReply(0, false), nil)
	bad := &CancelRecord{ClientID: 3}
	s.complete(bad, 202, []byte("junk"), nil)
	if null.Outcome != OutcomeAccepted || null.HasReported || bad.Outcome != OutcomeAccepted || bad.HasReported {
		t.Fatal("null or unparseable reply: still accepted, nothing reported")
	}
	if _, has := s.finished.get(3); has {
		t.Fatal("null reply must not set a watermark")
	}
}

// Design: abstraction/load_canceller/design.md#configuration-validation
func TestValidateIncrRandMax(t *testing.T) {
	p := DefaultParams()
	p.NumClients, p.CancelRate, p.DurationS = "4", "10", "1"
	if c, err := Validate(p); err != nil || c.IncrRandMax != 4 {
		t.Fatalf("default: %v %v", c.IncrRandMax, err)
	}
	for _, bad := range []string{"0", "-1", "4294967296", "x", ""} {
		p.IncrRandMax = bad
		if _, err := Validate(p); err == nil {
			t.Errorf("incr_rand_max %q accepted", bad)
		}
	}
	p.IncrRandMax = "4294967295"
	if _, err := Validate(p); err != nil {
		t.Errorf("2^32-1 rejected: %v", err)
	}
}

// Design: abstraction/load_canceller/design.md#outcome-classification
func TestClassify(t *testing.T) {
	if classify(202) != OutcomeAccepted || classify(503) != OutcomeRejected ||
		classify(400) != OutcomeBadRequest || classify(500) != OutcomeError {
		t.Fatal("classification table")
	}
}
