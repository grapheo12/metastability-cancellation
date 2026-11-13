// Source: abstraction/server/worker_pool/design.md (worker_pool v0.5.0)
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
		q := NewWorkQueue(0, disc)
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

	q := NewWorkQueue(0, QueueEDF) // close order is pop order
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

	q = NewWorkQueue(0, QueueEDF) // no deadlines at all: push order
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
	q := NewWorkQueue(0, QueueFIFO)
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
	q := NewWorkQueue(0, QueueFIFO)
	p := StartWorkerPool(4, q, Config{CancelMode: CancelCooperative, ResponseBytes: 64, CancelCheckIntervalUs: 50}, NewCancelRegistry(16), &CancelCounters{}, nil)
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
	cfg := Config{CancelMode: CancelIgnore, ResponseBytes: 64, CancelCheckIntervalUs: 50}
	for _, c := range []struct {
		n    uint32
		want []uint32
	}{{1, []uint32{0, 1, 2}}, {2, []uint32{1, 2, 0}}} {
		q := NewWorkQueue(0, QueueFIFO)
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
	q := NewWorkQueue(0, QueueFIFO)
	reg := NewCancelRegistry(16)
	j := testJob(9, 3_600_000_000) // an hour: must not run
	j.Task.Cancelled.Store(true)
	_ = q.Push(j)
	p := StartWorkerPool(1, q, Config{CancelMode: CancelCooperative, ResponseBytes: 64, CancelCheckIntervalUs: 50}, reg, &CancelCounters{}, nil)
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
	q := NewWorkQueue(0, QueueFIFO)
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
	q := NewWorkQueue(200, QueueFIFO)
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

	q0 := NewWorkQueue(0, QueueFIFO)
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
	q := NewWorkQueue(60_000, QueueFIFO)
	j := testJob(0, 0)
	_ = q.Push(j)
	p := StartWorkerPool(2, q, Config{CancelMode: CancelCooperative, ResponseBytes: 64, CancelCheckIntervalUs: 50}, NewCancelRegistry(16), &CancelCounters{}, nil)
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
