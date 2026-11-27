// Source: abstraction/server/worker_pool/design.md (worker_pool v0.7.0)
package server

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"testbed/internal/protocol"
)

func testJob(seq uint32, serviceUs uint64) *PingJob {
	id := protocol.MakeRequestID(seq, 0)
	return newPingJob(&StreamTask{}, id, 0, serviceUs, NoDeadline)
}

func deadlineJob(seq uint32, deadline uint64) *PingJob {
	return newPingJob(&StreamTask{}, protocol.MakeRequestID(seq, 0), 0, 0, deadline)
}

// The EarliestDeadlineFirst worked example: A=300, B=100, C=200, D=none,
// B'=100 (retry copy of B) pushed in that order pop B, B', C, A, D under edf
// and in push order under fifo; Close aborts in pop order; with no deadlines
// edf pops like fifo.
//
// Design: abstraction/server/worker_pool/design.md#earliestdeadlinefirst
func TestWorkQueueEDFWorkedExample(t *testing.T) {
	names := []string{"A", "B", "C", "D", "B'"}
	deadlines := []uint64{300, 100, 200, NoDeadline, 100}
	order := func(disc QueueDiscipline) string {
		q := NewWorkQueue(0, disc, 0, nil)
		byJob := map[*PingJob]string{}
		for i, d := range deadlines {
			j := deadlineJob(uint32(i), d)
			byJob[j] = names[i]
			if err := q.Push(j); err != nil {
				t.Fatal(err)
			}
		}
		if q.Len() != 5 {
			t.Fatalf("len %d", q.Len())
		}
		got := ""
		for i := 0; i < 5; i++ {
			got += byJob[q.Pop()] + " "
		}
		return got
	}
	if got := order(QueueEDF); got != "B B' C A D " {
		t.Fatalf("edf pop order %q", got)
	}
	if got := order(QueueFIFO); got != "A B C D B' " {
		t.Fatalf("fifo pop order %q", got)
	}

	q := NewWorkQueue(0, QueueEDF, 0, nil) // close order is pop order
	a, b, c := deadlineJob(0, 30), deadlineJob(1, 10), deadlineJob(2, 20)
	for _, j := range []*PingJob{a, b, c} {
		q.Push(j)
	}
	done := make(chan *PingJob, 3)
	for _, j := range []*PingJob{a, b, c} {
		go func(j *PingJob) { j.Reply(); done <- j }(j)
	}
	q.Close()
	for i := 0; i < 3; i++ {
		<-done
	}
	if q.Len() != 0 || q.Pop() != nil {
		t.Fatal("closed queue is empty and pops nil")
	}

	q = NewWorkQueue(0, QueueEDF, 0, nil) // no deadlines at all: push order
	var jobs []*PingJob
	for i := 0; i < 4; i++ {
		jobs = append(jobs, testJob(uint32(i), 0))
		q.Push(jobs[i])
	}
	for i := 0; i < 4; i++ {
		if q.Pop() != jobs[i] {
			t.Fatalf("no-deadline jobs must pop in push order (at %d)", i)
		}
	}
}

// Jobs leave the queue in push order; Close answers the rest Aborted and
// makes Pop return nil; Push after Close fails; Close twice is a no-op.
//
// Design: abstraction/server/worker_pool/design.md#workqueue
func TestWorkQueueFIFOAndClose(t *testing.T) {
	q := NewWorkQueue(0, QueueFIFO, 0, nil)
	jobs := []*PingJob{testJob(0, 0), testJob(1, 0), testJob(2, 0)}
	for _, j := range jobs {
		if err := q.Push(j); err != nil {
			t.Fatal(err)
		}
	}
	if got := q.Pop(); got != jobs[0] {
		t.Fatal("first pop must return the first push")
	}
	q.Close()
	q.Close()
	for _, j := range jobs[1:] {
		if r := j.Reply(); !r.Aborted {
			t.Fatalf("dropped job got %+v, want Aborted", r)
		}
	}
	if q.Pop() != nil {
		t.Fatal("pop after close must return nil")
	}
	if q.Push(testJob(3, 0)) != ErrQueueClosed {
		t.Fatal("push after close must fail")
	}
}

// Workers blocked in Pop wake up and return on Close.
//
// Design: abstraction/server/worker_pool/design.md#failure-and-edge-cases
func TestWorkerPoolJoinAfterClose(t *testing.T) {
	q := NewWorkQueue(0, QueueFIFO, 0, nil)
	p := StartWorkerPool(4, q, Config{CancelMode: CancelCooperative, ResponseBytes: 128, CancelCheckIntervalUs: 50}, NewCancelRegistry(16), &CancelCounters{}, nil)
	q.Close()
	done := make(chan struct{})
	go func() { p.Join(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("idle workers did not return")
	}
}

// Worked example: A=300ms, B=100ms, C=100ms. One worker finishes A, B, C;
// two workers finish B, C, A.
//
// Design: abstraction/server/worker_pool/design.md#ordering
func TestWorkerPoolOrdering(t *testing.T) {
	cfg := Config{CancelMode: CancelIgnore, ResponseBytes: 128, CancelCheckIntervalUs: 50}
	for _, c := range []struct {
		n    uint32
		want []uint32
	}{{1, []uint32{0, 1, 2}}, {2, []uint32{1, 2, 0}}} {
		q := NewWorkQueue(0, QueueFIFO, 0, nil)
		jobs := []*PingJob{testJob(0, 300_000), testJob(1, 100_000), testJob(2, 100_000)}
		for _, j := range jobs {
			_ = q.Push(j)
		}
		p := StartWorkerPool(c.n, q, cfg, NewCancelRegistry(16), &CancelCounters{}, nil)
		var mu sync.Mutex
		var order []uint32
		var wg sync.WaitGroup
		for _, j := range jobs {
			wg.Add(1)
			go func(j *PingJob) {
				defer wg.Done()
				if r := j.Reply(); r.Status != 202 {
					t.Errorf("job %d: %+v", j.RequestID, r)
				}
				mu.Lock()
				seq, _ := protocol.SplitRequestID(j.RequestID)
				order = append(order, seq)
				mu.Unlock()
			}(j)
		}
		wg.Wait()
		q.Close()
		p.Join()
		for i := range c.want {
			if order[i] != c.want[i] {
				t.Fatalf("n=%d: finish order %v, want %v", c.n, order, c.want)
			}
		}
	}
}

// A job whose stream was reset while queued is skipped at CancelCheckPoint 1
// in cooperative mode (no burn) and still publishes its id.
//
// Design: abstraction/server/worker_pool/design.md#failure-and-edge-cases
func TestResetWhileQueued(t *testing.T) {
	q := NewWorkQueue(0, QueueFIFO, 0, nil)
	reg := NewCancelRegistry(16)
	j := testJob(9, 3_600_000_000) // an hour: must not run
	j.Task.Cancelled.Store(true)
	_ = q.Push(j)
	p := StartWorkerPool(1, q, Config{CancelMode: CancelCooperative, ResponseBytes: 128, CancelCheckIntervalUs: 50}, reg, &CancelCounters{}, nil)
	if r := j.Reply(); !r.Aborted {
		t.Fatalf("got %+v, want Aborted", r)
	}
	if f, ok := reg.Finished(0); !ok || f != j.RequestID {
		t.Fatal("aborted job must publish its id")
	}
	q.Close()
	p.Join()
}

// Worked example of the late-wake rule, interval 10 ms.
//
// Design: abstraction/server/worker_pool/design.md#queuelengthlog
func TestNextSampleIndexWorkedExample(t *testing.T) {
	for _, c := range []struct{ tMs, want uint64 }{{0, 1}, {10, 2}, {20, 3}, {47, 5}, {50, 6}} {
		if got := nextSampleIndex(c.tMs, 10); got != c.want {
			t.Errorf("t=%d: next k %d, want %d", c.tMs, got, c.want)
		}
	}
}

// The sampler writes the header, then rows with strictly increasing t_ms and
// the current queue length; Stop closes the file.
//
// Design: abstraction/server/worker_pool/design.md#queuelengthlog
func TestQueueSamplerRows(t *testing.T) {
	q := NewWorkQueue(0, QueueFIFO, 0, nil)
	for i := uint32(0); i < 3; i++ {
		_ = q.Push(testJob(i, 0))
	}
	path := filepath.Join(t.TempDir(), "q.csv")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	s := StartQueueSampler(q, f, 5)
	time.Sleep(60 * time.Millisecond)
	s.Stop()
	b, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if lines[0] != "t_ms,queue_len" || len(lines) < 4 {
		t.Fatalf("log %q", b)
	}
	if !strings.HasPrefix(lines[1], "0,") {
		t.Fatalf("first row %q, want t_ms 0", lines[1])
	}
	prev := int64(-1)
	for _, l := range lines[1:] {
		parts := strings.Split(l, ",")
		tMs, _ := strconv.ParseInt(parts[0], 10, 64)
		if tMs <= prev || parts[1] != "3" {
			t.Fatalf("row %q after t=%d", l, prev)
		}
		prev = tMs
	}
	if q.Len() != 3 {
		t.Fatal("sampling must not change the queue")
	}
	q.Close()
	if q.Len() != 0 {
		t.Fatal("closed queue has length 0")
	}
}

// StallGate: nothing is popped until initial_stall_ms after the first push;
// later pushes do not move the opening time; stall 0 never blocks.
//
// Design: abstraction/server/worker_pool/design.md#stallgate
func TestStallGateOpensAfterStall(t *testing.T) {
	q := NewWorkQueue(200, QueueFIFO, 0, nil)
	start := time.Now()
	_ = q.Push(testJob(0, 0))
	time.Sleep(50 * time.Millisecond)
	_ = q.Push(testJob(1, 0)) // does not re-arm
	if q.Len() != 2 {
		t.Fatalf("queue %d, want 2", q.Len())
	}
	first := q.Pop()
	if waited := time.Since(start); waited < 200*time.Millisecond || waited > 2*time.Second {
		t.Fatalf("popped after %v, want ~200ms", waited)
	}
	if seq, _ := protocol.SplitRequestID(first.RequestID); seq != 0 {
		t.Fatal("jobs still leave in push order")
	}
	if q.Pop() == nil {
		t.Fatal("open gate: second job must pop at once")
	}

	q0 := NewWorkQueue(0, QueueFIFO, 0, nil)
	_ = q0.Push(testJob(0, 0))
	done := make(chan struct{})
	go func() { q0.Pop(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stall 0 must not block")
	}
}

// Close during the stall wakes the workers at once and drops queued jobs.
//
// Design: abstraction/server/worker_pool/design.md#stallgate
func TestStallGateCloseDuringStall(t *testing.T) {
	q := NewWorkQueue(60_000, QueueFIFO, 0, nil)
	j := testJob(0, 0)
	_ = q.Push(j)
	p := StartWorkerPool(2, q, Config{CancelMode: CancelCooperative, ResponseBytes: 128, CancelCheckIntervalUs: 50}, NewCancelRegistry(16), &CancelCounters{}, nil)
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	q.Close()
	p.Join()
	if time.Since(start) > time.Second {
		t.Fatal("workers did not return promptly")
	}
	if r := j.Reply(); !r.Aborted {
		t.Fatalf("queued job got %+v, want Aborted", r)
	}
}

// WorkerFreeze: jobs pop until freeze_after_ms after the first push, then
// none do; pushes still grow the queue; FreezeWait blocks until Close and
// Close releases every frozen worker.
//
// Design: abstraction/server/worker_pool/design.md#workerfreeze
func TestWorkerFreezeStopsPopping(t *testing.T) {
	q := NewWorkQueue(0, QueueFIFO, 100, nil)
	_ = q.Push(testJob(0, 0)) // arms: freezeAt = now + 100ms
	if q.Pop() == nil {
		t.Fatal("before the freeze a job must pop")
	}
	q.FreezeWait() // not frozen yet: returns at once
	time.Sleep(150 * time.Millisecond)
	_ = q.Push(testJob(1, 0))
	_ = q.Push(testJob(2, 0))
	if q.Len() != 2 {
		t.Fatalf("frozen queue still admits: len %d, want 2", q.Len())
	}
	popped := make(chan *PingJob, 1)
	go func() { popped <- q.Pop() }()
	waited := make(chan struct{})
	go func() { q.FreezeWait(); close(waited) }()
	select {
	case <-popped:
		t.Fatal("frozen queue handed out a job")
	case <-waited:
		t.Fatal("FreezeWait returned while frozen")
	case <-time.After(200 * time.Millisecond):
	}
	q.Close()
	select {
	case j := <-popped:
		if j != nil {
			t.Fatal("after Close, Pop must return nil")
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not release Pop")
	}
	select {
	case <-waited:
	case <-time.After(time.Second):
		t.Fatal("Close did not release FreezeWait")
	}
}

// Without freeze_after_ms the queue never freezes, and FreezeWait never waits.
//
// Design: abstraction/server/worker_pool/design.md#workerfreeze
func TestWorkerFreezeNever(t *testing.T) {
	q := NewWorkQueue(0, QueueFIFO, 0, nil)
	_ = q.Push(testJob(0, 0))
	time.Sleep(20 * time.Millisecond)
	q.FreezeWait()
	if q.Pop() == nil {
		t.Fatal("no freeze: job must pop")
	}
}

// WorkerFreeze mid-request: a 300 ms sleep that is running when the queue
// freezes (at 50 ms) does not finish while frozen, in both cancel modes; Close
// releases it and it completes.
//
// Design: abstraction/server/ping_handler/design.md#cancelcheckpoints
func TestWorkerFreezeHoldsRunningRequest(t *testing.T) {
	for _, mode := range []CancelMode{CancelCooperative, CancelIgnore} {
		q := NewWorkQueue(0, QueueFIFO, 50, nil)
		_ = q.Push(testJob(0, 300_000)) // arms the freeze at +50 ms
		job := q.Pop()
		cfg := Config{CancelMode: mode, ResponseBytes: 128, Workload: WorkloadSleep, FreezeAfterMs: 50}
		got := make(chan Response, 1)
		go func() { got <- runPing(job, cfg, NewCancelRegistry(16), &CancelCounters{}, nil, q) }()
		select {
		case r := <-got:
			t.Fatalf("mode %v: frozen request answered %+v", mode, r)
		case <-time.After(600 * time.Millisecond):
		}
		q.Close()
		select {
		case r := <-got:
			if r.Status != 202 {
				t.Fatalf("mode %v: after Close got %+v, want 202", mode, r)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("mode %v: Close did not release the request", mode)
		}
	}
}

// scenarioQueue returns a queue that follows a ScenarioSchedule given in ms.
func scenarioQueue(t *testing.T, freezeAfterMs uint64, schedule string) *WorkQueue {
	t.Helper()
	sc, err := NewScenario(`{"states": {"fast": {"service_us": 40000}, "down": {"stall": true},
		"slow": {"service_us": 80000}}, "schedule": `+schedule+`}`, 0)
	if err != nil {
		t.Fatal(err)
	}
	return NewWorkQueue(0, QueueFIFO, freezeAfterMs, sc)
}

// ScenarioStall worked example, scaled from s to ms: schedule fast -> down at
// 60 -> slow at 80. A job popped in fast gets 40000 µs, one popped in slow
// 80000 µs (the state at the pop, not at the push); nothing pops while down,
// and FreezeWait holds until the stall ends.
//
// Design: abstraction/server/worker_pool/design.md#scenariostall
func TestScenarioStallServiceTimeAtPop(t *testing.T) {
	q := scenarioQueue(t, 0, `[[0, "fast"], [60, "down"], [80, "slow"]]`)
	start := time.Now()
	_ = q.Push(testJob(0, 999)) // arms the ScenarioClock
	if j := q.Pop(); j == nil || j.ServiceUs != 40000 {
		t.Fatalf("popped in fast: %+v, want service 40000", j)
	}
	_ = q.Push(testJob(1, 999)) // pushed in fast, popped in down -> slow
	time.Sleep(time.Until(start.Add(65 * time.Millisecond)))
	held := make(chan time.Time, 1)
	go func() { q.FreezeWait(); held <- time.Now() }()
	j := q.Pop()
	popped := time.Since(start)
	if j == nil || j.ServiceUs != 80000 {
		t.Fatalf("popped after the stall: %+v, want service 80000", j)
	}
	if popped < 80*time.Millisecond || popped > 400*time.Millisecond {
		t.Errorf("popped at %v, want just after 80 ms (the stall's end)", popped)
	}
	select {
	case at := <-held:
		if d := at.Sub(start); d < 80*time.Millisecond {
			t.Errorf("FreezeWait returned at %v, during the stall", d)
		}
	case <-time.After(time.Second):
		t.Fatal("FreezeWait did not return after the stall")
	}
	q.FreezeWait() // not stalled: returns at once
	q.Close()
}

// A first state that stalls holds Pop and FreezeWait before the first push
// (no deadline yet); the push arms the clock and wakes them, so both end when
// the stall does.
//
// Design: abstraction/server/worker_pool/design.md#scenariostall
func TestScenarioStallFirstState(t *testing.T) {
	q := scenarioQueue(t, 0, `[[0, "down"], [50, "fast"]]`)
	popped := make(chan *PingJob, 1)
	go func() { popped <- q.Pop() }()
	waited := make(chan time.Time, 1)
	go func() { q.FreezeWait(); waited <- time.Now() }()
	time.Sleep(30 * time.Millisecond)
	start := time.Now()
	_ = q.Push(testJob(0, 0))
	select {
	case j := <-popped:
		if d := time.Since(start); j == nil || j.ServiceUs != 40000 || d < 50*time.Millisecond {
			t.Fatalf("pop %+v at %v, want service 40000 after 50 ms", j, d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the first-state stall never ended for Pop")
	}
	select {
	case at := <-waited:
		if at.Sub(start) < 50*time.Millisecond {
			t.Fatalf("FreezeWait ended at %v, before the stall's end", at.Sub(start))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the first-state stall never ended for FreezeWait")
	}
	q.Close()
}

// A stall that never ends (the last schedule entry) is like a WorkerFreeze:
// Close releases Pop (nil) and FreezeWait. Combined with a WorkerFreeze, the
// freeze wins and never ends.
//
// Design: abstraction/server/worker_pool/design.md#failure-and-edge-cases
func TestScenarioStallPermanentAndClose(t *testing.T) {
	for _, freeze := range []uint64{0, 10} {
		q := scenarioQueue(t, freeze, `[[0, "fast"], [20, "down"]]`)
		_ = q.Push(testJob(0, 0))
		if j := q.Pop(); j == nil {
			t.Fatal("pop before the stall")
		}
		time.Sleep(40 * time.Millisecond)
		_ = q.Push(testJob(1, 0))
		popped := make(chan *PingJob, 1)
		go func() { popped <- q.Pop() }()
		waited := make(chan struct{})
		go func() { q.FreezeWait(); close(waited) }()
		select {
		case <-popped:
			t.Fatalf("freeze %d: popped during a permanent stall", freeze)
		case <-waited:
			t.Fatalf("freeze %d: FreezeWait returned during a permanent stall", freeze)
		case <-time.After(200 * time.Millisecond):
		}
		q.Close()
		select {
		case j := <-popped:
			if j != nil {
				t.Fatalf("freeze %d: after Close, Pop must return nil", freeze)
			}
		case <-time.After(time.Second):
			t.Fatalf("freeze %d: Close did not release Pop", freeze)
		}
		select {
		case <-waited:
		case <-time.After(time.Second):
			t.Fatalf("freeze %d: Close did not release FreezeWait", freeze)
		}
	}
}
