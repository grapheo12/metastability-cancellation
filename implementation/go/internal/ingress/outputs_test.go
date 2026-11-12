// Source: abstraction/ingress/design.md (ingress v0.1.17)
package ingress

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// forwards.csv: t_ns from run_start, stable sort by t_ns, outcome words and
// the shortest round-trip float; header only for an empty log.
//
// Design: abstraction/ingress/design.md#shutdown
func TestWriteForwardsCSV(t *testing.T) {
	run := time.Now()
	recs := []ForwardRecord{
		{End: run.Add(5 * time.Millisecond), ServerID: 1, Outcome: OutcomeRejected, LatencyNs: 300_000, EwmaLatencyNs: 1_300_000},
		{End: run.Add(time.Millisecond), ServerID: 0, Outcome: OutcomeOK, LatencyNs: 2_000_000, EwmaLatencyNs: 1_300_000},
		{End: run.Add(5 * time.Millisecond), ServerID: 0, Outcome: OutcomeReset, LatencyNs: 7, EwmaLatencyNs: 850_000.5},
	}
	path := filepath.Join(t.TempDir(), "f.csv")
	if err := writeForwardsCSV(path, recs, run); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	want := "t_ns,server_id,outcome,latency_ns,ewma_latency_ns\n" +
		"1000000,0,ok,2000000,1300000\n" +
		"5000000,1,rejected,300000,1300000\n" + // equal t_ns: log order kept
		"5000000,0,reset,7,850000.5\n"
	if string(got) != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if err := writeForwardsCSV(path, nil, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "t_ns,server_id,outcome,latency_ns,ewma_latency_ns\n" {
		t.Fatalf("empty: %q", got)
	}
}
