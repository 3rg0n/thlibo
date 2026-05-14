package queue

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A8: with max-waiting = 10 and one active job, submitting 12 jobs
// results in exactly 1 active, 10 queued, and the 12th returns ErrFull
// immediately. We enforce this with a blocking job that holds the
// worker busy while we enqueue.
func TestQueueCapacityMatchesSpec(t *testing.T) {
	q := New(10)
	defer q.Close()

	active := make(chan struct{})     // closes when the first job is running
	release := make(chan struct{})    // unblocks the first job
	firstDone := make(chan struct{})

	first := &Job{
		Ctx: context.Background(),
		Run: func(ctx context.Context) {
			close(active)
			<-release
		},
	}
	if err := q.Submit(first); err != nil {
		t.Fatalf("submit first: %v", err)
	}
	go func() {
		<-first.Done
		close(firstDone)
	}()

	// Wait until first is definitely running.
	select {
	case <-active:
	case <-time.After(time.Second):
		t.Fatal("first job never became active")
	}

	// Now fill the waiting queue with 10 jobs.
	queued := make([]*Job, 0, 10)
	for i := 0; i < 10; i++ {
		j := &Job{Ctx: context.Background(), Run: func(ctx context.Context) {}}
		if err := q.Submit(j); err != nil {
			t.Fatalf("submit waiting job %d: %v", i, err)
		}
		queued = append(queued, j)
	}

	if got := q.Len(); got != 10 {
		t.Errorf("queue depth = %d, want 10", got)
	}

	// One more submit must fail immediately. Measure the wall clock to
	// be sure Submit didn't block even for a moment.
	overflow := &Job{Ctx: context.Background(), Run: func(ctx context.Context) {}}
	start := time.Now()
	err := q.Submit(overflow)
	elapsed := time.Since(start)
	if !errors.Is(err, ErrFull) {
		t.Errorf("overflow submit err = %v, want ErrFull", err)
	}
	if elapsed > 50*time.Millisecond {
		t.Errorf("Submit blocked for %s; spec says it must not block", elapsed)
	}

	// Release the first job. All 10 queued jobs should then complete.
	close(release)
	<-firstDone
	for i, j := range queued {
		select {
		case <-j.Done:
		case <-time.After(2 * time.Second):
			t.Fatalf("queued job %d did not run", i)
		}
		if j.Dropped() {
			t.Errorf("queued job %d was dropped (expected to run)", i)
		}
	}
}

// A9: cancelling a job's context before it runs causes it to be
// dropped. The worker must still advance to the next job.
func TestQueueCancelBeforeRun(t *testing.T) {
	q := New(5)
	defer q.Close()

	// Block worker with a first job so the cancellable one sits in queue.
	release := make(chan struct{})
	first := &Job{Ctx: context.Background(), Run: func(ctx context.Context) { <-release }}
	_ = q.Submit(first)

	ctx, cancel := context.WithCancel(context.Background())
	var ran atomic.Bool
	victim := &Job{
		Ctx: ctx,
		Run: func(ctx context.Context) { ran.Store(true) },
	}
	_ = q.Submit(victim)

	cancel()
	close(release)

	<-victim.Done
	if ran.Load() {
		t.Error("cancelled job ran anyway")
	}
	if !victim.Dropped() {
		t.Error("cancelled job was not marked Dropped")
	}
}

// A9: context cancellation while a job is actively running reaches
// Run via ctx.Done().
func TestQueueCancelDuringRun(t *testing.T) {
	q := New(5)
	defer q.Close()

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	var observed atomic.Bool
	job := &Job{
		Ctx: ctx,
		Run: func(ctx context.Context) {
			close(started)
			select {
			case <-ctx.Done():
				observed.Store(true)
			case <-time.After(3 * time.Second):
			}
		},
	}
	_ = q.Submit(job)

	<-started
	cancelStart := time.Now()
	cancel()
	<-job.Done
	if time.Since(cancelStart) > 500*time.Millisecond {
		t.Errorf("job took %s to observe cancellation; A9 requires <500ms",
			time.Since(cancelStart))
	}
	if !observed.Load() {
		t.Error("running job did not observe ctx.Done()")
	}
}

// A8 again: the worker invariant is "exactly one running at any time".
// Stress test: fire 50 jobs that each increment a running counter on
// entry and decrement on exit. The counter must never exceed 1.
func TestQueueSingleActive(t *testing.T) {
	q := New(50)
	defer q.Close()

	var running, max atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		j := &Job{
			Ctx: context.Background(),
			Run: func(ctx context.Context) {
				defer wg.Done()
				n := running.Add(1)
				for {
					cur := max.Load()
					if n <= cur || max.CompareAndSwap(cur, n) {
						break
					}
				}
				time.Sleep(2 * time.Millisecond)
				running.Add(-1)
			},
		}
		if err := q.Submit(j); err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
	}
	wg.Wait()
	if max.Load() > 1 {
		t.Errorf("max concurrent = %d, want 1", max.Load())
	}
}

// Close drains any remaining jobs so no caller blocks forever on Done.
func TestQueueCloseDrains(t *testing.T) {
	q := New(5)

	// Fill the queue with a blocker in front so the others sit waiting.
	release := make(chan struct{})
	first := &Job{Ctx: context.Background(), Run: func(ctx context.Context) { <-release }}
	_ = q.Submit(first)
	waiting := make([]*Job, 0, 3)
	for i := 0; i < 3; i++ {
		j := &Job{Ctx: context.Background(), Run: func(ctx context.Context) {}}
		_ = q.Submit(j)
		waiting = append(waiting, j)
	}

	// Close before releasing first. Worker must drain waiting jobs.
	close(release)
	q.Close()

	for i, j := range waiting {
		select {
		case <-j.Done:
		case <-time.After(500 * time.Millisecond):
			t.Errorf("waiting job %d never completed after Close", i)
		}
	}
}

// Submit after Close returns ErrShutdown.
func TestQueueSubmitAfterClose(t *testing.T) {
	q := New(2)
	q.Close()
	err := q.Submit(&Job{Ctx: context.Background(), Run: func(ctx context.Context) {}})
	if !errors.Is(err, ErrShutdown) {
		t.Errorf("Submit after Close = %v, want ErrShutdown", err)
	}
}

// #17: a single caller submitting up to callerCap jobs succeeds;
// the (callerCap+1)th returns ErrCallerFull even when the global
// queue has room.
func TestQueueCallerCapBlocksSingleNoisyCaller(t *testing.T) {
	q := NewWithCallerCap(10 /*global*/, 3 /*per caller*/)
	defer q.Close()

	// Block the worker on the first job so subsequent submissions
	// stay queued, letting us exercise the admission check.
	release := make(chan struct{})
	busy := &Job{
		Ctx:      context.Background(),
		CallerID: "noisy",
		Run:      func(ctx context.Context) { <-release },
	}
	if err := q.Submit(busy); err != nil {
		t.Fatalf("Submit busy: %v", err)
	}

	// Fill the caller's quota (2 more with CallerID "noisy" =
	// total 3 = callerCap).
	for i := 0; i < 2; i++ {
		if err := q.Submit(&Job{Ctx: context.Background(), CallerID: "noisy",
			Run: func(ctx context.Context) {}}); err != nil {
			t.Fatalf("Submit filler %d: %v", i, err)
		}
	}

	// Fourth job from the same caller must fail with ErrCallerFull.
	if err := q.Submit(&Job{Ctx: context.Background(), CallerID: "noisy",
		Run: func(ctx context.Context) {}}); !errors.Is(err, ErrCallerFull) {
		t.Errorf("over-quota Submit = %v, want ErrCallerFull", err)
	}

	// A different caller still has room in the global queue.
	if err := q.Submit(&Job{Ctx: context.Background(), CallerID: "quiet",
		Run: func(ctx context.Context) {}}); err != nil {
		t.Errorf("other caller Submit = %v, want nil", err)
	}

	close(release)
}

// #17: once a caller's job completes, its slot is released so the
// caller can submit again.
func TestQueueCallerCapReleasesOnDone(t *testing.T) {
	q := NewWithCallerCap(10, 1)
	defer q.Close()

	first := &Job{Ctx: context.Background(), CallerID: "x",
		Run: func(ctx context.Context) {}}
	if err := q.Submit(first); err != nil {
		t.Fatalf("first: %v", err)
	}
	<-first.Done

	// Give the releaseOnDone goroutine a beat to run.
	for i := 0; i < 20; i++ {
		err := q.Submit(&Job{Ctx: context.Background(), CallerID: "x",
			Run: func(ctx context.Context) {}})
		if err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("caller slot never released after Done")
}

// #17: a job with empty CallerID bypasses the per-caller check so
// transports that don't support peer identification (e.g. a test
// harness) aren't artificially constrained.
func TestQueueCallerCapSkipsEmptyID(t *testing.T) {
	q := NewWithCallerCap(10, 1)
	defer q.Close()

	// Block the worker.
	release := make(chan struct{})
	if err := q.Submit(&Job{Ctx: context.Background(),
		Run: func(ctx context.Context) { <-release }}); err != nil {
		t.Fatal(err)
	}
	// Two more empty-ID jobs should still queue fine.
	for i := 0; i < 2; i++ {
		if err := q.Submit(&Job{Ctx: context.Background(),
			Run: func(ctx context.Context) {}}); err != nil {
			t.Fatalf("empty-ID job %d rejected: %v", i, err)
		}
	}
	close(release)
}

// Compile-time check that we didn't accidentally break the atomic
// import the existing tests reference.
var _ = atomic.Uint32{}

// Same for sync.
var _ = sync.Mutex{}
