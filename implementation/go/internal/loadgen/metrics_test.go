// Source: abstraction/load_generator/metrics/design.md (metrics v0.1.1)
package loadgen

import "testing"

// Design: abstraction/load_generator/metrics/design.md#percentiles
func TestPercentileWorkedExample(t *testing.T) {
	v := []float64{9, 8, 7, 6, 5, 4, 3, 2, 1, 0} // sorted: v[k] = k
	s := latencyStats(v)
	if *s.P50 != 4 || *s.P90 != 8 || *s.P99 != 9 || *s.P999 != 9 || *s.Max != 9 || *s.Mean != 4.5 {
		t.Fatalf("got p50=%v p90=%v p99=%v p999=%v", *s.P50, *s.P90, *s.P99, *s.P999)
	}
	if e := latencyStats(nil); e.Count != 0 || e.P50 != nil {
		t.Fatal("empty set must give nulls")
	}
}

// Design: abstraction/load_generator/metrics/design.md#collection
func TestAggregateCounts(t *testing.T) {
	recs := []RequestRecord{
		{Outcome: OutcomeOK, ScheduledNs: 0, SendNs: 1000, EndNs: 3000, Sent: true},
		{Outcome: OutcomeRejected, Sent: true},
		{Outcome: OutcomeCancelled, Sent: true},
		{Outcome: OutcomeNotSent},
	}
	r := Aggregate(recs, Config{DurationS: 2}, 2e9)
	if r.Counts.Scheduled != 4 || r.Counts.Sent != 3 || r.Counts.OK != 1 || r.RatesPerS.Offered != 2 || r.RatesPerS.Goodput != 0.5 {
		t.Fatalf("%+v", r.Counts)
	}
	if *r.E2ELatencyUs.P50 != 3 || *r.ServiceLatencyUs.P50 != 2 {
		t.Fatal("latencies in µs over ok records")
	}
}

// Design: abstraction/load_generator/client/design.md#outcome-classification
func TestClassify(t *testing.T) {
	body := []byte(`{"id":"7","payload":""}`)
	if classify(202, body, 7) != OutcomeOK || classify(503, body, 7) != OutcomeRejected ||
		classify(500, body, 7) != OutcomeError || classify(202, body, 8) != OutcomeError {
		t.Fatal("classification table")
	}
}
