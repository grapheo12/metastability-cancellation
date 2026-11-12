// Source: abstraction/server/design.md (server v0.7.0)
package server

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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
	return startTestServerN(t, mode, rate, 1)
}

func startTestServerN(t *testing.T, mode CancelMode, rate float64, workers uint32) *Server {
	t.Helper()
	s, err := Listen(Config{ListenAddr: "127.0.0.1:0", ResponseRate: rate, ResponseBytes: 64, CancelMode: mode, CancelCheckIntervalUs: 50, NWorkerThreads: workers})
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

// /cancel replies report the FinishedWatermark after a 202, a 503 and an RST abort.
//
// Design: abstraction/server/ping_handler/design.md#finishedwatermark
func TestFinishedWatermarkReplies(t *testing.T) {
	s := startTestServer(t, CancelCooperative, 100000)
	addr := s.Addr().String()
	client, canceller := dialTest(t, addr), dialTest(t, addr)
	finished := func(target uint64) (uint64, bool) {
		st, b := canceller.do(t, "DELETE", "/cancel", protocol.BuildCancelBody(target))
		f, ok, err := protocol.ParseCancelReply(b)
		if st != 202 || err != nil {
			t.Fatalf("cancel reply %d %q %v", st, b, err)
		}
		return f, ok
	}
	if _, ok := finished(protocol.MakeRequestID(9, 4)); ok {
		t.Fatal("before any /ping: want null")
	}
	id0, id1 := protocol.MakeRequestID(0, 4), protocol.MakeRequestID(1, 4)
	if st, _ := client.do(t, "POST", "/ping", protocol.BuildBody(id0, 64)); st != 202 {
		t.Fatalf("ping 0: %d", st)
	}
	if f, ok := finished(id1); !ok || f != id0 { // also registers a cancel for id1
		t.Fatalf("after 202: %d %v", f, ok)
	}
	if st, _ := client.do(t, "POST", "/ping", protocol.BuildBody(id1, 64)); st != 503 {
		t.Fatalf("ping 1: %d", st)
	}
	if f, ok := finished(protocol.MakeRequestID(99, 4)); !ok || f != id1 {
		t.Fatalf("after 503: %d %v", f, ok)
	}
	s.Shutdown()

	// RST: a long burn stopped by the client's reset still publishes its id.
	s = startTestServer(t, CancelCooperative, 0.0001)
	addr = s.Addr().String()
	client, canceller = dialTest(t, addr), dialTest(t, addr)
	id := protocol.MakeRequestID(0, 7)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", "http://"+addr+"/ping", bytes.NewReader(protocol.BuildBody(id, 64)))
	if _, err := client.cc.RoundTrip(req); err == nil {
		t.Fatal("expected cancellation error")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if f, ok := finished(protocol.MakeRequestID(5, 7)); ok && f == id {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("RST-stopped request never published")
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.Shutdown()
}

// With n_worker_threads = 2, a second client is served while the first one's
// long burn occupies a worker, and /cancel is answered while every worker is
// busy (it is never queued).
//
// Design: abstraction/server/design.md#concurrency-model
func TestTwoWorkersServeInParallel(t *testing.T) {
	s := startTestServerN(t, CancelCooperative, 0.0001, 2) // mean ServiceTime ~ hours
	addr := s.Addr().String()
	slow, slow2, canceller := dialTest(t, addr), dialTest(t, addr), dialTest(t, addr)
	idA, idB := protocol.MakeRequestID(1, 0), protocol.MakeRequestID(1, 1) // clients 0 and 1
	resA, resB := make(chan int, 1), make(chan int, 1)
	go func() { st, _ := slow.do(t, "POST", "/ping", protocol.BuildBody(idA, 64)); resA <- st }()
	go func() { st, _ := slow2.do(t, "POST", "/ping", protocol.BuildBody(idB, 64)); resB <- st }()
	time.Sleep(100 * time.Millisecond) // both workers are now burning

	start := time.Now()
	if st, _ := canceller.do(t, "DELETE", "/cancel", protocol.BuildCancelBody(idA)); st != 202 {
		t.Fatalf("cancel A: %d", st)
	}
	if time.Since(start) > time.Second {
		t.Fatal("/cancel waited for a worker")
	}
	select {
	case st := <-resA:
		if st != 503 {
			t.Fatalf("A: %d", st)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("A was not stopped")
	}
	if st, _ := canceller.do(t, "DELETE", "/cancel", protocol.BuildCancelBody(idB)); st != 202 {
		t.Fatalf("cancel B: %d", st)
	}
	select {
	case st := <-resB:
		if st != 503 {
			t.Fatalf("B: %d", st)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("B was not stopped")
	}
	if _, d, _ := s.Counters().Snapshot(); d != 2 {
		t.Fatalf("cancelled_during_processing %d, want 2", d)
	}
	s.Shutdown()
}

// Configuration validation of n_worker_threads.
//
// Design: abstraction/server/design.md#configuration-validation
func TestValidateNWorkerThreads(t *testing.T) {
	p := DefaultParams()
	p.ResponseRate, p.ResponseBytes = "10", "64"
	if c, err := Validate(p); err != nil || c.NWorkerThreads != 1 {
		t.Fatalf("default: %+v %v", c, err)
	}
	for _, bad := range []string{"0", "-1", "x", "4294967296"} {
		p.NWorkerThreads = bad
		if _, err := Validate(p); err == nil {
			t.Fatalf("n_worker_threads %q must be rejected", bad)
		}
	}
	p.NWorkerThreads = "1"
	p.QueueSampleIntervalMs = "0"
	if _, err := Validate(p); err == nil {
		t.Fatal("queue_sample_interval_ms 0 must be rejected")
	}
	p.QueueSampleIntervalMs = "10"
	p.NWorkerThreads = "4294967295"
	if c, err := Validate(p); err != nil || c.NWorkerThreads != 4294967295 {
		t.Fatalf("max: %+v %v", c, err)
	}
}

// workload is validated; response_rate is optional for message_buffer.
//
// Design: abstraction/server/design.md#configuration-validation
func TestValidateWorkload(t *testing.T) {
	p := DefaultParams()
	p.ResponseBytes = "64"
	if _, err := Validate(p); err == nil {
		t.Fatal("cpu_burn needs response_rate")
	}
	p.Workload = "message_buffer"
	c, err := Validate(p)
	if err != nil || c.Workload != WorkloadMessageBuffer || c.WindowSize != 200000 || c.MessageBytes != 1024 ||
		c.NMessagesPerRequest != 50 {
		t.Fatalf("message_buffer defaults: %+v %v", c, err)
	}
	for _, bad := range []func(*Params){
		func(q *Params) { q.Workload = "gc" },
		func(q *Params) { q.ResponseRate = "0" }, // validated when given
		func(q *Params) { q.WindowSize = "0" },
		func(q *Params) { q.WindowSize = "4294967296" },
		func(q *Params) { q.MessageBytes = "0" },
		func(q *Params) { q.NMessagesPerRequest = "0" },
	} {
		q := p
		bad(&q)
		if _, err := Validate(q); err == nil {
			t.Fatalf("must be rejected: %+v", q)
		}
	}
}

// End to end with workload = message_buffer: the window is full before the
// server listens, each /ping writes its messages at its client's cursor, and
// no ServiceTime is drawn.
//
// Design: abstraction/server/ping_handler/design.md#messagework
// Design: abstraction/server/design.md#listener
func TestMessageBufferEndToEnd(t *testing.T) {
	s, err := Listen(Config{ListenAddr: "127.0.0.1:0", ResponseBytes: 64, CancelMode: CancelCooperative,
		CancelCheckIntervalUs: 50, NWorkerThreads: 2, Workload: WorkloadMessageBuffer, WindowSize: 100,
		MessageBytes: 16, NMessagesPerRequest: 5})
	if err != nil {
		t.Fatal(err)
	}
	if s.buf == nil || len(s.buf.slots) != 100 || s.buf.slots[99][0] != 99 {
		t.Fatal("window not filled at Listen")
	}
	go s.Serve()
	c := dialTest(t, s.Addr().String())
	for seq := uint32(0); seq < 3; seq++ {
		id := protocol.MakeRequestID(seq, 1)
		if st, b := c.do(t, "POST", "/ping", protocol.BuildBody(id, 64)); st != 202 || len(b) != 64 {
			t.Fatalf("ping %d: %d", seq, st)
		}
	}
	s.Shutdown()
	// client 1 starts at 7919 mod 100 = 19; 3 requests x 5 messages fill 19..33
	for i := 0; i < 15; i++ {
		id := protocol.MakeRequestID(uint32(i/5), 1)
		if got, want := s.buf.slots[19+i][0], byte(id+uint64(i%5)); got != want || len(s.buf.slots[19+i]) != 16 {
			t.Fatalf("slot %d = %d want %d", 19+i, got, want)
		}
	}
	if s.buf.cursors[1] != 34 || s.buf.slots[18][0] != 18 || s.buf.slots[34][0] != 34 {
		t.Fatalf("cursor %d", s.buf.cursors[1])
	}
}

// MessageWork checks after each message; a cancelled request never writes.
//
// Design: abstraction/server/ping_handler/design.md#messagework
// Design: abstraction/server/ping_handler/design.md#cancelcheckpoints
func TestFillMessagesCancel(t *testing.T) {
	cfg := Config{MessageBytes: 4, NMessagesPerRequest: 3}
	job := &PingJob{RequestID: protocol.MakeRequestID(5, 2), ClientID: 2}
	buf := NewMessageBuffer(10, 4)
	calls := 0
	idCheck := func() bool { calls++; return calls == 2 } // registered before the second check
	var rst atomic.Bool
	if got := fillMessages(job, cfg, buf, &rst, idCheck); got != BurnCancelledID || calls != 2 {
		t.Fatalf("cancel: %v after %d checks", got, calls)
	}
	rst.Store(true)
	if got := fillMessages(job, cfg, buf, &rst, func() bool { t.Fatal("RST is checked first"); return false }); got != BurnCancelledRST {
		t.Fatalf("rst: %v", got)
	}
	if len(buf.cursors) != 0 {
		t.Fatal("a cancelled request wrote the buffer")
	}
	if got := fillMessages(job, cfg, buf, nil, nil); got != BurnCompleted {
		t.Fatalf("ignore mode: %v", got)
	}
	// client 2 starts at 2*7919 mod 10 = 8: slots 8, 9, 0 get bytes 05, 06, 07
	if buf.slots[8][0] != 5 || buf.slots[9][0] != 6 || buf.slots[0][0] != 7 || buf.cursors[2] != 1 {
		t.Fatalf("slots %v %v %v cursor %d", buf.slots[8], buf.slots[9], buf.slots[0], buf.cursors[2])
	}
}

// End to end: with one worker busy on a long burn, later requests wait in the
// WorkQueue and the QueueLengthLog shows them; the file is complete after
// Shutdown, and an unwritable path makes Listen fail.
//
// Design: abstraction/server/design.md#listener
func TestQueueLengthLogEndToEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server_0.queue.csv")
	s, err := Listen(Config{ListenAddr: "127.0.0.1:0", ResponseRate: 0.0001, ResponseBytes: 64,
		CancelMode: CancelCooperative, CancelCheckIntervalUs: 50, NWorkerThreads: 1,
		QueueLogPath: path, QueueSampleIntervalMs: 5})
	if err != nil {
		t.Fatal(err)
	}
	go s.Serve()
	addr := s.Addr().String()
	ctx, cancel := context.WithCancel(context.Background())
	for c := uint32(0); c < 3; c++ { // three clients; one burns, two wait
		client := dialTest(t, addr)
		req, _ := http.NewRequestWithContext(ctx, "POST", "http://"+addr+"/ping",
			bytes.NewReader(protocol.BuildBody(protocol.MakeRequestID(0, c), 64)))
		go func() { _, _ = client.cc.RoundTrip(req) }()
	}
	time.Sleep(150 * time.Millisecond)
	cancel() // RST all three so shutdown is prompt
	s.Shutdown()
	b, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(b), "t_ms,queue_len\n") || !strings.Contains(string(b), ",2\n") {
		t.Fatalf("queue log does not show 2 waiting jobs:\n%s", b)
	}
	if _, err := Listen(Config{ListenAddr: "127.0.0.1:0", ResponseRate: 1, ResponseBytes: 64, NWorkerThreads: 1,
		QueueLogPath: filepath.Join(dir, "missing", "q.csv"), QueueSampleIntervalMs: 10}); err == nil {
		t.Fatal("unwritable queue_log_path must fail Listen")
	}
}

// End to end: /cancel does not arm the StallGate; the first /ping does, and is
// answered only after the stall. A /cancel registered during the stall is
// caught at CancelCheckPoint 1.
//
// Design: abstraction/server/worker_pool/design.md#stallgate
func TestInitialStallEndToEnd(t *testing.T) {
	s, err := Listen(Config{ListenAddr: "127.0.0.1:0", ResponseRate: 100000, ResponseBytes: 64,
		CancelMode: CancelCooperative, CancelCheckIntervalUs: 50, NWorkerThreads: 1, InitialStallMs: 300})
	if err != nil {
		t.Fatal(err)
	}
	go s.Serve()
	addr := s.Addr().String()
	a, b, canceller := dialTest(t, addr), dialTest(t, addr), dialTest(t, addr)
	if st, _ := canceller.do(t, "DELETE", "/cancel", protocol.BuildCancelBody(protocol.MakeRequestID(9, 9))); st != 202 {
		t.Fatalf("cancel before any ping: %d", st)
	}
	time.Sleep(200 * time.Millisecond) // must not count towards the stall
	idA, idB := protocol.MakeRequestID(0, 0), protocol.MakeRequestID(0, 1)
	resB := make(chan int, 1)
	start := time.Now()
	go func() { st, _ := b.do(t, "POST", "/ping", protocol.BuildBody(idB, 64)); resB <- st }()
	time.Sleep(50 * time.Millisecond)
	if st, _ := canceller.do(t, "DELETE", "/cancel", protocol.BuildCancelBody(idB)); st != 202 {
		t.Fatalf("cancel during stall: %d", st)
	}
	if st, _ := a.do(t, "POST", "/ping", protocol.BuildBody(idA, 64)); st != 202 {
		t.Fatalf("A: %d", st)
	}
	if waited := time.Since(start); waited < 300*time.Millisecond {
		t.Fatalf("answered after %v, before the 300ms stall", waited)
	}
	if st := <-resB; st != 503 {
		t.Fatalf("B cancelled during the stall: %d, want 503", st)
	}
	if bp, _, _ := s.Counters().Snapshot(); bp != 1 {
		t.Fatalf("cancelled_before_processing %d, want 1", bp)
	}
	s.Shutdown()
}

// Design: abstraction/server/design.md#processstart
func TestProcessStartLine(t *testing.T) {
	if got := ProcessStartLine(time.Unix(1700000000, 12)); got != "process_start_unix_ns 1700000000000000012" {
		t.Fatalf("got %q", got)
	}
}
