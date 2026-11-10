// Source: abstraction/load_generator/client/design.md (client v0.3.0)
package loadgen

import (
	"testing"
	"time"

	"testbed/internal/h2client"
	"testbed/internal/sampling"
	"testbed/internal/server"
)

// runAgainstStalledServer runs one client for 300 ms against a one-worker
// server whose StallGate holds every request for 200 ms, so requests pile up.
func runAgainstStalledServer(t *testing.T, waiting uint32) []RequestRecord {
	t.Helper()
	s, err := server.Listen(server.Config{ListenAddr: "127.0.0.1:0", ResponseRate: 100000, ResponseBytes: 64,
		CancelMode: server.CancelCooperative, CancelCheckIntervalUs: 50, NWorkerThreads: 1, InitialStallMs: 200})
	if err != nil {
		t.Fatal(err)
	}
	go s.Serve()
	defer s.Shutdown()
	conn, err := h2client.Dial(s.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cfg := Config{NumClients: 1, GlobalRate: 200, RequestBytes: 64, SloMs: 5000, DurationS: 0.3, NWaitingRequests: waiting,
		PerClientRate: 200, SloNs: 5_000_000_000, DurationNs: 300_000_000}
	return RunClient(0, conn, cfg, sampling.NewRng(7), time.Now())
}

// With a cap above 1, the sender keeps sending while earlier requests are
// still outstanding; never more than the cap at once; records come back in
// seq order, all resolved.
//
// Design: abstraction/load_generator/client/design.md#semiopenloopclient
func TestSemiOpenLoopOverlaps(t *testing.T) {
	recs := runAgainstStalledServer(t, 10)
	maxOut := 0
	for i, r := range recs {
		if uint32(i) != r.Seq {
			t.Fatalf("records not in seq order at %d", i)
		}
		if r.Outcome != OutcomeOK && r.Outcome != OutcomeNotSent {
			t.Fatalf("seq %d: %s", r.Seq, r.Outcome)
		}
		if !r.Sent {
			continue
		}
		out := 0 // requests in flight at r's send
		for _, o := range recs {
			if o.Sent && o.SendNs <= r.SendNs && o.EndNs > r.SendNs {
				out++
			}
		}
		if out > maxOut {
			maxOut = out
		}
	}
	if maxOut < 2 || maxOut > 10 {
		t.Fatalf("max outstanding %d, want 2..=10", maxOut)
	}
}

// With a cap of 1 the client is the old closed loop: each send waits for the
// previous request to resolve (GatedArrival).
//
// Design: abstraction/load_generator/client/design.md#gatedarrival
func TestCapOneIsClosedLoop(t *testing.T) {
	recs := runAgainstStalledServer(t, 1)
	var prevEnd int64 = -1
	for _, r := range recs {
		if !r.Sent {
			continue
		}
		if r.SendNs < prevEnd {
			t.Fatalf("seq %d sent at %d before the previous request ended at %d", r.Seq, r.SendNs, prevEnd)
		}
		prevEnd = r.EndNs
	}
	if prevEnd < 0 {
		t.Fatal("nothing was sent")
	}
}

// n_waiting_requests: default 100, range 1..=MAX_WAITING_REQUESTS.
//
// Design: abstraction/load_generator/run_controller/design.md#configuration-validation
func TestValidateNWaitingRequests(t *testing.T) {
	p := DefaultParams()
	p.NumClients, p.GlobalRate, p.RequestBytes, p.SloMs, p.DurationS = "2", "10", "64", "100", "1"
	if c, err := Validate(p); err != nil || c.NWaitingRequests != 100 {
		t.Fatalf("default: %+v %v", c, err)
	}
	for _, bad := range []string{"0", "1001", "x", "-1"} {
		p.NWaitingRequests = bad
		if _, err := Validate(p); err == nil {
			t.Fatalf("%q must be rejected", bad)
		}
	}
	p.NWaitingRequests = "1000"
	if c, err := Validate(p); err != nil || c.NWaitingRequests != 1000 {
		t.Fatalf("max: %+v %v", c, err)
	}
}
