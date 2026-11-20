// Source: abstraction/server/heartbeat/design.md (heartbeat v0.1.0)
package server

import (
	"bufio"
	"net/http"
	"testing"
	"time"

	"testbed/internal/protocol"
)

// readLines reads HeartbeatLines from body into a channel of arrival times.
func readLines(body *bufio.Reader, out chan<- time.Time) {
	for {
		line, err := body.ReadBytes('\n')
		if err != nil {
			close(out)
			return
		}
		if _, err := protocol.ParseHeartbeatLine(line[:len(line)-1]); err == nil {
			out <- time.Now()
		}
	}
}

// End to end: GET /heartbeat gets 200 text/plain and a line about every
// heartbeat_interval_ms; once the WorkerFreeze starts no line follows;
// Shutdown ends the stream. Other methods get 405.
//
// Design: abstraction/server/heartbeat/design.md#heartbeatsender
// Design: abstraction/server/connection_handler/design.md#routing
func TestHeartbeatStreamAndFreeze(t *testing.T) {
	s, err := Listen(Config{ListenAddr: "127.0.0.1:0", ResponseRate: 1000, ResponseBytes: 128,
		CancelMode: CancelCooperative, CancelCheckIntervalUs: 50, NWorkerThreads: 1,
		HeartbeatIntervalMs: 100, FreezeAfterMs: 400})
	if err != nil {
		t.Fatal(err)
	}
	go s.Serve()
	c := dialTest(t, s.Addr().String())
	if st, _ := c.do(t, "POST", "/heartbeat", nil); st != 405 {
		t.Fatalf("POST /heartbeat: %d, want 405", st)
	}
	req, _ := http.NewRequest("GET", "http://"+c.addr+"/heartbeat", nil)
	resp, err := c.cc.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || resp.Header.Get("content-type") != "text/plain" {
		t.Fatalf("got %d %q", resp.StatusCode, resp.Header.Get("content-type"))
	}
	lines := make(chan time.Time, 100)
	go readLines(bufio.NewReader(resp.Body), lines)
	// Before any /ping the queue is not armed, so no freeze: lines keep coming.
	start := time.Now()
	n := 0
	for time.Since(start) < 350*time.Millisecond {
		select {
		case <-lines:
			n++
		case <-time.After(400 * time.Millisecond):
			t.Fatal("no heartbeat")
		}
	}
	if n < 3 || n > 6 {
		t.Fatalf("%d lines in ~350 ms at 100 ms", n)
	}
	// Arm the freeze with one /ping: it freezes 400 ms later.
	if st, _ := c.do(t, "POST", "/ping", protocol.BuildBody(protocol.MakeRequestID(0, 1), 0, nil, 128)); st != 202 {
		t.Fatalf("ping: %d", st)
	}
	armed := time.Now()
	var last time.Time
	deadline := time.After(1200 * time.Millisecond)
loop:
	for {
		select {
		case at, ok := <-lines:
			if !ok {
				t.Fatal("stream ended early")
			}
			last = at
		case <-deadline:
			break loop
		}
	}
	if d := last.Sub(armed); d > 550*time.Millisecond {
		t.Fatalf("heartbeat %v after arming; the freeze is at 400 ms", d)
	}
	done := make(chan struct{})
	go func() { s.Shutdown(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown blocked by a frozen heartbeat")
	}
}

// heartbeat_interval_ms: integer >= 1, default 500.
//
// Design: abstraction/server/design.md#configuration-validation
func TestValidateHeartbeatInterval(t *testing.T) {
	p := DefaultParams()
	p.ResponseRate, p.ResponseBytes = "100", "128"
	if c, err := Validate(p); err != nil || c.HeartbeatIntervalMs != 500 {
		t.Fatalf("default: %+v %v", c, err)
	}
	for _, bad := range []string{"0", "-1", "x"} {
		p.HeartbeatIntervalMs = bad
		if _, err := Validate(p); err == nil || err.Error() != "heartbeat_interval_ms must be an integer >= 1" {
			t.Fatalf("%q: %v", bad, err)
		}
	}
}
