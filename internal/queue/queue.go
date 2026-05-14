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

// ErrCallerFull is returned when the global queue has room but the
// per-caller quota is exhausted. Distinguishing this from ErrFull
// matters for operators diagnosing a noisy middleware: a single
// caller hitting its cap is a bug report, not a capacity-planning
// signal. See THREAT_MODEL.md finding #17.
var ErrCallerFull = errors.New("queue: caller quota exceeded")

// ErrShutdown is returned by Submit after Close has been called.
var ErrShutdown = errors.New("queue: shutdown")

// DefaultMaxPerCaller caps how many jobs a single caller (identified
// by Job.CallerID) may have queued or running at once. Prevents a
// single buggy middleware from starving others on the same user's
// daemon. Zero = no per-caller cap.
const DefaultMaxPerCaller = 4

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

	// Per-caller admission accounting. callerCap==0 disables the
	// feature. perCaller counts in-flight + queued jobs keyed by
	// Job.CallerID; it is incremented in Submit before the channel
	// send and decremented when the job's Done channel closes.
	callerCap int
	perCaller map[string]int
	callerMu  sync.Mutex
}

// Job is one unit of work. Run is called by the worker when the job
// reaches the front of the queue. The worker passes Run a context
// derived from Job.Ctx that is cancelled when the job completes; Run
// must respect ctx to honour cancellation.
//
// Done is closed by the queue after Run returns (or after the job is
// dropped before running), so callers can block on completion.
type Job struct {
	Ctx context.Context
	Run func(ctx context.Context)
	// CallerID identifies the submitter for per-caller quota
	// accounting. The daemon fills this with a peer-cred-derived
	// string (UID:PID on Unix, SID:PID on Windows, TCP tuple on
	// loopback). Empty CallerID disables the per-caller check for
	// this job. See THREAT_MODEL.md finding #17.
	CallerID string
	Done     chan struct{}

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
// defaults to 10 in the daemon. Uses DefaultMaxPerCaller for the
// per-caller cap; use NewWithCallerCap to override.
func New(maxWaiting int) *Queue {
	return NewWithCallerCap(maxWaiting, DefaultMaxPerCaller)
}

// NewWithCallerCap returns a Queue with an explicit per-caller quota.
// callerCap == 0 disables per-caller accounting entirely (matching
// v0.1 behaviour). Negative values are clamped to 0.
func NewWithCallerCap(maxWaiting, callerCap int) *Queue {
	if maxWaiting < 0 {
		maxWaiting = 0
	}
	if callerCap < 0 {
		callerCap = 0
	}
	q := &Queue{
		jobs:      make(chan *Job, maxWaiting),
		closed:    make(chan struct{}),
		callerCap: callerCap,
		perCaller: make(map[string]int),
	}
	q.wg.Add(1)
	go q.worker()
	return q
}

// Submit enqueues job. Non-blocking. Returns ErrFull if the queue
// already holds maxWaiting jobs, ErrCallerFull if the per-caller
// quota is exhausted, or ErrShutdown if Close has been called.
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

	// Per-caller check happens BEFORE the global channel send so a
	// caller who blew their quota doesn't temporarily occupy a
	// global slot.
	if q.callerCap > 0 && job.CallerID != "" {
		q.callerMu.Lock()
		if q.perCaller[job.CallerID] >= q.callerCap {
			q.callerMu.Unlock()
			return ErrCallerFull
		}
		q.perCaller[job.CallerID]++
		q.callerMu.Unlock()
	}

	select {
	case q.jobs <- job:
		// Wire the release of the per-caller counter to Done. The
		// counter is released when the worker closes Done (success,
		// drop, or drain), so it's bound to the full in-flight +
		// queued lifetime.
		if q.callerCap > 0 && job.CallerID != "" {
			go q.releaseOnDone(job)
		}
		return nil
	case <-q.closed:
		q.releaseCaller(job.CallerID)
		return ErrShutdown
	default:
		q.releaseCaller(job.CallerID)
		return ErrFull
	}
}

// releaseOnDone decrements the per-caller counter when Done fires.
func (q *Queue) releaseOnDone(job *Job) {
	<-job.Done
	q.releaseCaller(job.CallerID)
}

// releaseCaller decrements the per-caller counter. No-op if the
// feature is disabled or the caller ID is empty.
func (q *Queue) releaseCaller(callerID string) {
	if q.callerCap == 0 || callerID == "" {
		return
	}
	q.callerMu.Lock()
	if q.perCaller[callerID] > 0 {
		q.perCaller[callerID]--
	}
	if q.perCaller[callerID] == 0 {
		delete(q.perCaller, callerID)
	}
	q.callerMu.Unlock()
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
