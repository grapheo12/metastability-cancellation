// Source: abstraction/server/worker_pool/design.md (worker_pool v0.5.0)
package server

import (
	"bufio"
	"container/heap"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// ErrQueueClosed is returned by Push once the WorkQueue has been closed.
var ErrQueueClosed = errors.New("work queue closed")

// PingJob is one admitted POST /ping. reply is the OneShot: a 1-buffered
// channel that receives exactly one Response, from the worker that ran the
// job or from Close.
//
// Design: abstraction/server/worker_pool/design.md#pingjob
type PingJob struct {
	Task      *StreamTask
	RequestID uint64
	ClientID  uint32
	ServiceUs uint64
	// DeadlineUnixNs is the RequestDeadline, or NoDeadline; read only under edf.
	DeadlineUnixNs uint64
	reply          chan Response
}

// NoDeadline is NO_DEADLINE = 2^64 - 1: a job without a RequestDeadline pops
// after every job with one under edf.
const NoDeadline = ^uint64(0)

func newPingJob(task *StreamTask, requestID uint64, clientID uint32, serviceUs, deadlineUnixNs uint64) *PingJob {
	return &PingJob{Task: task, RequestID: requestID, ClientID: clientID, ServiceUs: serviceUs,
		DeadlineUnixNs: deadlineUnixNs, reply: make(chan Response, 1)}
}

// QueueDiscipline is the WorkQueue's pop order.
//
// Design: abstraction/server/worker_pool/design.md#workqueue
type QueueDiscipline int

const (
	QueueFIFO QueueDiscipline = iota // push order
	QueueEDF                         // EarliestDeadlineFirst
)

// edfItem is one edf entry, keyed by (deadline, seq).
type edfItem struct {
	deadline, seq uint64
	job           *PingJob
}

// edfHeap is a container/heap min-heap of edfItems: the smallest
// (deadline, seq) pair is at index 0.
//
// Design: abstraction/server/worker_pool/design.md#earliestdeadlinefirst
type edfHeap []edfItem

func (h edfHeap) Len() int { return len(h) }
func (h edfHeap) Less(i, j int) bool {
	if h[i].deadline != h[j].deadline {
		return h[i].deadline < h[j].deadline
	}
	return h[i].seq < h[j].seq // equal deadlines (retry copies, no deadline) pop in push order
}
func (h edfHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *edfHeap) Push(x interface{}) { *h = append(*h, x.(edfItem)) }
func (h *edfHeap) Pop() interface{} {
	old := *h
	it := old[len(old)-1]
	old[len(old)-1] = edfItem{} // let the GC reclaim the job once answered
	*h = old[:len(old)-1]
	return it
}

// Reply blocks until the job's result is available (OneShot take).
func (j *PingJob) Reply() Response { return <-j.reply }

// WorkQueue is the process-wide unbounded MPMC queue: one mutex, one condition
// variable, and a slice used as a FIFO (fifo) or a container/heap min-heap
// (edf); Go channels are bounded. armed/openAt
// are the StallGate, guarded by mu like everything else.
//
// Design: abstraction/server/worker_pool/design.md#workqueue
type WorkQueue struct {
	mu       sync.Mutex
	notEmpty *sync.Cond
	disc     QueueDiscipline
	items    []*PingJob // fifo
	edf      edfHeap    // edf
	nextSeq  uint64     // edf: push_seq of the next push
	closed   bool
	stall    time.Duration
	armed    bool
	openAt   time.Time // monotonic reading; valid once armed
}

// NewWorkQueue returns an empty, open queue with the given QueueDiscipline
// whose StallGate opens initialStallMs after the first push.
//
// Design: abstraction/server/worker_pool/design.md#workqueue
func NewWorkQueue(initialStallMs uint64, disc QueueDiscipline) *WorkQueue {
	q := &WorkQueue{stall: time.Duration(initialStallMs) * time.Millisecond, disc: disc}
	q.notEmpty = sync.NewCond(&q.mu)
	return q
}

// Push adds job (at the tail for fifo, by (deadline, push seq) for edf). It never blocks; after Close it returns
// ErrQueueClosed and leaves job.reply untouched.
//
// Design: abstraction/server/worker_pool/design.md#workqueue
func (q *WorkQueue) Push(job *PingJob) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return ErrQueueClosed
	}
	if !q.armed { // StallGate: the first push arms it
		q.arm()
	}
	q.addItem(job)
	q.notEmpty.Signal()
	return nil
}

// addItem adds job in the discipline's order. Called with mu held.
//
// Design: abstraction/server/worker_pool/design.md#workqueue
// Design: abstraction/server/worker_pool/design.md#earliestdeadlinefirst
func (q *WorkQueue) addItem(job *PingJob) {
	if q.disc == QueueFIFO {
		q.items = append(q.items, job)
		return
	}
	heap.Push(&q.edf, edfItem{deadline: job.DeadlineUnixNs, seq: q.nextSeq, job: job})
	q.nextSeq++
}

// takeItem removes the next job in the discipline's order. Called with mu
// held and the queue not empty.
//
// Design: abstraction/server/worker_pool/design.md#workqueue
func (q *WorkQueue) takeItem() *PingJob {
	if q.disc == QueueFIFO {
		job := q.items[0]
		q.items[0] = nil // let the GC reclaim the job once answered
		q.items = q.items[1:]
		return job
	}
	return heap.Pop(&q.edf).(edfItem).job
}

// lenLocked is the number of queued jobs. Called with mu held.
func (q *WorkQueue) lenLocked() int { return len(q.items) + len(q.edf) }

// arm sets the StallGate's opening time. With a stall, a timer wakes every
// waiting worker at openAt; time.AfterFunc fires no earlier than that, and Pop
// re-checks the time anyway. Called with mu held.
//
// Design: abstraction/server/worker_pool/design.md#stallgate
func (q *WorkQueue) arm() {
	q.armed = true
	q.openAt = time.Now().Add(q.stall)
	if q.stall > 0 {
		time.AfterFunc(q.stall, func() {
			q.mu.Lock()
			q.notEmpty.Broadcast()
			q.mu.Unlock()
		})
	}
}

// Pop removes and returns the next job in the discipline's order, waiting while the queue is empty or
// the StallGate is closed, and the queue is open. It returns nil once the
// queue is closed (closed wins over items).
//
// Design: abstraction/server/worker_pool/design.md#workqueue
// Design: abstraction/server/worker_pool/design.md#stallgate
func (q *WorkQueue) Pop() *PingJob {
	q.mu.Lock()
	defer q.mu.Unlock()
	for !q.closed && (q.lenLocked() == 0 || time.Now().Before(q.openAt)) {
		q.notEmpty.Wait() // woken by a push, the gate's timer, or Close
	}
	if q.closed {
		return nil
	}
	return q.takeItem()
}

// Close closes the queue, answers every queued job Aborted in pop order
// (outside the lock) and wakes every waiting worker. Idempotent.
//
// Design: abstraction/server/worker_pool/design.md#workqueue
func (q *WorkQueue) Close() {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	q.closed = true
	var dropped []*PingJob
	for q.lenLocked() > 0 {
		dropped = append(dropped, q.takeItem())
	}
	q.notEmpty.Broadcast()
	q.mu.Unlock()
	for _, job := range dropped {
		job.reply <- Response{Aborted: true}
	}
}

// WorkerPool is the set of running workers.
//
// Design: abstraction/server/worker_pool/design.md#workerpool
type WorkerPool struct {
	wg sync.WaitGroup
}

// StartWorkerPool starts n workers (n >= 1, checked by Validate). Each is a
// goroutine; Go preempts goroutines and runs them on GOMAXPROCS OS threads,
// which is how burns run in parallel with each other and with the HTTP/2 read
// loops.
//
// Design: abstraction/server/worker_pool/design.md#workerpool
// Design: abstraction/server/worker_pool/design.md#concurrency-requirement
func StartWorkerPool(n uint32, q *WorkQueue, cfg Config, registry *CancelRegistry, counters *CancelCounters, buf *MessageBuffer) *WorkerPool {
	p := &WorkerPool{}
	for w := uint32(0); w < n; w++ {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			workerLoop(q, cfg, registry, counters, buf)
		}()
	}
	return p
}

// workerLoop pops jobs in push order until the queue is closed.
//
// Design: abstraction/server/worker_pool/design.md#workerpool
// Design: abstraction/server/worker_pool/design.md#ordering
func workerLoop(q *WorkQueue, cfg Config, registry *CancelRegistry, counters *CancelCounters, buf *MessageBuffer) {
	for {
		job := q.Pop()
		if job == nil {
			return
		}
		job.reply <- runPing(job, cfg, registry, counters, buf) // buf: nil unless message_buffer
	}
}

// Join returns once every worker has returned (after Close).
//
// Design: abstraction/server/worker_pool/design.md#workerpool
func (p *WorkerPool) Join() { p.wg.Wait() }

// Len is queue_len: the number of jobs waiting (pushed, not yet popped).
//
// Design: abstraction/server/worker_pool/design.md#queuelengthlog
func (q *WorkQueue) Len() uint64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return uint64(q.lenLocked())
}

// QueueSampler writes the QueueLengthLog on its own goroutine.
type QueueSampler struct {
	stop chan struct{}
	done chan struct{}
}

// StartQueueSampler starts queue_sampler. It owns out and closes it when it
// returns.
//
// Design: abstraction/server/worker_pool/design.md#queuelengthlog
func StartQueueSampler(q *WorkQueue, out io.WriteCloser, intervalMs uint64) *QueueSampler {
	s := &QueueSampler{stop: make(chan struct{}), done: make(chan struct{})}
	go s.run(q, out, intervalMs)
	return s
}

// nextSampleIndex is the late-wake rule: the index of the first deadline
// strictly after tMs (integer division), so a late wake writes one row only.
//
// Design: abstraction/server/worker_pool/design.md#queuelengthlog
func nextSampleIndex(tMs, intervalMs uint64) uint64 {
	return tMs/intervalMs + 1
}

// run is the sampler loop: drift-free deadlines k*interval from the start,
// one row per wake, no row once stop is set. time.Time carries a monotonic
// reading, so time.Since is monotonic.
//
// Design: abstraction/server/worker_pool/design.md#queuelengthlog
func (s *QueueSampler) run(q *WorkQueue, out io.WriteCloser, intervalMs uint64) {
	defer close(s.done)
	start := time.Now()
	w := bufio.NewWriter(out)
	_, err := w.WriteString("t_ms,queue_len\n")
	var k uint64
	for err == nil {
		wait := time.Until(start.Add(time.Duration(k*intervalMs) * time.Millisecond))
		timer := time.NewTimer(wait)
		select {
		case <-s.stop:
			timer.Stop()
			s.finish(w, out, nil)
			return
		case <-timer.C:
		}
		select { // stop set at the deadline: no extra row
		case <-s.stop:
			s.finish(w, out, nil)
			return
		default:
		}
		tMs := uint64(time.Since(start) / time.Millisecond)
		_, err = fmt.Fprintf(w, "%d,%d\n", tMs, q.Len())
		k = nextSampleIndex(tMs, intervalMs)
	}
	s.finish(w, out, err) // write failed: stop sampling, keep serving
}

func (s *QueueSampler) finish(w *bufio.Writer, out io.WriteCloser, err error) {
	if err == nil {
		err = w.Flush()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "queue log write failed:", err)
	}
}

// Stop is sampler_stop: it returns once the sampler has closed its file.
//
// Design: abstraction/server/worker_pool/design.md#queuelengthlog
func (s *QueueSampler) Stop() {
	close(s.stop)
	<-s.done
}
