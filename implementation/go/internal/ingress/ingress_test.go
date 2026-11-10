// Source: abstraction/ingress/frontend/design.md (frontend v0.1.0)
package ingress

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"testbed/internal/h2client"
	"testbed/internal/protocol"
	"testbed/internal/server"
)

func startIngress(t *testing.T, backends []string, policy string, cancelRate float64) *Ingress {
	t.Helper()
	cfg, err := Validate(Params{ListenAddr: "127.0.0.1:0", Backends: joinComma(backends), NumClients: "4",
		LbPolicy: policy, CancelRate: ftoa(cancelRate), DurationS: "5", Seed: "1", InitialStallMs: "0",
		OutputPath: filepath.Join(t.TempDir(), "ingress")})
	if err != nil {
		t.Fatal(err)
	}
	ing, err := Listen(cfg)
	if err != nil {
		t.Fatal(err)
	}
	go ing.Serve()
	return ing
}

func joinComma(s []string) string {
	out := ""
	for i, x := range s {
		if i > 0 {
			out += ","
		}
		out += x
	}
	return out
}

func ftoa(f float64) string { b, _ := json.Marshal(f); return string(b) }

func ping(t *testing.T, ctx context.Context, c *h2client.Conn, method, path string, body []byte) (int, []byte, error) {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, method, "http://"+c.Authority+path, bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	resp, err := c.CC.RoundTrip(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp.StatusCode, b, err
}

func dial(t *testing.T, ing *Ingress) *h2client.Conn {
	t.Helper()
	c, err := h2client.Dial(ing.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Relay, routing errors, round-robin spread over two backends, and outputs.
//
// Design: abstraction/ingress/frontend/design.md#forwardtask
// Design: abstraction/ingress/design.md#shutdown
func TestForwardRelayAndSpread(t *testing.T) {
	b0, b1 := startBackend(t, 100000), startBackend(t, 100000)
	ing := startIngress(t, []string{b0.Addr().String(), b1.Addr().String()}, PolicyRoundRobin, 0)
	ctx := context.Background()
	for c := uint32(0); c < 4; c++ {
		conn := dial(t, ing)
		for seq := uint32(0); seq < 3; seq++ {
			id := protocol.MakeRequestID(seq, c)
			st, b, err := ping(t, ctx, conn, "POST", "/ping", protocol.BuildBody(id, 64))
			if got, _ := protocol.ParseBody(b); err != nil || st != 202 || len(b) != 64 || got != id {
				t.Fatalf("client %d seq %d: %d %v", c, seq, st, err)
			}
		}
		if st, _, _ := ping(t, ctx, conn, "POST", "/ping", protocol.BuildBody(protocol.MakeRequestID(0, 9), 64)); st != 400 {
			t.Fatalf("client id >= N: %d", st)
		}
		if st, _, _ := ping(t, ctx, conn, "GET", "/ping", nil); st != 405 {
			t.Fatalf("GET: %d", st)
		}
		if st, _, _ := ping(t, ctx, conn, "DELETE", "/cancel", nil); st != 404 {
			t.Fatalf("/cancel: %d", st)
		}
	}
	if err := ing.Shutdown(); err != nil {
		t.Fatal(err)
	}
	var s Summary
	raw, _ := os.ReadFile(ing.cfg.OutputPath + ".summary.json")
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	if len(s.Backends) != 2 || s.Backends[0].Forwarded != 6 || s.Backends[1].Forwarded != 6 ||
		s.Backends[0].OK+s.Backends[1].OK != 12 || s.Counts.Scheduled != 0 || s.RunEndS <= 0 {
		t.Fatalf("summary %+v", s)
	}
	if _, err := os.Stat(ing.cfg.OutputPath + ".cancels.csv"); err != nil {
		t.Fatal("cancels.csv must exist even with the canceller off")
	}
	b0.Shutdown()
	b1.Shutdown()
}

// The in-ingress canceller finds the in-flight request and its backend
// answers 503; a long burn stopped this way is counted cancelled_during.
//
// Design: abstraction/ingress/load_canceller/design.md#inflightvictimchoice
// Design: abstraction/ingress/frontend/design.md#startonce
func TestCancelReachesBackend(t *testing.T) {
	b0, b1 := startBackend(t, 0.0001), startBackend(t, 0.0001) // bursts of hours: only a cancel ends them
	ing := startIngress(t, []string{b0.Addr().String(), b1.Addr().String()}, PolicyLeastOutstanding, 200)
	conn := dial(t, ing)
	id := protocol.MakeRequestID(0, 2)
	start := time.Now()
	st, b, err := ping(t, context.Background(), conn, "POST", "/ping", protocol.BuildBody(id, 64))
	if got, _ := protocol.ParseBody(b); err != nil || st != 503 || got != id {
		t.Fatalf("want 503 for %d: %d %v", id, st, err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("cancel took too long")
	}
	if err := ing.Shutdown(); err != nil {
		t.Fatal(err)
	}
	var s Summary
	raw, _ := os.ReadFile(ing.cfg.OutputPath + ".summary.json")
	_ = json.Unmarshal(raw, &s)
	if s.Counts.Accepted < 1 || s.Backends[0].Rejected != 1 {
		t.Fatalf("summary %+v", s)
	}
	b0.Shutdown()
	b1.Shutdown()
	_, d0, _ := b0.Counters().Snapshot()
	if d0 != 1 {
		t.Fatalf("backend 0 cancelled_during_processing = %d", d0)
	}
}

// A client reset is passed upstream (the burn stops), and a dead backend
// gives 502.
//
// Design: abstraction/ingress/frontend/design.md#state-machine-per-forwardtask
func TestResetAndBadGateway(t *testing.T) {
	b0 := startBackend(t, 0.0001)
	ing := startIngress(t, []string{b0.Addr().String()}, PolicyLeastOutstanding, 0)
	conn := dial(t, ing)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, _, err := ping(t, ctx, conn, "POST", "/ping", protocol.BuildBody(protocol.MakeRequestID(0, 1), 64)); err == nil {
		t.Fatal("expected the client's reset")
	}
	// The reset reaches the Ingress a few ms after the client gives up; wait for the
	// forward to end (by the reset, not by the backend going away) before stopping it.
	for i := 0; i < 500 && ing.lb.totalOutstanding() > 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	done := make(chan struct{})
	go func() { b0.Shutdown(); close(done) }() // returns only once the burn has stopped
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("upstream burn was not reset")
	}
	_ = ing.Shutdown()
	if v := ing.lb.Snapshot()[0]; v.Reset != 1 {
		t.Fatalf("reset outcome not counted: %+v", v)
	}

	dead := startIngress(t, []string{"127.0.0.1:1"}, PolicyRoundRobin, 0)
	c2 := dial(t, dead)
	if st, b, _ := ping(t, context.Background(), c2, "POST", "/ping", protocol.BuildBody(5, 64)); st != 502 || len(b) != 0 {
		t.Fatalf("dead backend: %d", st)
	}
	_ = dead.Shutdown()
	if v := dead.lb.Snapshot()[0]; v.BadGateway != 1 || dead.lb.totalOutstanding() != 0 {
		t.Fatalf("bad gateway not counted: %+v", v)
	}
}

var _ = server.CancelCooperative
