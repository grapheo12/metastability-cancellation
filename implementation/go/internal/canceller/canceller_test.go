// Source: abstraction/load_canceller/design.md (load_canceller v0.1.0)
package canceller

import (
	"math"
	"testing"

	"testbed/internal/protocol"
)

// Worked example from the CancelTargetChoice table (INCR_RAND_MAX = 4).
//
// Design: abstraction/load_canceller/design.md#canceltargetchoice
func TestNextTargetWorkedExample(t *testing.T) {
	last := map[uint32]uint64{}
	steps := []struct {
		client uint32
		step   uint64
		seq    uint32
		id     uint64
	}{
		{3, 2, 1, 12884901889},
		{0, 4, 3, 3},
		{3, 1, 2, 12884901890},
		{3, 3, 5, 12884901893},
	}
	for i, s := range steps {
		seq, ok := nextTarget(last, s.client, s.step)
		if !ok || seq != s.seq || protocol.MakeRequestID(seq, s.client) != s.id {
			t.Fatalf("arrival %d: seq %d id %d", i, seq, protocol.MakeRequestID(seq, s.client))
		}
	}
	last[9] = math.MaxUint32
	if _, ok := nextTarget(last, 9, 1); ok || last[9] != math.MaxUint32 {
		t.Fatal("exhausted counter must be skipped and left unchanged")
	}
}

// Design: abstraction/load_canceller/design.md#outcome-classification
func TestClassify(t *testing.T) {
	if classify(202) != OutcomeAccepted || classify(503) != OutcomeRejected ||
		classify(400) != OutcomeBadRequest || classify(500) != OutcomeError {
		t.Fatal("classification table")
	}
}
