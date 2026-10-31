// Source: abstraction/server/design.md (server v0.2.1)
package server

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"golang.org/x/net/http2"

	"testbed/internal/protocol"
)

type testClient struct {
	cc   *http2.ClientConn
	addr string
}

func dialTest(t *testing.T, addr string) *testClient {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	cc, err := (&http2.Transport{AllowHTTP: true}).NewClientConn(conn)
	if err != nil {
		t.Fatal(err)
	}
	if err := cc.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &testClient{cc: cc, addr: addr}
}

func (c *testClient) do(t *testing.T, method, path string, body []byte) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, "http://"+c.addr+path, bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	resp, err := c.cc.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func startTestServer(t *testing.T, mode CancelMode, rate float64) *Server {
	t.Helper()
	s, err := Listen(Config{ListenAddr: "127.0.0.1:0", ResponseRate: rate, ResponseBytes: 64, CancelMode: mode, CancelCheckIntervalUs: 50})
	if err != nil {
		t.Fatal(err)
	}
	go s.Serve()
	return s
}

// End to end: binding, a /cancel registered before processing gives 503 and
// counts cancelled_before_processing; routing errors give 404/405/400.
//
// Design: abstraction/server/design.md#cancellation-semantics
func TestCancelBeforeProcessing(t *testing.T) {
	s := startTestServer(t, CancelCooperative, 100000)
	addr := s.Addr().String()
	client, canceller := dialTest(t, addr), dialTest(t, addr)

	id0, id1 := protocol.MakeRequestID(0, 5), protocol.MakeRequestID(1, 5)
	if st, b := client.do(t, "POST", "/ping", protocol.BuildBody(id0, 64)); st != 202 || len(b) != 64 {
		t.Fatalf("first ping: %d len %d", st, len(b))
	}
	if st, _ := canceller.do(t, "DELETE", "/cancel", protocol.BuildCancelBody(id1)); st != 202 {
		t.Fatalf("cancel: %d", st)
	}
	st, b := client.do(t, "POST", "/ping", protocol.BuildBody(id1, 64))
	if got, _ := protocol.ParseBody(b); st != 503 || len(b) != 64 || got != id1 {
		t.Fatalf("cancelled ping: %d %s", st, b)
	}
	if st, _ := client.do(t, "POST", "/ping", protocol.BuildBody(protocol.MakeRequestID(2, 6), 64)); st != 400 {
		t.Fatalf("client id mismatch: %d", st)
	}
	if st, _ := client.do(t, "GET", "/ping", nil); st != 405 {
		t.Fatalf("GET /ping: %d", st)
	}
	if st, _ := client.do(t, "POST", "/other", nil); st != 404 {
		t.Fatalf("/other: %d", st)
	}
	if b, d, c := s.Counters().Snapshot(); b != 1 || d != 0 || c != 0 {
		t.Fatalf("counters %d %d %d", b, d, c)
	}
	s.Shutdown()
}

// A /cancel arriving during a long burn stops it with 503.
//
// Design: abstraction/server/cpu_burner/design.md#cooperativecpuburn
func TestCancelDuringProcessing(t *testing.T) {
	s := startTestServer(t, CancelCooperative, 0.0001) // mean ServiceTime ~ hours
	addr := s.Addr().String()
	client, canceller := dialTest(t, addr), dialTest(t, addr)
	id := protocol.MakeRequestID(0, 1)
	go func() {
		time.Sleep(100 * time.Millisecond)
		canceller.do(t, "DELETE", "/cancel", protocol.BuildCancelBody(id))
	}()
	start := time.Now()
	if st, _ := client.do(t, "POST", "/ping", protocol.BuildBody(id, 64)); st != 503 {
		t.Fatalf("got %d", st)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("burn did not stop promptly")
	}
	if _, d, _ := s.Counters().Snapshot(); d != 1 {
		t.Fatalf("cancelled_during_processing %d", d)
	}
	s.Shutdown()
}

// A client-side context cancel (RST_STREAM) stops a cooperative burn.
//
// Design: abstraction/server/connection_handler/design.md#framereader
func TestRSTStopsBurn(t *testing.T) {
	s := startTestServer(t, CancelCooperative, 0.0001)
	client := dialTest(t, s.Addr().String())
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", "http://"+client.addr+"/ping", bytes.NewReader(protocol.BuildBody(1, 64)))
	if _, err := client.cc.RoundTrip(req); err == nil {
		t.Fatal("expected cancellation error")
	}
	// The worker must be free again: a fresh request with a tiny ServiceTime is not
	// possible at this rate, so instead check that shutdown returns promptly.
	done := make(chan struct{})
	go func() { s.Shutdown(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("burn was not stopped by RST")
	}
}
