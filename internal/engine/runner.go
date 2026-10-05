package engine

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/awhadi/blasta-perftest/internal/backpressure"
	"github.com/awhadi/blasta-perftest/internal/collector"
	"github.com/awhadi/blasta-perftest/internal/config"
)

// Runner drives one test job: it produces scheduled work, hands it through a
// bounded queue, and runs a fixed worker pool against an Executor.
//
// Design invariants (these are what keep host load bounded):
//  1. Fixed worker count. No goroutine is created per request.
//  2. Bounded queue. Work beyond the cap is rejected/dropped, never buffered.
//  3. Open-loop arrival. Requests are scheduled at a fixed rate regardless of
//     how fast the target responds, so latency includes real queueing delay and
//     we avoid coordinated omission.
type Runner struct {
	job       config.Job
	exec      Executor
	collector *collector.Collector
	queue     *backpressure.Queue

	runID string
	log   Logger
}

// Logger is the minimal logging surface the engine needs.
type Logger interface {
	Debug(msg string, args ...any)
	Warn(msg string, args ...any)
}

type nopLogger struct{}

func (nopLogger) Debug(string, ...any) {}
func (nopLogger) Warn(string, ...any)  {}

func NewRunner(runID string, job config.Job, exec Executor, col *collector.Collector, log Logger) *Runner {
	if log == nil {
		log = nopLogger{}
	}
	workers := job.Concurrency
	if workers > job.MaxWorkers {
		workers = job.MaxWorkers
	}
	if workers < 1 {
		workers = 1
	}
	queueSize := job.QueueSize
	if queueSize < workers {
		queueSize = workers
	}
	return &Runner{
		job:       job,
		exec:      exec,
		collector: col,
		queue:     backpressure.New(queueSize, backpressure.Policy(job.OnQueueFull)),
		runID:     runID,
		log:       log,
	}
}

// Run executes the job to completion. It returns when the duration elapses,
// the request count is reached, or ctx is cancelled.
func (r *Runner) Run(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	workers := r.job.Concurrency
	if workers > r.job.MaxWorkers {
		workers = r.job.MaxWorkers
	}
	if workers < 1 {
		workers = 1
	}

	// Compute planned total for progress reporting when open-loop.
	var planned int64
	if r.job.Duration > 0 && r.job.RPS > 0 {
		target := float64(r.job.RPS)
		if r.job.Ramp > 0 && r.job.Ramp < r.job.Duration {
			// A linear 0->target ramp averages half the target rate over the ramp
			// window, which is exactly what produceRamped schedules.
			rampDur := r.job.Ramp
			steadyDur := r.job.Duration - r.job.Ramp
			planned = int64(target/2*rampDur.Seconds() + target*steadyDur.Seconds())
		} else {
			planned = int64(target * r.job.Duration.Seconds())
		}
	}
	r.collector.Begin(float64(r.job.RPS), planned)

	var wg sync.WaitGroup
	start := time.Now()

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go r.worker(ctx, &wg)
	}

	// Producer: open-loop scheduler.
	r.produce(ctx, start)

	// Stop the producer, then let workers drain the queue before closing it so
	// already-scheduled work still completes instead of reporting as canceled.
	r.queue.Close()
	wg.Wait()
	cancel()

	// Drain anything left behind (defensive; workers normally consume all).
	for {
		if _, ok := r.queue.Pop(context.Background()); !ok {
			break
		}
	}
	r.collector.End()
}

// produce schedules work at the configured rate (open loop).
func (r *Runner) produce(ctx context.Context, start time.Time) {
	deadline := time.Time{}
	if r.job.Duration > 0 {
		deadline = start.Add(r.job.Duration)
	}

	// Open-loop pacing: compute each request's intended send time from a fixed
	// schedule rather than reacting to target speed. A ticker would quantize the
	// rate (and cap it at 1/tick), so we sleep until each due time instead.
	//
	// When RPS is 0 the run is closed-loop: workers simply run as fast as the
	// queue allows.
	if r.job.RPS <= 0 {
		r.produceClosedLoop(ctx, deadline)
		return
	}

	if r.job.Ramp > 0 && (deadline.IsZero() || r.job.Ramp < r.job.Duration) {
		r.produceRamped(ctx, start, deadline)
		return
	}

	interval := time.Duration(float64(time.Second) / float64(r.job.RPS))
	if interval <= 0 {
		interval = time.Nanosecond
	}
	base := start
	for i := 0; ; i++ {
		if !deadline.IsZero() {
			if d := deadline.Sub(base); time.Duration(i)*interval >= d {
				return
			}
		}
		due := base.Add(time.Duration(i) * interval)
		if err := sleepUntil(ctx, due); err != nil {
			return
		}
		if !r.enqueue(ctx, due) {
			return
		}
	}
}

// produceRamped schedules a linear ramp from zero up to the configured RPS over
// job.Ramp, then holds the target rate for the remainder of the run.
//
// Due times come from the closed-form inverse of the cumulative request count,
// so the schedule neither drifts nor depends on a fixed step:
//
//	ramp phase   (t <= ramp):  N(t) = rMax * t^2 / (2*ramp)
//	steady phase (t >  ramp):  N(t) = rampCount + rMax * (t - ramp)
//
// Inverting gives t_i = sqrt(2*ramp*i/rMax) during the ramp and a constant
// interval afterwards.
func (r *Runner) produceRamped(ctx context.Context, start time.Time, deadline time.Time) {
	rMax := float64(r.job.RPS)
	if rMax <= 0 {
		r.produceClosedLoop(ctx, deadline)
		return
	}
	ramp := r.job.Ramp.Seconds()
	if ramp <= 0 {
		ramp = 1
	}
	// Requests emitted across the ramp phase.
	rampCount := rMax * ramp / 2

	base := start
	for i := 0; ; i++ {
		fi := float64(i)
		var off float64
		if fi <= rampCount {
			off = math.Sqrt(2 * ramp * fi / rMax)
		} else {
			off = ramp + (fi-rampCount)/rMax
		}
		due := base.Add(time.Duration(off * float64(time.Second)))
		if !deadline.IsZero() && !due.Before(deadline) {
			return
		}
		if err := sleepUntil(ctx, due); err != nil {
			return
		}
		if !r.enqueue(ctx, due) {
			return
		}
	}
}

// requestFor builds the request template handed to the executor. expectStatus is
// injected into Meta so executors can classify acceptable status codes without
// needing the whole job.
func (r *Runner) requestFor(due time.Time) Request {
	meta := r.job.Target.Meta
	if len(r.job.ExpectStatus) > 0 {
		if meta == nil {
			meta = make(map[string]any, 1)
		}
		// []int so the executor's native path handles it; JSON-sourced jobs
		// arrive as []any of float64 and are converted there instead.
		meta["expectStatus"] = append([]int(nil), r.job.ExpectStatus...)
	}
	return Request{
		Method:  r.job.Method,
		URL:     r.job.Target.URL,
		Headers: r.job.Headers,
		Body:    []byte(r.job.Body),
		Meta:    meta,
	}
}

// enqueue submits one scheduled item at the given send time. It returns false
// when the context is cancelled and the producer should stop.
func (r *Runner) enqueue(ctx context.Context, due time.Time) bool {
	item := &workItem{
		req:         r.requestFor(due),
		scheduledAt: due,
	}
	if err := r.queue.Push(ctx, item); err != nil {
		r.collector.AddSkipped(1)
		if ctx.Err() != nil {
			return false
		}
	}
	return true
}

// produceClosedLoop enqueues as fast as the queue accepts when RPS is unset.
func (r *Runner) produceClosedLoop(ctx context.Context, deadline time.Time) {
	for {
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			return
		}
		select {
		case <-ctx.Done():
			return
		default:
		}
		if !r.enqueue(ctx, time.Now()) {
			return
		}
		if ctx.Err() != nil {
			return
		}
		// Yield so a saturated queue cannot spin the CPU.
		time.Sleep(time.Millisecond)
	}
}

// sleepUntil waits until t or ctx cancellation, using a timer so the
// producer never busy-waits.
func sleepUntil(ctx context.Context, t time.Time) error {
	d := time.Until(t)
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// workItem is one scheduled unit of work.
type workItem struct {
	req         Request
	scheduledAt time.Time
}

// worker consumes from the queue and executes requests.
func (r *Runner) worker(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		v, ok := r.queue.Pop(ctx)
		if !ok {
			return
		}
		item, ok := v.(*workItem)
		if !ok {
			continue
		}

		sentAt := time.Now()
		res, err := r.exec.Do(ctx, item.req)
		res.ScheduledAt = item.scheduledAt
		res.WaitTime = sentAt.Sub(item.scheduledAt)
		if err != nil && res.Err == nil {
			res.Err = err
		}
		if res.Duration == 0 && !res.End.IsZero() && !res.Start.IsZero() {
			res.Duration = res.End.Sub(res.Start)
		}
		r.collector.Add(res)

		if ctx.Err() != nil {
			// Drain remaining and exit.
			return
		}
	}
}
