// Source: abstraction/server/worker_pool/design.md (worker_pool v0.2.0)
package server

import (
	"bufio"
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
	reply     chan Response
}

func newPingJob(task *StreamTask, requestID uint64, clientID uint32, serviceUs uint64) *PingJob {
	return &PingJob{Task: task, RequestID: requestID, ClientID: clientID, ServiceUs: serviceUs, reply: make(chan Response, 1)}
}

// Reply blocks until the job's result is available (OneShot take).
func (j *PingJob) Reply() Response { return <-j.reply }

// WorkQueue is the process-wide unbounded MPMC FIFO: one mutex, one condition
// variable and a slice used as a FIFO (Go channels are bounded).
//
// Design: abstraction/server/worker_pool/design.md#workqueue
type WorkQueue struct {
	mu       sync.Mutex
	notEmpty *sync.Cond
	items    []*PingJob
	closed   bool
}

// NewWorkQueue returns an empty, open queue.
//
// Design: abstraction/server/worker_pool/design.md#workqueue
func NewWorkQueue() *WorkQueue {
	q := &WorkQueue{}
	q.notEmpty = sync.NewCond(&q.mu)
	return q
}

// Push appends job at the tail. It never blocks; after Close it returns
// ErrQueueClosed and leaves job.reply untouched.
//
// Design: abstraction/server/worker_pool/design.md#workqueue
func (q *WorkQueue) Push(job *PingJob) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return ErrQueueClosed
	}
	q.items = append(q.items, job)
	q.notEmpty.Signal()
	return nil
}

// Pop removes and returns the head job, waiting while the queue is empty and
// open. It returns nil once the queue is closed (closed wins over items).
//
// Design: abstraction/server/worker_pool/design.md#workqueue
func (q *WorkQueue) Pop() *PingJob {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) == 0 && !q.closed {
		q.notEmpty.Wait()
	}
	if q.closed {
		return nil
	}
	job := q.items[0]
	q.items[0] = nil // let the GC reclaim the job once answered
	q.items = q.items[1:]
	return job
}

// Close closes the queue, answers every queued job Aborted in FIFO order
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
	dropped := q.items
	q.items = nil
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
func StartWorkerPool(n uint32, q *WorkQueue, cfg Config, registry *CancelRegistry, counters *CancelCounters) *WorkerPool {
	p := &WorkerPool{}
	for w := uint32(0); w < n; w++ {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			workerLoop(q, cfg, registry, counters)
		}()
	}
	return p
}

// workerLoop pops jobs in push order until the queue is closed.
//
// Design: abstraction/server/worker_pool/design.md#workerpool
// Design: abstraction/server/worker_pool/design.md#ordering
func workerLoop(q *WorkQueue, cfg Config, registry *CancelRegistry, counters *CancelCounters) {
	for {
		job := q.Pop()
		if job == nil {
			return
		}
		job.reply <- runPing(job, cfg, registry, counters)
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
	return uint64(len(q.items))
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
