package server

import (
	"bufio"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/awhadi/blasta-perftest/internal/bootstrap"
	"github.com/awhadi/blasta-perftest/internal/config"
)

func testJobForAPI() config.Job {
	j := config.DefaultJob()
	j.Name = "api test"
	j.Executor = "http"
	j.Target = config.Target{URL: "http://127.0.0.1:1/"}
	j.Concurrency = 2
	j.RPS = 20
	j.Duration = 400 * time.Millisecond
	j.Timeout = 500 * time.Millisecond
	j.BlockPrivate = false
	j.OnQueueFull = "reject"
	return j
}

// post sends a JSON body the way the UI does, with the required marker header.
func post(t *testing.T, a *API, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("X-Requested-With", "blasta")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)
	return rec
}

func testAPI(t *testing.T) (*API, *Manager) {
	t.Helper()
	bootstrap.Register()
	mgr := NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)))
	return NewAPI(mgr, slog.New(slog.NewTextHandler(io.Discard, nil))), mgr
}

func TestLoopbackRefusedByDefault(t *testing.T) {
	a, _ := testAPI(t)
	body := `{"name":"x","executor":"http","target":{"url":"http://169.254.169.254/"},
	          "concurrency":1,"rps":1,"duration":1000000000,"timeout":1000000000,
	          "queueSize":1,"maxWorkers":1,"blockPrivate":true}`
	if rec := post(t, a, "/api/jobs", body); rec.Code != 400 {
		t.Fatalf("expected 400 for metadata target, got %d (%s)", rec.Code, rec.Body)
	}
}

func TestJobLifecycleAndRun(t *testing.T) {
	a, _ := testAPI(t)
	jobBody := `{"name":"api","executor":"http","target":{"url":"http://127.0.0.1:1/"},
	            "concurrency":2,"rps":20,"duration":300000000,"timeout":500000000,
	            "queueSize":100,"maxWorkers":10,"onQueueFull":"reject","blockPrivate":false}`
	rec := post(t, a, "/api/jobs", jobBody)
	if rec.Code != 201 {
		t.Fatalf("create job: %d %s", rec.Code, rec.Body)
	}
	var job struct{ ID string }
	json.Unmarshal(rec.Body.Bytes(), &job)
	if job.ID == "" {
		t.Fatal("job id empty")
	}

	rec = post(t, a, "/api/jobs/"+job.ID+"/start", "")
	if rec.Code != 202 {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	var run struct{ ID string }
	json.Unmarshal(rec.Body.Bytes(), &run)

	// Wait for completion.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rec = httptest.NewRecorder()
		a.ServeHTTP(rec, httptest.NewRequest("GET", "/api/runs/"+run.ID, nil))
		var r struct{ State string }
		json.Unmarshal(rec.Body.Bytes(), &r)
		if r.State == "finished" || r.State == "aborted" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	rec = httptest.NewRecorder()
	a.ServeHTTP(rec, httptest.NewRequest("GET", "/api/runs/"+run.ID+"/report", nil))
	if rec.Code != 200 {
		t.Fatalf("report: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"total"`) {
		t.Errorf("report missing totals: %s", rec.Body.String())
	}
}

func TestReportCSV(t *testing.T) {
	a, mgr := testAPI(t)
	job, err := mgr.SaveJob(testJobForAPI())
	if err != nil {
		t.Fatal(err)
	}
	run, err := mgr.Start(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(600 * time.Millisecond)
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, httptest.NewRequest("GET", "/api/runs/"+run.ID+"/report?format=csv", nil))
	if rec.Code != 200 {
		t.Fatalf("csv: %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/csv") {
		t.Errorf("content-type = %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "metric,value") {
		t.Errorf("csv body: %s", rec.Body.String())
	}
}

// TestStreamUnsubscribesAfterCompletion covers the double-close panic that
// occurred when a client disconnected after the run had already finished.
func TestStreamUnsubscribesAfterCompletion(t *testing.T) {
	a, mgr := testAPI(t)
	job, err := mgr.SaveJob(testJobForAPI())
	if err != nil {
		t.Fatal(err)
	}
	run, err := mgr.Start(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(700 * time.Millisecond)

	srv := httptest.NewServer(a)
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL+"/api/runs/"+run.ID+"/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("stream request: %v", err)
	}
	sc := bufio.NewScanner(resp.Body)
	done := make(chan struct{})
	go func() {
		defer close(done)
		n := 0
		for sc.Scan() && n < 3 {
			n++
		}
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not close promptly after the run finished")
	}
	resp.Body.Close()
}

// A stopped run must never be observable as finished-with-no-results: the
// summary is published at stop time so a client polling immediately sees the
// real numbers and the aborted flag.
func TestStopPublishesSummaryImmediately(t *testing.T) {
	_, mgr := testAPI(t)
	job, err := mgr.SaveJob(testJobForAPI())
	if err != nil {
		t.Fatal(err)
	}
	job.Duration = 30 * time.Second
	job.RPS = 100
	if _, err := mgr.SaveJob(job); err != nil {
		t.Fatal(err)
	}
	run, err := mgr.Start(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(700 * time.Millisecond)
	if err := mgr.Stop(run.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	v := run.view()
	if v.State != "aborted" {
		t.Errorf("state = %q, want aborted", v.State)
	}
	if v.Summary.Total == 0 {
		t.Error("summary.total is 0 immediately after stop; client would see an empty result")
	}
	if !v.Summary.Aborted {
		t.Error("summary.aborted = false after stop")
	}

	// The graceful drain then finalises the run, still as aborted.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if run.view().EndedAt != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	fin := run.view()
	if fin.State != "aborted" {
		t.Errorf("final state = %q, want aborted (must not be relabelled finished)", fin.State)
	}
	if fin.Summary.Total == 0 {
		t.Error("final summary has no results")
	}
}

func TestStopTwiceIsRejected(t *testing.T) {
	_, mgr := testAPI(t)
	job, _ := mgr.SaveJob(testJobForAPI())
	job.Duration = 30 * time.Second
	job.RPS = 50
	job, _ = mgr.SaveJob(job)
	run, err := mgr.Start(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(600 * time.Millisecond)
	if err := mgr.Stop(run.ID); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	if err := mgr.Stop(run.ID); err == nil {
		t.Error("expected error stopping an already stopped run")
	}
}

// The control plane is reachable from any page the user visits, so state-changing
// requests must carry the marker header and match the origin.
func TestCrossOriginAndHeaderGuards(t *testing.T) {
	a, _ := testAPI(t)
	body := `{"name":"x","executor":"http","target":{"url":"http://127.0.0.1:1/"},
	          "concurrency":1,"rps":1,"duration":100000000,"timeout":100000000,
	          "queueSize":1,"maxWorkers":1,"blockPrivate":false}`

	// No marker header.
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, httptest.NewRequest("POST", "/api/jobs", strings.NewReader(body)))
	if rec.Code != 403 {
		t.Errorf("POST without marker = %d, want 403", rec.Code)
	}

	// Marker present but hostile Origin.
	req := httptest.NewRequest("POST", "/api/jobs", strings.NewReader(body))
	req.Header.Set("X-Requested-With", "blasta")
	req.Header.Set("Origin", "https://evil.example")
	rec = httptest.NewRecorder()
	a.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Errorf("cross-origin POST = %d, want 403", rec.Code)
	}

	// Same-origin with the marker is allowed.
	req = httptest.NewRequest("POST", "/api/jobs", strings.NewReader(body))
	req.Header.Set("X-Requested-With", "blasta")
	req.Header.Set("Origin", "http://"+req.Host)
	rec = httptest.NewRecorder()
	a.ServeHTTP(rec, req)
	if rec.Code != 201 {
		t.Errorf("same-origin POST = %d, want 201 (%s)", rec.Code, rec.Body)
	}

	// GET stays open so curl and same-origin reads keep working.
	rec = httptest.NewRecorder()
	a.ServeHTTP(rec, httptest.NewRequest("GET", "/api/runs", nil))
	if rec.Code != 200 {
		t.Errorf("GET /api/runs = %d, want 200", rec.Code)
	}
}

func TestDefaultsAppliedForPartialJob(t *testing.T) {
	a, _ := testAPI(t)
	rec := post(t, a, "/api/jobs",
		`{"name":"partial","executor":"http","target":{"url":"http://127.0.0.1:1/"},"blockPrivate":false}`)
	if rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var j config.Job
	json.Unmarshal(rec.Body.Bytes(), &j)
	if j.Concurrency == 0 || j.Duration == 0 || j.Timeout == 0 {
		t.Errorf("defaults not applied: concurrency=%d duration=%d timeout=%d", j.Concurrency, j.Duration, j.Timeout)
	}
	if j.Method != "GET" {
		t.Errorf("method = %q, want GET", j.Method)
	}
}

// DefaultBlockPrivate must reject a link-local target when the document is
// silent, so an omitted flag cannot be used to slip past the SSRF guard.
func TestDefaultBlockPrivateGuardsTargets(t *testing.T) {
	a, _ := testAPI(t)
	rec := post(t, a, "/api/jobs",
		`{"name":"guarded","executor":"http","target":{"url":"http://169.254.169.254/"}}`)
	if rec.Code != 400 {
		t.Errorf("metadata target accepted with %d, want 400", rec.Code)
	}
}

func TestReportFilenameIsSanitised(t *testing.T) {
	a, mgr := testAPI(t)
	job, _ := mgr.SaveJob(testJobForAPI())
	run, _ := mgr.Start(job.ID)
	time.Sleep(500 * time.Millisecond)
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, httptest.NewRequest("GET", "/api/runs/"+run.ID+"/report?format=csv", nil))
	cd := rec.Header().Get("Content-Disposition")
	if strings.ContainsAny(cd, "\r\n") || !strings.HasPrefix(cd, "attachment; filename=") {
		t.Errorf("Content-Disposition = %q", cd)
	}
}
