// Source: abstraction/ingress/heartbeat_monitor/design.md (heartbeat_monitor v0.1.0)
package ingress

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"testbed/internal/protocol"
	"testbed/internal/server"
)

func startHeartbeatBackend(t *testing.T, freezeAfterMs uint64) *server.Server {
	t.Helper()
	s, err := server.Listen(server.Config{ListenAddr: "127.0.0.1:0", ResponseRate: 100000, ResponseBytes: 128,
		CancelMode: server.CancelCooperative, CancelCheckIntervalUs: 50, NWorkerThreads: 1,
		HeartbeatIntervalMs: 100, FreezeAfterMs: freezeAfterMs})
	if err != nil {
		t.Fatal(err)
	}
	go s.Serve()
	return s
}

// The worked example's checker rule, on a fixed clock: dead at exactly
// timeout after the last line, live again at the next check after a line.
//
// Design: abstraction/ingress/heartbeat_monitor/design.md#backendliveness
func TestBackendLivenessWorkedExample(t *testing.T) {
	base := time.Now()
	m := &HeartbeatMonitor{base: base, timeoutNs: 1000 * 1_000_000,
		lastHb: make([]atomic.Int64, 1), live: make([]atomic.Bool, 1)}
	m.live[0].Store(true)
	at := func(ms int64) time.Time { return base.Add(time.Duration(ms) * time.Millisecond) }
	m.lastHb[0].Store(60100 * 1_000_000)
	m.check(at(61050))
	if !m.IsLive(0) {
		t.Fatal("950 ms after the last line: still live")
	}
	m.check(at(61100))
	if m.IsLive(0) {
		t.Fatal("1000 ms after the last line: dead")
	}
	m.lastHb[0].Store(75000 * 1_000_000)
	m.check(at(75000))
	if !m.IsLive(0) || len(m.log) != 2 || m.log[0].Live || !m.log[1].Live {
		t.Fatalf("readmission: live %v log %+v", m.IsLive(0), m.log)
	}
	var none *HeartbeatMonitor
	if !none.IsLive(0) || none.Stop() != nil {
		t.Fatal("no monitor: every backend live")
	}
}

// LiveSet: dead backends are skipped by every policy; with none live, all are used.
//
// Design: abstraction/ingress/load_balancer/design.md#lbpolicy
// Design: abstraction/ingress/load_balancer/design.md#worked-examples
func TestLiveSetWorkedExample(t *testing.T) {
	m := &HeartbeatMonitor{live: make([]atomic.Bool, 3)}
	m.live[0].Store(true)
	m.live[2].Store(true) // backend 1 dead: L = [0, 2]
	mk := func(policy string) *LoadBalancer {
		return NewLoadBalancer(Config{LbPolicy: policy, EwmaBeta: 0.7, Backends: []string{"h:1", "h:2", "h:3"}, Seed: 7}, m)
	}
	rr := mk(PolicyRoundRobin)
	for k, want := range []uint32{0, 2, 0} {
		if got := rr.chooseAny(); got != want {
			t.Fatalf("round_robin pick %d: %d want %d", k, got, want)
		}
	}
	lo := mk(PolicyLeastOutstanding)
	setOutstanding(lo, 3, 0, 1)
	if got := lo.chooseAny(); got != 2 {
		t.Fatalf("least_outstanding: %d want 2", got)
	}
	out := []int64{3, 0, 1}
	if got := twoChoice([]uint64{0, 2}, 0, 0, func(x, y uint64) bool { return out[x] < out[y] }); got != 2 {
		t.Fatalf("power_of_two draws (0,0): %d want 2", got)
	}
	for _, p := range []string{PolicyPowerOfTwo, PolicyEwma} {
		lb := mk(p)
		for i := 0; i < 200; i++ {
			if lb.chooseAny() == 1 {
				t.Fatalf("%s chose the dead backend", p)
			}
		}
	}
	m.live[0].Store(false) // only 2 live: no draw
	if got := mk(PolicyEwma).chooseAny(); got != 2 {
		t.Fatalf("n = 1: %d want 2", got)
	}
	m.live[2].Store(false) // none live: all of them
	seen := map[uint32]bool{}
	lb := mk(PolicyRoundRobin)
	for i := 0; i < 3; i++ {
		seen[lb.chooseAny()] = true
	}
	if len(seen) != 3 {
		t.Fatalf("none live: picks %v, want all three", seen)
	}
}

// End to end: two real servers beating every 100 ms, one freezing 300 ms
// after its first /ping. With heartbeat_timeout_ms = 300 the Ingress marks it
// dead, sends it nothing more, and logs the change in liveness.csv.
//
// Design: abstraction/ingress/heartbeat_monitor/design.md#heartbeatreader
// Design: abstraction/ingress/design.md#shutdown
func TestHeartbeatMonitorDropsFrozenBackend(t *testing.T) {
	b0, b1 := startHeartbeatBackend(t, 300), startHeartbeatBackend(t, 0)
	defer b1.Shutdown()
	defer b0.Shutdown()
	out := filepath.Join(t.TempDir(), "ingress")
	cfg, err := Validate(Params{ListenAddr: "127.0.0.1:0", Backends: b0.Addr().String() + "," + b1.Addr().String(),
		NumClients: "4", LbPolicy: PolicyRoundRobin, LbFilter: FilterNone, LbRetryPolicy: RetryPolicyNone, LbMaxRetries: "0", LbRetryTimeoutMs: "150", EwmaBeta: "0.7", CancelRate: "0", DurationS: "5", Seed: "1",
		InitialStallMs: "0", HeartbeatTimeoutMs: "300", OutputPath: out})
	if err != nil {
		t.Fatal(err)
	}
	ing, err := Listen(cfg)
	if err != nil {
		t.Fatal(err)
	}
	go ing.Serve()
	conn := dial(t, ing)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	// Two pings arm both servers (round robin): b0 freezes 300 ms later.
	for seq := uint32(0); seq < 2; seq++ {
		if st, _, err := ping(t, ctx, conn, "POST", "/ping", protocol.BuildBody(protocol.MakeRequestID(seq, 1), 0, nil, 128)); err != nil || st != 202 {
			t.Fatalf("ping %d: %d %v", seq, st, err)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for ing.monitor.IsLive(0) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if ing.monitor.IsLive(0) || !ing.monitor.IsLive(1) {
		t.Fatalf("live = (%v, %v), want (false, true)", ing.monitor.IsLive(0), ing.monitor.IsLive(1))
	}
	before := ing.lb.stats[0].Forwarded.Load()
	for seq := uint32(2); seq < 8; seq++ {
		c, cc := context.WithTimeout(context.Background(), time.Second)
		if st, _, err := ping(t, c, conn, "POST", "/ping", protocol.BuildBody(protocol.MakeRequestID(seq, 1), 0, nil, 128)); err != nil || st != 202 {
			t.Fatalf("ping %d after the freeze: %d %v", seq, st, err)
		}
		cc()
	}
	if got := ing.lb.stats[0].Forwarded.Load(); got != before {
		t.Fatalf("dead backend got %d new forwards", got-before)
	}
	if err := ing.Shutdown(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out + ".liveness.csv")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if lines[0] != "t_ns,server_id,live" || len(lines) != 4 || !strings.HasSuffix(lines[1], ",0,1") ||
		!strings.HasSuffix(lines[2], ",1,1") || !strings.HasSuffix(lines[3], ",0,0") {
		t.Fatalf("liveness.csv:\n%s", b)
	}
}
