// Source: abstraction/load_generator/metrics/design.md (metrics v0.4.0)
package loadgen

import (
	"testing"

	"testbed/internal/protocol"
)

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
		{Outcome: OutcomeOK, ScheduledNs: 0, SendNs: 1000, EndNs: 3000, Sent: true, Copies: 2},
		{Outcome: OutcomeRejected, Sent: true, Copies: 1},
		{Outcome: OutcomeCancelled, Sent: true, Copies: 2},
		{Outcome: OutcomeDropped, Sent: true, Copies: 2},
		{Outcome: OutcomeNotSent},
	}
	r := Aggregate(recs, Config{DurationS: 2}, 2e9, 1_700_000_000_000_000_000)
	if r.RunStartUnixNs != 1_700_000_000_000_000_000 {
		t.Fatal("run_start_unix_ns is passed through")
	}
	if r.Counts.Scheduled != 5 || r.Counts.Sent != 4 || r.Counts.OK != 1 || r.Counts.Dropped != 1 || r.Counts.CopiesSent != 7 ||
		r.RatesPerS.Offered != 2.5 || r.RatesPerS.Goodput != 0.5 {
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
		classify(500, body, 7) != OutcomeError || classify(202, body, 8) != OutcomeError ||
		classify(protocol.StatusBadGateway, nil, 7) != OutcomeError { // 502 from an Ingress
		t.Fatal("classification table")
	}
}

// StallWindowRequests count only in counts.stall_window; every other count,
// the rates (over duration_s - initial_stall_ms / 1000) and the latency
// statistics leave them out.
//
// Design: abstraction/load_generator/metrics/design.md#collection
// Design: abstraction/load_generator/metrics/design.md#summary
func TestAggregateLeavesOutStallWindow(t *testing.T) {
	recs := []RequestRecord{
		{Outcome: OutcomeOK, ScheduledNs: 0, SendNs: 0, EndNs: 900_000, Sent: true, Copies: 1, InStallWindow: true},
		{Outcome: OutcomeCancelled, Sent: true, Copies: 1, InStallWindow: true},
		{Outcome: OutcomeOK, ScheduledNs: 0, SendNs: 1000, EndNs: 3000, Sent: true, Copies: 2},
		{Outcome: OutcomeDropped, Sent: true, Copies: 2},
	}
	r := Aggregate(recs, Config{DurationS: 3, InitialStallMs: 1000}, 3e9, 0)
	c := r.Counts
	if c.StallWindow != 2 || c.Scheduled != 2 || c.Sent != 2 || c.OK != 1 || c.Cancelled != 0 || c.Dropped != 1 || c.CopiesSent != 4 {
		t.Fatalf("%+v", c)
	}
	if r.RatesPerS.Offered != 1 || r.RatesPerS.Goodput != 0.5 { // over 3 - 1 = 2 s
		t.Fatalf("%+v", r.RatesPerS)
	}
	if r.E2ELatencyUs.Count != 1 || *r.E2ELatencyUs.Max != 3 {
		t.Fatal("latency over the measured ok records only")
	}
}
