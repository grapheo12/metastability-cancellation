// Source: abstraction/ingress/load_canceller/design.md (load_canceller v0.5.0)
package ingress

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	m.Publish(&InFlightEntry{RequestID: 17, ServerID: 1})
	m.Publish(&InFlightEntry{RequestID: 18, ServerID: 0}) // client 0 has two requests in flight
	m.Publish(&InFlightEntry{RequestID: 2<<32 | 9, ServerID: 0})
	m.Publish(&InFlightEntry{RequestID: 3<<32 | 30, ServerID: 1})
	counts := map[uint64]int{}
	for i := 0; i < 40000; i++ {
		v, ok := chooseVictim(rng, m)
		if !ok {
			t.Fatal("victim expected")
		}
		counts[v.Entry.RequestID]++
	}
	for _, id := range []uint64{17, 18, 2<<32 | 9, 3<<32 | 30} { // uniform over requests, not clients
		if counts[id] < 9000 || counts[id] > 11000 {
			t.Fatalf("not uniform over requests: %v", counts)
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

// Initial stall: the schedule is the stall-free one shifted by S and cut at D;
// S >= D sends nothing. No backends and nothing in flight: every arrival is
// skipped, which is enough to see the arrival times.
//
// Design: abstraction/ingress/load_canceller/design.md#cancelschedule
func TestCancelScheduleInitialStall(t *testing.T) {
	run := func(stallMs uint64, durationMs int64) []*CancelRecord {
		cfg := Config{CancelRate: 1000, DurationNs: durationMs * 1_000_000, InitialStallMs: stallMs,
			InitialStallNs: int64(stallMs) * 1_000_000}
		c := StartCanceller(cfg, NewInFlightMap(4), time.Now())
		time.Sleep(time.Duration(durationMs+100) * time.Millisecond)
		return c.Stop()
	}
	base, stalled := run(0, 300), run(200, 300)
	if len(stalled) == 0 || len(stalled) >= len(base) {
		t.Fatalf("records: %d with stall, %d without", len(stalled), len(base))
	}
	for i, r := range stalled {
		if r.ScheduledNs != base[i].ScheduledNs+200_000_000 || r.ScheduledNs >= 300_000_000 {
			t.Fatalf("arrival %d at %d, want %d + 200ms and < 300ms", i, r.ScheduledNs, base[i].ScheduledNs)
		}
	}
	if recs := run(300, 300); len(recs) != 0 {
		t.Fatalf("stall >= duration: %d records, want 0", len(recs))
	}
}
