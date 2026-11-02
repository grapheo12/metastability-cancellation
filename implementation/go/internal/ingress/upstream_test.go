// Source: abstraction/ingress/upstream_pool/design.md (upstream_pool v0.1.0)
package ingress

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"testbed/internal/h2client"
	"testbed/internal/server"
)

func startBackend(t *testing.T, rate float64) *server.Server {
	t.Helper()
	s, err := server.Listen(server.Config{ListenAddr: "127.0.0.1:0", ResponseRate: rate, ResponseBytes: 64,
		CancelMode: server.CancelCooperative, CancelCheckIntervalUs: 50})
	if err != nil {
		t.Fatal(err)
	}
	go s.Serve()
	return s
}

// The event table: one dial per pair under concurrency, failures not cached,
// identity-checked discard.
//
// Design: abstraction/ingress/upstream_pool/design.md#get
func TestUpstreamPoolEvents(t *testing.T) {
	b := startBackend(t, 100000)
	defer b.Shutdown()
	p := NewUpstreamPool([]string{"127.0.0.1:1", b.Addr().String()})
	var dials atomic.Int32
	fail := atomic.Bool{}
	p.dial = func(target string) (*h2client.Conn, error) {
		dials.Add(1)
		if fail.Load() {
			return nil, errors.New("refused")
		}
		return h2client.Dial(target)
	}
	var wg sync.WaitGroup
	conns := make([]*h2client.Conn, 8)
	for i := range conns {
		wg.Add(1)
		go func(i int) { defer wg.Done(); conns[i], _ = p.Get(2, 1) }(i)
	}
	wg.Wait()
	if dials.Load() != 1 {
		t.Fatalf("concurrent first gets dialed %d times", dials.Load())
	}
	for _, c := range conns {
		if c == nil || c != conns[0] {
			t.Fatal("all gets must share one connection")
		}
	}
	x := conns[0]
	p.Discard(2, 1, x)
	fail.Store(true)
	if _, err := p.Get(2, 1); err == nil {
		t.Fatal("failed dial must return an error")
	}
	fail.Store(false)
	y, err := p.Get(2, 1)
	if err != nil || y == x {
		t.Fatalf("redial after failure: %v", err)
	}
	p.Discard(2, 1, x) // late discard of X keeps Y
	if z, _ := p.Get(2, 1); z != y {
		t.Fatal("late discard must not drop Y")
	}
	if dials.Load() != 3 {
		t.Fatalf("dials %d, want 3", dials.Load())
	}
	p.CloseAll()
}
