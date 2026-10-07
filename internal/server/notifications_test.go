package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/awhadi/blasta-perftest/internal/collector"
	"github.com/awhadi/blasta-perftest/internal/config"
	"github.com/awhadi/blasta-perftest/internal/notify"
)

func TestEventOfJudgesARunByItsTargets(t *testing.T) {
	five := 5.0
	v := RunView{ID: "r1", JobName: "J", Target: "https://x.test", State: "finished",
		Summary: collector.Summary{Total: 1000, Errors: 100, AvgRPS: 50, DurationMs: 30000},
		Plan:    &RunPlan{SLO: &config.SLO{MaxErrorRate: &five, MaxP95: 200 * time.Millisecond}}}
	v.Summary.Latency.Percentiles = map[string]float64{"p50": 10000, "p95": 350000, "p99": 900000}
	e := eventOf(v, "https://s.test/history/r1")
	if !e.NeedsAttention() || !e.HasTargets || len(e.Problems) != 2 {
		t.Fatalf("both targets are missed: %+v", e.Problems)
	}
	if e.P95ms != 350 || e.ErrorRatePct != 10 || !strings.HasPrefix(e.Headline(), "Needs attention") {
		t.Errorf("numbers: %+v", e)
	}
	// Within the targets: a pass.
	ten := 10.0
	v.Plan.SLO = &config.SLO{MaxErrorRate: &ten, MaxP95: time.Second}
	if e := eventOf(v, ""); e.NeedsAttention() || !strings.HasPrefix(e.Headline(), "Passed") {
		t.Errorf("within targets: %+v", e.Problems)
	}
	// No targets: one failure in a hundred is a problem; a user's own stop is not.
	v.Plan = nil
	if e := eventOf(v, ""); !e.NeedsAttention() {
		t.Error("10% failing with no targets should need attention")
	}
	v.Summary.Errors = 0
	v.State, v.Reason = "aborted", "stopped by user"
	if e := eventOf(v, ""); e.NeedsAttention() {
		t.Errorf("a person stopping their own run is not a problem: %v", e.Problems)
	}
	v.State, v.Reason = "error", "target unreachable"
	if e := eventOf(v, ""); !e.NeedsAttention() || !strings.Contains(e.Problems[0], "unreachable") {
		t.Errorf("a failed run: %v", e.Problems)
	}
}

func TestAdministratorSetsHowFinishedTestsAreAnnounced(t *testing.T) {
	notify.AllowPrivate = true
	defer func() { notify.AllowPrivate = false }()
	srv, _ := siteWithDB(t)
	admin := newVisitor(t, srv.URL)
	if code, _, b := admin.do("POST", "/api/auth/register", `{"email":"admin@example.test","name":"A","password":"correct horse battery"}`); code != 201 {
		t.Fatalf("register: %d %s", code, b)
	}
	// An ordinary person, who never sets anything up.
	pat := newVisitor(t, srv.URL)
	if code, _, b := pat.do("POST", "/api/auth/register", `{"email":"pat@example.test","name":"Pat","password":"correct horse battery"}`); code != 201 {
		t.Fatalf("register: %d %s", code, b)
	}
	got := make(chan map[string]any, 4)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		got <- m
	}))
	defer hook.Close()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer target.Close()

	// Only an administrator can set it; people have nothing to set.
	save := `{"enabled":true,"on":"always","webhook":"` + hook.URL + `/secret-path-token"}`
	if code, _, _ := newVisitor(t, srv.URL).do("POST", "/api/admin/settings/notifications", save); code != 401 {
		t.Errorf("anonymous = %d, want 401", code)
	}
	if code, _, _ := pat.do("POST", "/api/admin/settings/notifications", save); code != 403 {
		t.Errorf("an ordinary person = %d, want 403", code)
	}
	if code, _, _ := pat.do("GET", "/api/me/notifications", ""); code != 404 {
		t.Errorf("people have no notification settings of their own any more: %d", code)
	}
	for name, body := range map[string]string{
		"bad mode":              `{"enabled":true,"on":"sometimes","emailRunner":true}`,
		"not an email":          `{"enabled":true,"on":"always","emailTo":["not an email"]}`,
		"http address":          `{"enabled":true,"on":"always","slack":"ftp://x.example.test/a"}`,
		"address with a space":  `{"enabled":true,"on":"always","webhook":"https://x y"}`,
		"on with nowhere to go": `{"enabled":true,"on":"always"}`,
	} {
		if code, _, _ := admin.do("POST", "/api/admin/settings/notifications", body); code != 400 {
			t.Errorf("%s = %d, want 400", name, code)
		}
	}
	if code, _, b := admin.do("POST", "/api/admin/settings/notifications", save); code != 200 {
		t.Fatalf("save: %d %s", code, b)
	}
	_, _, v := admin.do("GET", "/api/admin/settings", "")
	if strings.Contains(v, "secret-path-token") || !strings.Contains(v, `"webhook":{"set":true`) || !strings.Contains(v, `"notifications":{`) {
		t.Errorf("the settings page shows where, never the address: %s", v)
	}

	run := func(who *visitor) {
		code, _, jb := who.do("POST", "/api/jobs", `{"name":"Ping","executor":"http","target":{"url":"`+target.URL+`/"},"concurrency":1,"rps":5,"duration":600000000,"timeout":300000000,"blockPrivate":false}`)
		if code != 201 {
			t.Fatalf("job: %d %s", code, jb)
		}
		if code, _, rb := who.do("POST", "/api/jobs/"+idOf(t, jb)+"/start", ""); code != 202 {
			t.Fatalf("start: %d %s", code, rb)
		}
	}
	// Pat starts a test and sets nothing up: the channel hears about it, naming Pat.
	run(pat)
	select {
	case m := <-got:
		r, _ := m["run"].(map[string]any)
		if m["event"] != "run.finished" || r["job"] != "Ping" || r["startedBy"] != "pat@example.test" || r["needsAttention"] != false || !strings.Contains(m["url"].(string), "/history/run_") {
			t.Errorf("payload: %v", m)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("no notification arrived")
	}
	// "Only when something needs attention": a clean run is silent.
	if code, _, b := admin.do("POST", "/api/admin/settings/notifications", `{"enabled":true,"on":"problems","emailRunner":false}`); code != 200 {
		t.Fatalf("problems only: %d %s", code, b)
	}
	run(pat)
	select {
	case m := <-got:
		t.Fatalf("a clean run must not be announced when only problems are wanted: %v", m)
	case <-time.After(2500 * time.Millisecond):
	}
	// The test button sends a sample to the saved channels, and the last delivery is shown.
	if code, _, b := admin.do("POST", "/api/admin/settings/notifications/test", ""); code != 200 || !strings.Contains(b, `"channel":"webhook","ok":true`) {
		t.Errorf("test: %d %s", code, b)
	}
	select {
	case m := <-got:
		if r, _ := m["run"].(map[string]any); !strings.Contains(r["job"].(string), "sample") {
			t.Errorf("the test message should say it is a sample: %v", m)
		}
	case <-time.After(5 * time.Second):
		t.Error("the test message did not arrive")
	}
	if _, _, v = admin.do("GET", "/api/admin/settings", ""); !strings.Contains(v, "webhook sent") {
		t.Errorf("the settings page should show the last delivery: %s", v)
	}
	if code, _, _ := pat.do("POST", "/api/admin/settings/notifications/test", ""); code != 403 {
		t.Errorf("only an administrator may send a test: %d", code)
	}
	// Clearing the address and turning it off.
	if code, _, b := admin.do("POST", "/api/admin/settings/notifications", `{"enabled":false,"on":"problems","webhook":""}`); code != 200 {
		t.Fatalf("clear: %d %s", code, b)
	}
	if _, _, v = admin.do("GET", "/api/admin/settings", ""); !strings.Contains(v, `"webhook":{"set":false}`) {
		t.Errorf("cleared: %s", v)
	}
	if code, _, _ := admin.do("POST", "/api/admin/settings/notifications/test", ""); code != 400 {
		t.Errorf("nothing set up = %d, want 400", code)
	}
}
