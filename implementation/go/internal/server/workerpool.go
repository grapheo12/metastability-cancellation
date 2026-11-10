// Source: abstraction/server/worker_pool/design.md (worker_pool v0.1.0)
package server

import (
	"errors"
	"sync"
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
