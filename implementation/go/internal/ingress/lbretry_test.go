// Source: abstraction/ingress/frontend/design.md (frontend v0.10.0)
package ingress

import (
	"context"
	"encoding/json"
	"fmt"
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

// fakeBackend is a prior-knowledge h2c /ping server that answers 202 after
// delay, or stops early if its stream is reset. It counts requests and resets.
type fakeBackend struct {
	ln     net.Listener
	hits   atomic.Int64
	resets atomic.Int64
}

func startFakeBackend(t *testing.T, delay time.Duration) *fakeBackend {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fb := &fakeBackend{ln: ln}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		id, _ := protocol.ParseBody(b)
		fb.hits.Add(1)
		select {
		case <-time.After(delay):
			w.Header().Set(protocol.WorkerThreadsHeader, "1")
			w.WriteHeader(protocol.StatusOK)
			_, _ = w.Write(protocol.BuildBody(id, uint64(delay), nil, 128))
		case <-r.Context().Done():
			fb.resets.Add(1)
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
	t.Cleanup(func() { ln.Close() })
	return fb
}

func (fb *fakeBackend) addr() string { return fb.ln.Addr().String() }

// startRetryIngress is an Ingress with round_robin, no filter, and the given
// LB retry policy and budget.
func startRetryIngress(t *testing.T, backends []string, policy, maxRetries string) *Ingress {
	t.Helper()
	cfg, err := Validate(Params{ListenAddr: "127.0.0.1:0", Backends: joinComma(backends), NumClients: "4",
		LbPolicy: PolicyRoundRobin, LbFilter: FilterNone, LbRetryPolicy: policy, LbMaxRetries: maxRetries, LbRetryTimeoutMs: "50", LbRetryAnxietyRatio: "0.3",
		EwmaBeta: "0.7", CancelRate: "0", DurationS: "5", Seed: "1", InitialStallMs: "0", HeartbeatTimeoutMs: "0",
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

// Test-only policies: retry on a failed newest attempt; hedge once the newest
// attempt has run 50 ms; retry on every tick.
func init() {
	RegisterRetryPolicy("test_failover", func(Config) RetryPolicy {
		return RetryPolicyFunc(func(s RetryState, e RetryEvent) RetryDecision {
			if e.Kind == EventAttemptEnded {
				return Retry
			}
			return Wait
		})
	})
	RegisterRetryPolicy("test_hedge50", func(Config) RetryPolicy {
		return RetryPolicyFunc(func(s RetryState, e RetryEvent) RetryDecision {
			n := s.Attempts[len(s.Attempts)-1]
			if e.Kind == EventTick && n.Running && s.Now.Sub(n.Start) >= 50*time.Millisecond {
				return Retry
			}
			return Wait
		})
	})
	RegisterRetryPolicy("test_always", func(Config) RetryPolicy {
		return RetryPolicyFunc(func(RetryState, RetryEvent) RetryDecision { return Retry })
	})
}

func forwardsAttempts(t *testing.T, ing *Ingress) []string {
	t.Helper()
	raw, _ := os.ReadFile(ing.cfg.OutputPath + ".forwards.csv")
	rows := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")[1:]
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		f := strings.Split(r, ",")
		out = append(out, f[1]+":"+f[2]+":"+f[7]) // server:outcome:attempt
	}
	return out
}

func lbRetriesOf(t *testing.T, ing *Ingress) uint64 {
	t.Helper()
	var s Summary
	raw, _ := os.ReadFile(ing.cfg.OutputPath + ".summary.json")
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	return s.LbRetries
}

// Fail-over: attempt 0's backend is down (502); the policy retries on
// AttemptEnded and round_robin sends attempt 1 to the healthy backend.
//
// Design: abstraction/ingress/frontend/design.md#lbretryloop
func TestLbRetryFailover(t *testing.T) {
	good := startFakeBackend(t, 10*time.Millisecond)
	ing := startRetryIngress(t, []string{"127.0.0.1:1", good.addr()}, "test_failover", "1")
	conn := dial(t, ing)
	id := protocol.MakeRequestID(0, 1)
	st, b, err := ping(t, context.Background(), conn, "POST", "/ping", protocol.BuildBody(id, 0, nil, 128))
	if got, _ := protocol.ParseBody(b); err != nil || st != 202 || got != id {
		t.Fatalf("want 202 from attempt 1: %d %v", st, err)
	}
	if err := ing.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if got := forwardsAttempts(t, ing); len(got) != 2 || got[0] != "0:bad_gateway:0" || got[1] != "1:ok:1" {
		t.Fatalf("forwards %v", got)
	}
	if lbRetriesOf(t, ing) != 1 {
		t.Fatal("lb_retries must be 1")
	}
}

// Hedge on tick: attempt 0 is slow; at about 50 ms attempt 1 goes to the fast
// backend and its answer is relayed; attempt 0 is not reset, finishes later and
// is logged with attempt 0; the InFlightMap shows attempt 1 meanwhile.
//
// Design: abstraction/ingress/frontend/design.md#lbretryloop
// Design: abstraction/ingress/frontend/design.md#forwardattempt
func TestLbRetryHedgeNewestAnswerCounts(t *testing.T) {
	slow, fast := startFakeBackend(t, 400*time.Millisecond), startFakeBackend(t, 100*time.Millisecond)
	ing := startRetryIngress(t, []string{slow.addr(), fast.addr()}, "test_hedge50", "1")
	conn := dial(t, ing)
	id := protocol.MakeRequestID(0, 2)
	sawAttempt1 := make(chan bool, 1)
	go func() {
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			for _, v := range ing.inflight.Snapshot() {
				if v.Entry.RequestID == id && v.Entry.Attempt == 1 && v.Entry.ServerID == 1 {
					sawAttempt1 <- true
					return
				}
			}
			time.Sleep(5 * time.Millisecond)
		}
		sawAttempt1 <- false
	}()
	start := time.Now()
	st, b, err := ping(t, context.Background(), conn, "POST", "/ping", protocol.BuildBody(id, 0, nil, 128))
	elapsed := time.Since(start)
	if ex, _, _, _ := protocol.ParseExecReport(b); err != nil || st != 202 || ex != uint64(100*time.Millisecond) {
		t.Fatalf("want the fast backend's 202: %d %v exec %d", st, err, ex)
	}
	if elapsed < 140*time.Millisecond || elapsed > 380*time.Millisecond {
		t.Fatalf("answer after %v: want about 50 + 100 ms", elapsed)
	}
	if !<-sawAttempt1 {
		t.Fatal("InFlightMap must show attempt 1 @ server 1")
	}
	time.Sleep(400 * time.Millisecond) // attempt 0 finishes in the background
	if err := ing.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if slow.resets.Load() != 0 {
		t.Fatal("the older attempt must not be reset")
	}
	if got := forwardsAttempts(t, ing); len(got) != 2 || got[0] != "1:ok:1" || got[1] != "0:ok:0" {
		t.Fatalf("forwards %v", got)
	}
}

// The budget is enforced by the Frontend: a policy that always says Retry gets
// exactly lb_max_retries LbRetries; with lb_max_retries = 0, none.
//
// Design: abstraction/ingress/frontend/design.md#lbretryloop
func TestLbRetryBudget(t *testing.T) {
	for _, c := range []struct {
		max  string
		want uint64
	}{{"2", 2}, {"0", 0}} {
		a, b := startFakeBackend(t, 150*time.Millisecond), startFakeBackend(t, 150*time.Millisecond)
		ing := startRetryIngress(t, []string{a.addr(), b.addr()}, "test_always", c.max)
		conn := dial(t, ing)
		if st, _, err := ping(t, context.Background(), conn, "POST", "/ping", protocol.BuildBody(protocol.MakeRequestID(0, 3), 0, nil, 128)); err != nil || st != 202 {
			t.Fatalf("max %s: %d %v", c.max, st, err)
		}
		time.Sleep(200 * time.Millisecond)
		_ = ing.Shutdown()
		if got := lbRetriesOf(t, ing); got != c.want || uint64(a.hits.Load()+b.hits.Load()) != c.want+1 {
			t.Fatalf("max %s: lb_retries %d, hits %d", c.max, got, a.hits.Load()+b.hits.Load())
		}
	}
}

// A client reset resets every running attempt upstream.
//
// Design: abstraction/ingress/frontend/design.md#lbretryloop
func TestLbRetryClientResetCancelsAll(t *testing.T) {
	a, b := startFakeBackend(t, time.Hour), startFakeBackend(t, time.Hour)
	ing := startRetryIngress(t, []string{a.addr(), b.addr()}, "test_hedge50", "1")
	conn := dial(t, ing)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, _, err := ping(t, ctx, conn, "POST", "/ping", protocol.BuildBody(protocol.MakeRequestID(0, 1), 0, nil, 128)); err == nil {
		t.Fatal("expected the client's reset")
	}
	for i := 0; i < 200 && a.resets.Load()+b.resets.Load() < 2; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if a.resets.Load() != 1 || b.resets.Load() != 1 {
		t.Fatalf("resets %d / %d, want both attempts reset", a.resets.Load(), b.resets.Load())
	}
	_ = ing.Shutdown()
	got := forwardsAttempts(t, ing)
	if len(got) != 2 || !strings.Contains(got[0], ":reset:") || !strings.Contains(got[1], ":reset:") {
		t.Fatalf("forwards %v: both attempts must end reset", got)
	}
}

// With none (or a budget of 0) a ForwardTask is one attempt: a dead backend's
// 502 is relayed, nothing is retried.
//
// Design: abstraction/ingress/frontend/design.md#lbretryloop
func TestLbRetryNoneIsOneAttempt(t *testing.T) {
	good := startFakeBackend(t, 10*time.Millisecond)
	ing := startRetryIngress(t, []string{"127.0.0.1:1", good.addr()}, RetryPolicyNone, "3")
	conn := dial(t, ing)
	if st, _, _ := ping(t, context.Background(), conn, "POST", "/ping", protocol.BuildBody(protocol.MakeRequestID(0, 1), 0, nil, 128)); st != 502 {
		t.Fatalf("none: want 502, got %d", st)
	}
	_ = ing.Shutdown()
	if lbRetriesOf(t, ing) != 0 || good.hits.Load() != 0 {
		t.Fatal("none must not retry")
	}
}

// The timeout policy end to end (lb_retry_timeout_ms = 50): attempt 0 is slow,
// attempt 1 starts 50-60 ms after attempt 0's pick and its answer is relayed.
//
// Design: abstraction/ingress/retry_policy/design.md#retrypolicyregistry
// Design: abstraction/ingress/frontend/design.md#lbretryloop
func TestLbRetryTimeoutPolicy(t *testing.T) {
	slow, fast := startFakeBackend(t, 500*time.Millisecond), startFakeBackend(t, 20*time.Millisecond)
	ing := startRetryIngress(t, []string{slow.addr(), fast.addr()}, RetryPolicyTimeout, "1")
	conn := dial(t, ing)
	start := time.Now()
	st, b, err := ping(t, context.Background(), conn, "POST", "/ping", protocol.BuildBody(protocol.MakeRequestID(0, 1), 0, nil, 128))
	elapsed := time.Since(start)
	if ex, _, _, _ := protocol.ParseExecReport(b); err != nil || st != 202 || ex != uint64(20*time.Millisecond) {
		t.Fatalf("want the fast backend's 202: %d %v", st, err)
	}
	if elapsed < 70*time.Millisecond || elapsed > 300*time.Millisecond {
		t.Fatalf("answer after %v: want about 50-60 + 20 ms", elapsed)
	}
	time.Sleep(500 * time.Millisecond)
	_ = ing.Shutdown()
	if lbRetriesOf(t, ing) != 1 || slow.resets.Load() != 0 {
		t.Fatalf("lb_retries %d, slow resets %d", lbRetriesOf(t, ing), slow.resets.Load())
	}
}

// startFilterIngress is an Ingress with deadline_mean and no LB retries.
func startFilterIngress(t *testing.T, backends []string) *Ingress {
	t.Helper()
	cfg, err := Validate(Params{ListenAddr: "127.0.0.1:0", Backends: joinComma(backends), NumClients: "4",
		LbPolicy: PolicyRoundRobin, LbFilter: FilterDeadlineMean, LbRetryPolicy: RetryPolicyNone, LbMaxRetries: "0",
		LbRetryTimeoutMs: "150", LbRetryAnxietyRatio: "0.3", EwmaBeta: "0.7", CancelRate: "0", DurationS: "5", Seed: "1", InitialStallMs: "0",
		HeartbeatTimeoutMs: "0", OutputPath: filepath.Join(t.TempDir(), "ingress")})
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

func summaryOf(t *testing.T, ing *Ingress) Summary {
	t.Helper()
	var s Summary
	raw, _ := os.ReadFile(ing.cfg.OutputPath + ".summary.json")
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

// A request that does not fit waits, and is sent once the backend's load
// drops (300 ms service, EwmaExecTime 300 ms): with two in flight it needs
// 0.9 s > 0.75 s; once they finish it needs 0.3 s and is sent from the queue.
//
// Design: abstraction/ingress/frontend/design.md#forwardtask
// Design: abstraction/ingress/load_balancer/design.md#filterwaitqueue
func TestFilterWaitQueueEndToEnd(t *testing.T) {
	fb := startFakeBackend(t, 300*time.Millisecond)
	ing := startFilterIngress(t, []string{fb.addr()})
	conn := dial(t, ing)
	ctx := context.Background()
	body := func(seq uint32, left time.Duration) []byte {
		return protocol.BuildRequestBody(protocol.MakeRequestID(seq, 1), uint64(time.Now().Add(left).UnixNano()), 128)
	}
	if st, _, err := ping(t, ctx, conn, "POST", "/ping", body(0, 5*time.Second)); err != nil || st != 202 {
		t.Fatalf("warm-up: %d %v", st, err) // EwmaExecTime = 300 ms from here
	}
	done := make(chan int, 2)
	for seq := uint32(1); seq <= 2; seq++ {
		go func(seq uint32) {
			st, _, _ := ping(t, ctx, conn, "POST", "/ping", body(seq, 5*time.Second))
			done <- st
		}(seq)
	}
	time.Sleep(30 * time.Millisecond) // both in flight: n = 2
	st, _, err := ping(t, ctx, conn, "POST", "/ping", body(3, 750*time.Millisecond))
	if err != nil || st != 202 {
		t.Fatalf("the waiting request must be sent once load drops: %d %v", st, err)
	}
	<-done
	<-done
	_ = ing.Shutdown()
	s := summaryOf(t, ing)
	if s.FilterQueued != 1 || s.FilterSentFromQ != 1 || s.FilterDropped != 0 || s.FilterQueueMax < 1 {
		t.Fatalf("summary %+v", s)
	}
}

// A client reset while waiting writes nothing and is never forwarded.
//
// Design: abstraction/ingress/frontend/design.md#forwardtask
func TestFilterWaitQueueClientReset(t *testing.T) {
	fb := startFakeBackend(t, 2*time.Second)
	ing := startFilterIngress(t, []string{fb.addr()})
	conn := dial(t, ing)
	bg := context.Background()
	if st, _, _ := ping(t, bg, conn, "POST", "/ping", protocol.BuildRequestBody(protocol.MakeRequestID(0, 1), uint64(time.Now().Add(10*time.Second).UnixNano()), 128)); st != 202 {
		t.Fatal("warm-up") // EwmaExecTime = 2 s
	}
	go ping(t, bg, conn, "POST", "/ping", protocol.BuildRequestBody(protocol.MakeRequestID(1, 1), uint64(time.Now().Add(10*time.Second).UnixNano()), 128))
	time.Sleep(30 * time.Millisecond) // n = 1: a 3 s request needs 4 s, so it waits
	ctx, cancel := context.WithTimeout(bg, 100*time.Millisecond)
	defer cancel()
	if _, _, err := ping(t, ctx, conn, "POST", "/ping", protocol.BuildRequestBody(protocol.MakeRequestID(2, 1), uint64(time.Now().Add(3*time.Second).UnixNano()), 128)); err == nil {
		t.Fatal("expected the client's reset")
	}
	time.Sleep(50 * time.Millisecond) // a scan removes the gone entry
	if hits := fb.hits.Load(); hits != 2 {
		t.Fatalf("the reset request must never be forwarded: %d hits", hits)
	}
	time.Sleep(2 * time.Second)
	_ = ing.Shutdown()
	if s := summaryOf(t, ing); s.FilterQueued != 1 || s.FilterSentFromQ != 0 || s.FilterDropped != 0 {
		t.Fatalf("summary %+v", s)
	}
}

// anxious end to end: once both backends have an EwmaExecTime (one ping to
// each), a ping on the slow backend (exec 300 ms; anti 20 ms) with only 100 ms
// left is anxious and ExpectedLate at once, and is retried on the fast one
// within a few ticks. The warm-up pings have no deadline: never retried.
//
// Design: abstraction/ingress/frontend/design.md#forwardattempt
func TestLbRetryAnxiousPolicy(t *testing.T) {
	slow, fast := startFakeBackend(t, 300*time.Millisecond), startFakeBackend(t, 20*time.Millisecond)
	ing := startRetryIngress(t, []string{slow.addr(), fast.addr()}, RetryPolicyAnxious, "1")
	conn := dial(t, ing)
	for i := uint32(1); i <= 2; i++ { // round robin: slow, then fast; no EWMA yet for the first
		if st, _, err := ping(t, context.Background(), conn, "POST", "/ping", protocol.BuildBody(protocol.MakeRequestID(0, i), 0, nil, 128)); err != nil || st != 202 {
			t.Fatalf("warm-up %d: %d %v", i, st, err)
		}
	}
	if n := ing.lbRetries.Load(); n != 0 {
		t.Fatalf("warm-up retried %d times", n)
	}
	start := time.Now()
	// RequestDeadline 100 ms ahead: CurrentExpFinish (1 x 300 ms) > time left, so ExpectedLate.
	late := []byte(fmt.Sprintf(`{"id":"%d","deadline":"%d"}`, protocol.MakeRequestID(0, 3), time.Now().Add(100*time.Millisecond).UnixNano()))
	st, b, err := ping(t, context.Background(), conn, "POST", "/ping", late)
	elapsed := time.Since(start)
	if ex, _, _, _ := protocol.ParseExecReport(b); err != nil || st != 202 || ex != uint64(20*time.Millisecond) {
		t.Fatalf("want the fast backend's 202: %d %v", st, err)
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("answer after %v: want a retry within a few 10 ms ticks + 20 ms", elapsed)
	}
	time.Sleep(400 * time.Millisecond)
	_ = ing.Shutdown()
	if lbRetriesOf(t, ing) != 1 || slow.hits.Load() != 2 || fast.hits.Load() != 2 {
		t.Fatalf("lb_retries %d, slow hits %d, fast hits %d", lbRetriesOf(t, ing), slow.hits.Load(), fast.hits.Load())
	}
}

// observingElsewhere answers RetryElsewhere on AttemptEnded and records the
// TicketSeqs of RetryRefused observations.
type observingElsewhere struct{ refused chan uint64 }

func (p *observingElsewhere) Decide(s RetryState, e RetryEvent) RetryDecision {
	if e.Kind == EventAttemptEnded {
		return RetryElsewhere
	}
	return Wait
}

func (p *observingElsewhere) Observe(o RetryObservation) {
	if o.Kind == ObsRetryRefused {
		p.refused <- o.Seq
	}
}

// RetryElsewhere with one backend: the retry's pick excludes it, so it is
// refused, the 502 is relayed, lb_retry_refused counts it and the policy is
// told RetryRefused with attempt 0's TicketSeq (0, the run's first pick).
//
// Design: abstraction/ingress/frontend/design.md#lbretryloop
func TestLbRetryElsewhereRefused(t *testing.T) {
	pol := &observingElsewhere{refused: make(chan uint64, 4)}
	RegisterRetryPolicy("test_elsewhere", func(Config) RetryPolicy { return pol })
	defer delete(retryPolicies, "test_elsewhere")
	ing := startRetryIngress(t, []string{"127.0.0.1:1"}, "test_elsewhere", "1")
	conn := dial(t, ing)
	st, _, err := ping(t, context.Background(), conn, "POST", "/ping", protocol.BuildBody(protocol.MakeRequestID(0, 1), 0, nil, 128))
	if err != nil || st != 502 {
		t.Fatalf("want the 502 relayed: %d %v", st, err)
	}
	select {
	case seq := <-pol.refused:
		if seq != 0 {
			t.Fatalf("RetryRefused seq %d, want 0", seq)
		}
	case <-time.After(time.Second):
		t.Fatal("no RetryRefused observation")
	}
	if n := ing.lbRetryRefused.Load(); n != 1 {
		t.Fatalf("lb_retry_refused %d", n)
	}
	_ = ing.Shutdown()
}

// sentRecorder never retries and records the deadline of every AttemptSent.
type sentRecorder struct{ deadlines chan uint64 }

func (p *sentRecorder) Decide(RetryState, RetryEvent) RetryDecision { return Wait }
func (p *sentRecorder) Observe(o RetryObservation) {
	if o.Kind == ObsAttemptSent {
		p.deadlines <- o.Deadline
	}
}

// AttemptSent carries the request's RequestDeadline, or NoDeadline without one.
//
// Design: abstraction/ingress/frontend/design.md#forwardattempt
func TestAttemptSentDeadline(t *testing.T) {
	rec := &sentRecorder{deadlines: make(chan uint64, 4)}
	RegisterRetryPolicy("test_sent_recorder", func(Config) RetryPolicy { return rec })
	defer delete(retryPolicies, "test_sent_recorder")
	fb := startFakeBackend(t, time.Millisecond)
	ing := startRetryIngress(t, []string{fb.addr()}, "test_sent_recorder", "0")
	conn := dial(t, ing)
	dl := uint64(time.Now().Add(time.Hour).UnixNano())
	for _, body := range [][]byte{
		[]byte(fmt.Sprintf(`{"id":"%d","deadline":"%d"}`, protocol.MakeRequestID(0, 1), dl)),
		protocol.BuildBody(protocol.MakeRequestID(0, 2), 0, nil, 128),
	} {
		if st, _, err := ping(t, context.Background(), conn, "POST", "/ping", body); err != nil || st != 202 {
			t.Fatalf("ping: %d %v", st, err)
		}
	}
	for i, want := range []uint64{dl, NoDeadline} {
		select {
		case got := <-rec.deadlines:
			if got != want {
				t.Fatalf("AttemptSent %d: deadline %d, want %d", i, got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("AttemptSent %d not observed", i)
		}
	}
	_ = ing.Shutdown()
}
