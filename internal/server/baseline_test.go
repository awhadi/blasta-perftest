package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// finishedRun starts the job, waits for it to end and returns the run's id.
func finishedRun(t *testing.T, v *visitor, jobID string) string {
	t.Helper()
	code, _, rb := v.do("POST", "/api/jobs/"+jobID+"/start", "")
	if code != 202 {
		t.Fatalf("start: %d %s", code, rb)
	}
	id := idOf(t, rb)
	for i := 0; i < 100; i++ {
		_, _, g := v.do("GET", "/api/runs/"+id, "")
		if strings.Contains(g, `"state":"finished"`) {
			return id
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the run did not finish")
	return ""
}

func TestRunsOfOneTestAreRecognisedWhateverTheTime(t *testing.T) {
	a := RunView{Owner: "u", Executor: "http", Target: "https://x.test", JobName: "Manual test · x.test · 10:15 AM"}
	b := a
	b.JobName = "Manual test · x.test · 2:41 PM"
	c := a
	c.JobName = "Manual test · x.test · 14:05"
	d := a
	d.Target = "https://y.test"
	e := a
	e.JobName = "Orders API"
	if !sameTest(a, b) || !sameTest(a, c) {
		t.Error("the time of day is not part of the test")
	}
	if sameTest(a, d) || sameTest(a, e) {
		t.Error("another target or another name is another test")
	}
}

func TestBaselinesAndComparison(t *testing.T) {
	srv, _ := siteWithDB(t)
	admin := newVisitor(t, srv.URL)
	if code, _, b := admin.do("POST", "/api/auth/register", `{"email":"admin@example.test","name":"A","password":"correct horse battery"}`); code != 201 {
		t.Fatalf("register: %d %s", code, b)
	}
	other := newVisitor(t, srv.URL)
	if code, _, b := other.do("POST", "/api/auth/register", `{"email":"other@example.test","name":"O","password":"correct horse battery"}`); code != 201 {
		t.Fatalf("register: %d %s", code, b)
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer target.Close()
	_, _, jb := admin.do("POST", "/api/jobs", `{"name":"Ping","executor":"http","target":{"url":"`+target.URL+`/"},"concurrency":1,"rps":20,"duration":500000000,"timeout":300000000,"blockPrivate":false}`)
	job := idOf(t, jb)
	first, second := finishedRun(t, admin, job), finishedRun(t, admin, job)

	// Marking a baseline.
	if code, _, b := admin.do("POST", "/api/runs/"+first+"/baseline", `{"baseline":true}`); code != 200 || !strings.Contains(b, `"baseline":true`) {
		t.Fatalf("set baseline: %d %s", code, b)
	}
	if code, _, _ := other.do("POST", "/api/runs/"+first+"/baseline", `{"baseline":true}`); code != 404 {
		t.Errorf("someone else's run = %d, want 404", code)
	}
	// A new baseline for the same test replaces the old one.
	if code, _, _ := admin.do("POST", "/api/runs/"+second+"/baseline", `{"baseline":true}`); code != 200 {
		t.Fatal("second baseline")
	}
	_, _, list := admin.do("GET", "/api/runs", "")
	if strings.Count(list, `"baseline":true`) != 1 {
		t.Errorf("only one run of a test is the baseline: %s", list)
	}
	if _, _, g := admin.do("GET", "/api/runs/"+first, ""); strings.Contains(g, `"baseline":true`) {
		t.Error("the first run should have lost the mark")
	}
	if code, _, _ := admin.do("POST", "/api/runs/"+second+"/baseline", `{"baseline":false}`); code != 200 {
		t.Fatal("remove baseline")
	}

	// Comparing.
	code, _, c := admin.do("GET", "/api/runs/"+second+"/compare?to="+first+"&latency=10000&errors=5&throughput=100", "")
	if code != 200 || !strings.Contains(c, `"metrics"`) || !strings.Contains(c, `"judged":true`) || !strings.Contains(c, `"passed":true`) || !strings.Contains(c, `"key":"p95"`) {
		t.Fatalf("compare: %d %s", code, c)
	}
	if _, _, c = admin.do("GET", "/api/runs/"+second+"/compare?to="+first, ""); !strings.Contains(c, `"judged":false`) {
		t.Errorf("with no limits nothing is judged: %s", c)
	}
	// An impossible limit fails (throughput may not fall at all, yet latency may not move).
	if _, _, c = admin.do("GET", "/api/runs/"+second+"/compare?to="+first+"&latency=0&errors=0&throughput=0", ""); !strings.Contains(c, `"judged":true`) {
		t.Errorf("limits of zero still judge: %s", c)
	}
	for _, path := range []string{"/api/runs/" + second + "/compare?to=nope", "/api/runs/nope/compare?to=" + first} {
		if code, _, _ := admin.do("GET", path, ""); code != 404 {
			t.Errorf("%s = %d, want 404", path, code)
		}
	}
	if code, _, _ := other.do("GET", "/api/runs/"+second+"/compare?to="+first, ""); code != 404 {
		t.Errorf("comparing someone else's runs = %d, want 404", code)
	}
}
