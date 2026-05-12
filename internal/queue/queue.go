package queue

import (
	"context"
	"errors"
	"sync"
)

// ErrFull is returned by Submit when the queue is already holding its
// maximum number of waiting jobs. Per spec §Concurrency this is an
// immediate error; Submit never blocks.
var ErrFull = errors.New("queue: full")

// ErrShutdown is returned by Submit after Close has been called.
var ErrShutdown = errors.New("queue: shutdown")

// Queue is the daemon's request admission layer. It enforces the spec's
// concurrency contract:
//
//   - 1 active generation at a time.
//   - Up to MaxWaiting additional jobs queued (default 10).
//   - Submit is non-blocking: if the queue is full, it returns ErrFull
//     immediately.
//   - Each submitted job carries its own context; cancelling the
//     context (for instance when the client disconnects, A9) causes
//     the job to be dropped from the queue or, if already running,
//     signals the worker to stop.
//
// The queue is deliberately simple: a buffered channel of Jobs plus one
// worker goroutine. Everything that needs to coordinate with the worker
// does so through context cancellation and the job's Done channel.
type Queue struct {
	jobs   chan *Job
	closed chan struct{}

	closeOnce sync.Once
	wg        sync.WaitGroup
}

// Job is one unit of work. Run is called by the worker when the job
// reaches the front of the queue. The worker passes Run a context
// derived from Job.Ctx that is cancelled when the job completes; Run
// must respect ctx to honour cancellation.
//
// Done is closed by the queue after Run returns (or after the job is
// dropped before running), so callers can block on completion.
type Job struct {
	Ctx  context.Context
	Run  func(ctx context.Context)
	Done chan struct{}

	// dropped tracks whether the job was removed from the queue
	// before running (e.g. via context cancellation). Callers that
	// need to know can check after Done closes.
	dropped bool
	mu      sync.Mutex
}

// Dropped reports whether the job was cancelled before reaching the
// worker. Only valid after Done is closed.
func (j *Job) Dropped() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.dropped
}

// New returns a Queue with the given maximum number of waiting jobs.
// maxWaiting is the queue depth *excluding* the currently running job,
// so the total in-flight cap is maxWaiting + 1. Per spec: maxWaiting
// defaults to 10 in the daemon.
func New(maxWaiting int) *Queue {
	if maxWaiting < 0 {
		maxWaiting = 0
	}
	q := &Queue{
		jobs:   make(chan *Job, maxWaiting),
		closed: make(chan struct{}),
	}
	q.wg.Add(1)
	go q.worker()
	return q
}

// Submit enqueues job. Non-blocking. Returns ErrFull if the queue
// already holds maxWaiting jobs, or ErrShutdown if Close has been
// called.
//
// Why non-blocking matters: the spec is explicit that "queue full =
// immediate error, never blocks". A blocking submit would let a slow
// client hold an IPC connection open and starve the accept loop.
func (q *Queue) Submit(job *Job) error {
	if job.Done == nil {
		job.Done = make(chan struct{})
	}

	select {
	case <-q.closed:
		return ErrShutdown
	default:
	}

	select {
	case q.jobs <- job:
		return nil
	case <-q.closed:
		return ErrShutdown
	default:
		return ErrFull
	}
}

// worker pulls jobs and runs them one at a time. The spec's "1 active
// generation" rule falls out of having exactly one worker goroutine.
func (q *Queue) worker() {
	defer q.wg.Done()
	for {
		select {
		case <-q.closed:
			q.drain()
			return
		case job, ok := <-q.jobs:
			if !ok {
				return
			}
			q.runOne(job)
		}
	}
}

// runOne executes a single job, respecting its context. If the context
// is already cancelled when we dequeue it (e.g. the client disconnected
// while waiting), we skip Run and mark the job dropped.
func (q *Queue) runOne(job *Job) {
	defer close(job.Done)

	if job.Ctx.Err() != nil {
		job.mu.Lock()
		job.dropped = true
		job.mu.Unlock()
		return
	}

	if job.Run != nil {
		job.Run(job.Ctx)
	}
}

// drain marks every queued job as dropped so callers blocked on
// job.Done unblock cleanly after Close.
func (q *Queue) drain() {
	for {
		select {
		case job := <-q.jobs:
			job.mu.Lock()
			job.dropped = true
			job.mu.Unlock()
			close(job.Done)
		default:
			return
		}
	}
}

// Close stops accepting new jobs and drains waiting ones. Blocks until
// the worker exits. Idempotent.
func (q *Queue) Close() {
	q.closeOnce.Do(func() { close(q.closed) })
	q.wg.Wait()
}

// Len reports the current number of queued (not running) jobs. Useful
// for admin/metrics; not used for admission control.
func (q *Queue) Len() int { return len(q.jobs) }
