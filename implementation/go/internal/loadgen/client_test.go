// Source: abstraction/load_generator/client/design.md (client v0.4.0)
package loadgen

import (
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/http2"

	"testbed/internal/h2client"
	"testbed/internal/protocol"
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
		PerClientRate: 200, SloNs: 5_000_000_000, DurationNs: 300_000_000, RetryTimeoutNs: 10_000_000_000} // no retries
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
	p.NWaitingRequests, p.MaxRetries = "1000", "0" // the cap leaves no room for retries
	if c, err := Validate(p); err != nil || c.NWaitingRequests != 1000 {
		t.Fatalf("max: %+v %v", c, err)
	}
}

// scriptedServer is a prior-knowledge h2c server whose answer to the n-th
// copy (0-based) of a RequestId waits delays[n], or returns at once if its
// stream is reset. It reports how each copy ended on done.
type copyEnd struct {
	copy  int
	reset bool // the client reset the stream before the answer
}

func scriptedServer(t *testing.T, delays []time.Duration) (*h2client.Conn, chan copyEnd) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan copyEnd, 16)
	var mu sync.Mutex
	calls := map[uint64]int{}
	h := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		id, _ := protocol.ParseBody(b)
		mu.Lock()
		n := calls[id]
		calls[id]++
		mu.Unlock()
		select {
		case <-time.After(delays[n]):
			w.WriteHeader(protocol.StatusOK)
			w.Write(protocol.BuildBody(id, 64))
			done <- copyEnd{copy: n}
		case <-req.Context().Done():
			done <- copyEnd{copy: n, reset: true}
		}
	})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go (&http2.Server{}).ServeConn(c, &http2.ServeConnOpts{Handler: h})
		}
	}()
	conn, err := h2client.Dial(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(); ln.Close() })
	return conn, done
}

// oneRequest sends one request through startRequest and returns its record.
func oneRequest(t *testing.T, conn *h2client.Conn, sloMs, retryMs int64, maxRetries uint32) RequestRecord {
	t.Helper()
	cfg := Config{RequestBytes: 64, SloNs: sloMs * 1_000_000, RetryTimeoutNs: retryMs * 1_000_000, MaxRetries: maxRetries}
	events := make(chan clientEvent, 1)
	startRequest(3, 9, 0, conn, cfg, time.Now(), events)
	select {
	case ev := <-events:
		return ev.rec
	case <-time.After(5 * time.Second):
		t.Fatal("request never resolved")
	}
	return RequestRecord{}
}

const ms = time.Millisecond

// The RetryTimer worked examples (RETRY = 150, SLO = 300, R = 1 unless stated),
// with times scaled down where the case allows.
//
// Design: abstraction/load_generator/client/design.md#retrytimer
func TestRetryTimerWorkedExamples(t *testing.T) {
	service := func(r RequestRecord) time.Duration { return time.Duration(r.EndNs - r.SendNs) }
	t.Run("fast answer", func(t *testing.T) {
		conn, _ := scriptedServer(t, []time.Duration{10 * ms, 10 * ms})
		r := oneRequest(t, conn, 300, 150, 1)
		if r.Outcome != OutcomeOK || r.Copies != 1 || service(r) >= 150*ms {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("retry answered", func(t *testing.T) {
		conn, _ := scriptedServer(t, []time.Duration{time.Second, 10 * ms})
		r := oneRequest(t, conn, 300, 100, 1)
		if r.Outcome != OutcomeOK || r.Copies != 2 || service(r) < 100*ms || service(r) >= 300*ms {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("late original ignored", func(t *testing.T) {
		// copy 1 sent at 100; copy 0 answers at 130 (ignored); copy 1 answers at 100+60,
		// before its own RetryTimer (200).
		conn, done := scriptedServer(t, []time.Duration{130 * ms, 60 * ms})
		r := oneRequest(t, conn, 1000, 100, 1)
		if r.Outcome != OutcomeOK || r.Copies != 2 || service(r) < 155*ms {
			t.Fatalf("%+v: the superseded copy's answer must not count", r)
		}
		if e := <-done; e.copy != 0 || e.reset {
			t.Fatalf("copy 0 must be answered, not reset: %+v", e)
		}
	})
	t.Run("no answer: SLO resets every copy", func(t *testing.T) {
		conn, done := scriptedServer(t, []time.Duration{time.Hour, time.Hour})
		r := oneRequest(t, conn, 150, 100, 1)
		if r.Outcome != OutcomeCancelled || r.Copies != 2 || service(r) < 150*ms {
			t.Fatalf("%+v", r)
		}
		for i := 0; i < 2; i++ {
			select {
			case e := <-done:
				if !e.reset {
					t.Fatalf("%+v", e)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("both copies must be reset")
			}
		}
	})
	t.Run("dropped after the last copy, no reset", func(t *testing.T) {
		conn, done := scriptedServer(t, []time.Duration{300 * ms, 300 * ms})
		r := oneRequest(t, conn, 5000, 50, 1)
		if r.Outcome != OutcomeDropped || r.Copies != 2 || r.HTTPStatus != 0 || service(r) < 100*ms || service(r) >= 300*ms {
			t.Fatalf("%+v", r)
		}
		for i := 0; i < 2; i++ {
			if e := <-done; e.reset {
				t.Fatalf("a dropped request's copies are not reset: %+v", e)
			}
		}
	})
	t.Run("R = 0 drops the only copy", func(t *testing.T) {
		conn, _ := scriptedServer(t, []time.Duration{300 * ms})
		r := oneRequest(t, conn, 5000, 50, 0)
		if r.Outcome != OutcomeDropped || r.Copies != 1 || service(r) < 50*ms || service(r) >= 300*ms {
			t.Fatalf("%+v", r)
		}
	})
}

// retry_timeout_ms > 0 (default 150); max_retries >= 0 (default 1) and
// n_waiting_requests × (1 + max_retries) <= 1000, with the worked examples.
//
// Design: abstraction/load_generator/run_controller/design.md#configuration-validation
func TestValidateRetryParameters(t *testing.T) {
	p := DefaultParams()
	p.NumClients, p.GlobalRate, p.RequestBytes, p.SloMs, p.DurationS = "2", "10", "64", "100", "1"
	if c, err := Validate(p); err != nil || c.RetryTimeoutNs != 150_000_000 || c.MaxRetries != 1 {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	for _, tc := range []struct {
		waiting, retries string
		ok               bool
	}{{"100", "1", true}, {"500", "1", true}, {"1000", "1", false}, {"1", "999", true}, {"1", "1000", false},
		{"1000", "0", true}, {"1", "-1", false}, {"1", "x", false}} {
		p.NWaitingRequests, p.MaxRetries = tc.waiting, tc.retries
		if _, err := Validate(p); (err == nil) != tc.ok {
			t.Fatalf("n_waiting_requests=%s max_retries=%s: err=%v", tc.waiting, tc.retries, err)
		}
	}
	p.NWaitingRequests, p.MaxRetries = "100", "1"
	for _, bad := range []string{"0", "-5", "x", ""} {
		p.RetryTimeoutMs = bad
		if _, err := Validate(p); err == nil {
			t.Fatalf("retry_timeout_ms %q must be rejected", bad)
		}
	}
}
