// Source: abstraction/ingress/load_canceller/design.md (load_canceller v0.3.0)
package ingress

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"testbed/internal/sampling"
)

// Design: abstraction/ingress/load_canceller/design.md#inflightvictimchoice
func TestChooseVictim(t *testing.T) {
	rng := sampling.NewRng(1)
	m := NewInFlightMap(4)
	before := *rng
	if _, ok := chooseVictim(rng, m); ok || *rng != before {
		t.Fatal("empty snapshot: no victim and no draw")
	}
	m.Publish(0, &InFlightEntry{RequestID: 17, ServerID: 1})
	m.Publish(2, &InFlightEntry{RequestID: 2<<32 | 9, ServerID: 0})
	m.Publish(3, &InFlightEntry{RequestID: 3<<32 | 30, ServerID: 1})
	counts := map[uint32]int{}
	for i := 0; i < 30000; i++ {
		v, ok := chooseVictim(rng, m)
		if !ok {
			t.Fatal("victim expected")
		}
		counts[v.ClientID]++
	}
	for _, c := range []uint32{0, 2, 3} {
		if counts[c] < 9000 || counts[c] > 11000 {
			t.Fatalf("not uniform: %v", counts)
		}
	}
}

// Design: abstraction/ingress/load_canceller/design.md#outcome-classification
func TestClassifyCancel(t *testing.T) {
	if classifyCancel(202) != CancelAccepted || classifyCancel(503) != CancelRejected ||
		classifyCancel(400) != CancelBadRequest || classifyCancel(500) != CancelError {
		t.Fatal("classification table")
	}
}

// Design: abstraction/ingress/load_canceller/design.md#summary
func TestSummarizeAndCSV(t *testing.T) {
	n, r, targeted := summarizeCancels(nil, Config{})
	if n.Scheduled != 0 || r.Offered != 0 || targeted != 0 {
		t.Fatal("disabled canceller: all zero")
	}
	recs := []*CancelRecord{
		{Arrival: 0, Skipped: true, Outcome: CancelSkipped, ScheduledNs: 5},
		{Arrival: 1, CancelID: 2<<32 | 9, ClientID: 2, ServerID: 1, TargetSeq: 9, Outcome: CancelAccepted,
			HTTPStatus: 202, ScheduledNs: 10, SendNs: 11, EndNs: 12, HasEnd: true},
	}
	n, r, targeted = summarizeCancels(recs, Config{DurationS: 2})
	if n.Scheduled != 2 || n.Sent != 1 || n.Accepted != 1 || n.Skipped != 1 || r.Offered != 1 || targeted != 1 {
		t.Fatalf("summary %+v %+v %d", n, r, targeted)
	}
	path := filepath.Join(t.TempDir(), "c.csv")
	if err := writeCancelsCSV(path, recs); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	want := []string{
		"arrival,cancel_id,client_id,server_id,target_seq,outcome,http_status,scheduled_ns,send_ns,end_ns",
		"0,,,,,skipped,,5,,",
		"1,8589934601,2,1,9,accepted,202,10,11,12",
	}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Fatalf("csv:\n%s", b)
	}
}
