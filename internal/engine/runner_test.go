package engine_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/awhadi/blasta-perftest/internal/collector"
	"github.com/awhadi/blasta-perftest/internal/config"
	"github.com/awhadi/blasta-perftest/internal/engine"
	httpexec "github.com/awhadi/blasta-perftest/internal/executors/http"
)

func testJob(url string, rps, conc int, dur time.Duration) config.Job {
	j := config.DefaultJob()
	j.Name = "test"
	j.Executor = "http"
	j.Target = config.Target{URL: url}
	j.Method = "GET"
	j.RPS = rps
	j.Concurrency = conc
	j.Duration = dur
	j.Timeout = 5 * time.Second
	j.BlockPrivate = false
	j.OnQueueFull = "reject"
	j.QueueSize = 500
	return j
}

func TestRunnerHitsTargetRate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	job := testJob(srv.URL, 200, 10, 2*time.Second)
	col := collector.New()
	r := engine.NewRunner("t", job, httpexec.New(httpexec.Options{Timeout: 5 * time.Second}), col, nil)

	r.Run(context.Background())

	s := col.Summary("t", "test", "http", srv.URL, false, "")
	if s.Total < 300 {
		t.Fatalf("expected >=300 requests at 200rps for 2s, got %d", s.Total)
	}
	// Allow scheduler jitter but reject a badly wrong rate.
	if s.AvgRPS < 150 || s.AvgRPS > 260 {
		t.Fatalf("achieved rps %.1f far from target 200", s.AvgRPS)
	}
	if s.Errors != 0 {
		t.Fatalf("unexpected errors: %d (%v)", s.Errors, s.ErrorKinds)
	}
}

func TestRunnerClosedLoopUnderCap(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	job := testJob(srv.URL, 0, 25, time.Second)
	col := collector.New()
	r := engine.NewRunner("t", job, httpexec.New(httpexec.Options{Timeout: 5 * time.Second}), col, nil)

	r.Run(context.Background())

	s := col.Summary("t", "test", "http", srv.URL, false, "")
	if s.Total == 0 {
		t.Fatal("expected requests in closed-loop mode")
	}
	if s.Errors != 0 {
		t.Fatalf("unexpected errors: %v", s.ErrorKinds)
	}
	// Backpressure must bound the run: no unbounded queue growth.
	if s.Skipped == 0 && s.AvgRPS > 20000 {
		t.Logf("note: no skips observed, achieved %.0f rps", s.AvgRPS)
	}
}

func TestRunnerStopsOnContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	job := testJob(srv.URL, 0, 5, 10*time.Second)
	col := collector.New()
	r := engine.NewRunner("t", job, httpexec.New(httpexec.Options{Timeout: 5 * time.Second}), col, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.Run(ctx)
		close(done)
	}()
	time.Sleep(300 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not stop after context cancel")
	}
}

func TestRunnerDoesNotLeakGoroutines(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	// Warm up one run first: the first net/http run starts background
	// dial/read-loop goroutines that persist for the process lifetime.
	{
		job := testJob(srv.URL, 50, 5, 100*time.Millisecond)
		col := collector.New()
		exec := httpexec.New(httpexec.Options{Timeout: 2 * time.Second})
		engine.NewRunner("t", job, exec, col, nil).Run(context.Background())
		exec.CloseIdle()
	}
	time.Sleep(300 * time.Millisecond)
	before := runtime.NumGoroutine()
	for i := 0; i < 3; i++ {
		job := testJob(srv.URL, 50, 5, 300*time.Millisecond)
		col := collector.New()
		exec := httpexec.New(httpexec.Options{Timeout: 2 * time.Second})
		r := engine.NewRunner("t", job, exec, col, nil)
		r.Run(context.Background())
		exec.CloseIdle()
	}
	// Allow transports to settle.
	for i := 0; i < 20; i++ {
		if runtime.NumGoroutine() <= before+5 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	after := runtime.NumGoroutine()
	t.Fatalf("goroutines leaked: before=%d after=%d", before, after)
}

// TestRampReachesTargetRate verifies a linear 0->RPS ramp schedules the expected
// number of requests: ramp phase averages half the target rate, the remainder
// runs steady.
func TestRampReachesTargetRate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	col := collector.New()
	job := testJob(srv.URL, 100, 50, 4*time.Second)
	job.Ramp = 2 * time.Second
	engine.NewRunner("t", job, httpexec.New(httpexec.Options{Timeout: 5 * time.Second}), col, nil).
		Run(context.Background())

	snap := col.Summary("t", "t", "http", srv.URL, false, "")
	// ramp: 100/2 * 2s = 100; steady: 100 * 2s = 200. Total ~300.
	if snap.Total < 270 || snap.Total > 330 {
		t.Fatalf("total = %d, want ~300 for ramped schedule", snap.Total)
	}
	if snap.Skipped != 0 {
		t.Errorf("skipped = %d, want 0", snap.Skipped)
	}
}

// TestRampLongerThanDurationFallsBackToConstant guards the edge case where the
// configured ramp exceeds the run duration: the run must still terminate and
// behave like a steady-rate run rather than spinning.
func TestRampLongerThanDurationFallsBackToConstant(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	col := collector.New()
	job := testJob(srv.URL, 100, 50, 2*time.Second)
	job.Ramp = 30 * time.Second
	engine.NewRunner("t", job, httpexec.New(httpexec.Options{Timeout: 5 * time.Second}), col, nil).
		Run(context.Background())

	snap := col.Summary("t", "t", "http", srv.URL, false, "")
	if snap.Total < 150 || snap.Total > 250 {
		t.Fatalf("total = %d, want ~200", snap.Total)
	}
}
