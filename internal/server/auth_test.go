package server

import (
	"encoding/json"
	"github.com/awhadi/blasta-perftest/internal/db"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/awhadi/blasta-perftest/internal/auth"
	"github.com/awhadi/blasta-perftest/internal/bootstrap"
	"github.com/awhadi/blasta-perftest/internal/settings"
)

// authEnv is a real HTTP server with sign-in on and a client per person.
type authEnv struct {
	t   *testing.T
	srv *httptest.Server
	svc *auth.Service
	mgr *Manager
}

func newAuthEnv(t *testing.T) *authEnv {
	return newAuthEnvWith(t, auth.Config{Registration: auth.RegOpen})
}

func newAuthEnvWith(t *testing.T, cfg auth.Config) *authEnv {
	t.Helper()
	bootstrap.Register()
	d, _ := db.OpenMemory()
	t.Cleanup(func() { d.Close() })
	st := auth.NewStore(d)
	svc, err := auth.New(st, cfg)
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)))
	api := NewAPI(mgr, slog.New(slog.NewTextHandler(io.Discard, nil)), WithAuth(svc))
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	return &authEnv{t: t, srv: srv, svc: svc, mgr: mgr}
}

type person struct {
	e *authEnv
	c *http.Client
}

func (e *authEnv) signUp(email string) *person {
	e.t.Helper()
	jar, _ := cookiejar.New(nil)
	p := &person{e: e, c: &http.Client{Jar: jar}}
	code, body := p.do("POST", "/api/auth/register", `{"email":"`+email+`","password":"correct horse battery"}`)
	if code != 201 {
		e.t.Fatalf("register %s: %d %s", email, code, body)
	}
	return p
}

func (p *person) do(method, path, body string) (int, string) {
	req, _ := http.NewRequest(method, p.e.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "blasta")
	resp, err := p.c.Do(req)
	if err != nil {
		p.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func jobBody() string {
	return `{"name":"t","executor":"http","target":{"url":"http://127.0.0.1:1/"},"concurrency":1,"rps":5,"duration":300000000,"timeout":300000000,"blockPrivate":false}`
}

func idOf(t *testing.T, body string) string {
	t.Helper()
	var m struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &m); err != nil || m.ID == "" {
		t.Fatalf("no id in %s", body)
	}
	return m.ID
}

func TestEverythingNeedsSignInWhenGuestsAreOff(t *testing.T) {
	off := settings.DefaultGuest()
	off.Enabled = false
	e := newAuthEnvWith(t, auth.Config{Registration: auth.RegOpen, Guest: off})
	anon := &person{e: e, c: &http.Client{}}
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/jobs"}, {"GET", "/api/runs"}, {"GET", "/api/executors"},
		{"POST", "/api/jobs"}, {"GET", "/api/runs/run_x/report"}, {"GET", "/api/admin/users"},
	} {
		if code, _ := anon.do(tc.method, tc.path, jobBody()); code != 401 {
			t.Errorf("%s %s without a session = %d, want 401", tc.method, tc.path, code)
		}
	}
	for _, path := range []string{"/api/health", "/api/auth/config", "/api/presets", "/api/presets/wordpress"} { // the catalogue is read-only and open to visitors
		if code, _ := anon.do("GET", path, ""); code != 200 {
			t.Errorf("%s must stay public (the container health check uses it), got %d", path, code)
		}
	}
}

// People only see and control their own jobs and runs. Administrators manage
// accounts and settings, but their test history is their own, like anyone's.
func TestUsersAreIsolatedFromEachOther(t *testing.T) {
	e := newAuthEnv(t)
	admin := e.signUp("admin@x.test") // first account: administrator
	alice := e.signUp("alice@x.test")
	bob := e.signUp("bob@x.test")

	code, body := alice.do("POST", "/api/jobs", jobBody())
	if code != 201 {
		t.Fatalf("create job: %d %s", code, body)
	}
	job := idOf(t, body)
	code, body = alice.do("POST", "/api/jobs/"+job+"/start", "")
	if code != 202 {
		t.Fatalf("start: %d %s", code, body)
	}
	run := idOf(t, body)

	// Bob cannot see, read, start, stop, download or delete Alice's work.
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/jobs/" + job}, {"DELETE", "/api/jobs/" + job}, {"POST", "/api/jobs/" + job + "/start"},
		{"GET", "/api/runs/" + run}, {"POST", "/api/runs/" + run + "/stop"}, {"GET", "/api/runs/" + run + "/report"},
		{"GET", "/api/runs/" + run + "/metrics"}, {"GET", "/api/runs/" + run + "/stream"},
	} {
		if code, _ := bob.do(tc.method, tc.path, ""); code != 404 {
			t.Errorf("bob %s %s = %d, want 404 (not 403: ids must not be probeable)", tc.method, tc.path, code)
		}
	}
	if _, b := bob.do("GET", "/api/runs", ""); strings.Contains(b, run) {
		t.Error("bob's run list must not include alice's run")
	}
	if _, b := bob.do("GET", "/api/jobs", ""); strings.Contains(b, job) {
		t.Error("bob's job list must not include alice's job")
	}
	// Alice can; an administrator cannot, and does not see it in their history.
	if code, _ := alice.do("GET", "/api/runs/"+run, ""); code != 200 {
		t.Errorf("the owner may read the run, got %d", code)
	}
	if code, _ := admin.do("GET", "/api/runs/"+run, ""); code != 404 {
		t.Errorf("an administrator's history is their own: got %d, want 404", code)
	}
	if _, b := admin.do("GET", "/api/runs", ""); strings.Contains(b, run) {
		t.Error("the admin's run list must not include alice's run")
	}
	if _, b := alice.do("GET", "/api/runs", ""); !strings.Contains(b, run) {
		t.Error("alice sees her own run")
	}
	// The run records who started it, and survives for History.
	v := e.mgr.RunViewOf(run)
	if v == nil || v.OwnerName != "alice@x.test" || v.Owner == "" {
		t.Errorf("the run must record its owner: %+v", v)
	}
	time.Sleep(450 * time.Millisecond)
}

// Runs nobody owns (from before sign-in was switched on, if they were not
// adopted at import) belong to no one: nobody can open them.
func TestUnownedRunsAreNobodys(t *testing.T) {
	e := newAuthEnv(t)
	admin := e.signUp("admin@x.test")
	e.mgr.mu.Lock()
	e.mgr.runs["run_legacy"] = &Run{RunView: RunView{ID: "run_legacy", State: "finished", StartedAt: time.Now()}}
	e.mgr.mu.Unlock()
	if code, _ := admin.do("GET", "/api/runs/run_legacy", ""); code != 404 {
		t.Errorf("an unowned run must not be visible, got %d", code)
	}
}

// A visitor without an account can try BLASTA inside the limits, and nowhere else.
func TestGuestsGetAShortLimitedTrial(t *testing.T) {
	e := newAuthEnv(t)
	guest := &person{e: e, c: &http.Client{}}
	jar, _ := cookiejar.New(nil)
	guest.c.Jar = jar

	public := func(extra string) string {
		return `{"name":"t","executor":"http","target":{"url":"http://203.0.113.9/"},"concurrency":1,"rps":5,"duration":200000000,"timeout":100000000` + extra + `}`
	}
	// The template catalogue can be browsed; other people's data and settings are closed to guests.
	for _, path := range []string{"/api/presets", "/api/presets/wordpress"} {
		if code, _ := guest.do("GET", path, ""); code != 200 {
			t.Errorf("guest GET %s = %d, want 200 (browsing templates is open)", path, code)
		}
	}
	for _, path := range []string{"/api/admin/users", "/api/admin/settings"} {
		if code, _ := guest.do("GET", path, ""); code != 401 {
			t.Errorf("guest GET %s = %d, want 401", path, code)
		}
	}
	// Nothing but web tests, inside the limits.
	for name, body := range map[string]string{
		"too fast":     `{"name":"t","executor":"http","target":{"url":"http://203.0.113.9/"},"rps":100000,"duration":200000000}`,
		"too long":     `{"name":"t","executor":"http","target":{"url":"http://203.0.113.9/"},"rps":5,"duration":3600000000000}`,
		"too wide":     `{"name":"t","executor":"http","target":{"url":"http://203.0.113.9/"},"rps":5,"concurrency":9999,"duration":200000000}`,
		"a database":   `{"name":"t","executor":"sql","target":{"url":"x"},"rps":5,"duration":200000000}`,
		"a raw socket": `{"name":"t","executor":"tcp","target":{"url":"203.0.113.9:80"},"rps":5,"duration":200000000}`,
	} {
		if code, b := guest.do("POST", "/api/jobs", body); code != 403 {
			t.Errorf("guest job %s = %d %s, want 403", name, code, b)
		}
	}
	// A private address stays blocked even if the job says otherwise.
	if code, _ := guest.do("POST", "/api/jobs", `{"name":"t","executor":"http","target":{"url":"http://127.0.0.1:1/"},"rps":5,"duration":200000000,"blockPrivate":false}`); code != 400 {
		t.Errorf("a guest must never reach a private address, got %d", code)
	}
	code, body := guest.do("POST", "/api/jobs", public(""))
	if code != 201 {
		t.Fatalf("a guest may create a web test: %d %s", code, body)
	}
	job := idOf(t, body)
	code, body = guest.do("POST", "/api/jobs/"+job+"/start", "")
	if code != 202 {
		t.Fatalf("a guest may start it: %d %s", code, body)
	}
	run := idOf(t, body)
	if code, _ := guest.do("POST", "/api/jobs/"+job+"/start", ""); code != 409 {
		t.Errorf("one trial test at a time, got %d", code)
	}
	if code, _ := guest.do("GET", "/api/runs/"+run, ""); code != 200 {
		t.Errorf("a guest reads their own run, got %d", code)
	}

	// Another visitor sees none of it.
	other := &person{e: e, c: &http.Client{}}
	other.c.Jar, _ = cookiejar.New(nil)
	if _, b := other.do("GET", "/api/runs", ""); strings.Contains(b, run) {
		t.Error("another visitor must not see this run")
	}
	if code, _ := other.do("GET", "/api/runs/"+run, ""); code != 404 {
		t.Errorf("another visitor opening the run = %d, want 404", code)
	}
	// A guest run is never written to history.
	if v := e.mgr.RunViewOf(run); v == nil || !strings.HasPrefix(v.Owner, "guest:") {
		t.Errorf("the run must be owned by the guest: %+v", v)
	}
	// The trial is visible to the page.
	if _, b := guest.do("GET", "/api/auth/guest", ""); !strings.Contains(b, `"active":true`) || !strings.Contains(b, `"runsUsed":1`) {
		t.Errorf("trial status = %s", b)
	}
	time.Sleep(450 * time.Millisecond)
}

// With sign-in off nothing changes: no gate, no owner checks.
func TestSignInOffChangesNothing(t *testing.T) {
	a, mgr := testAPI(t)
	job, _ := mgr.SaveJob(testJobForAPI())
	if rec := post(t, a, "/api/jobs/"+job.ID+"/start", ""); rec.Code != 202 {
		t.Errorf("start without sign-in = %d", rec.Code)
	}
	if rec := get(t, a, "/api/runs"); rec.Code != 200 {
		t.Errorf("list without sign-in = %d", rec.Code)
	}
	if rec := get(t, a, "/api/auth/config"); rec.Code != 404 {
		t.Errorf("the sign-in endpoints must not exist when it is off, got %d", rec.Code)
	}
}

// BLASTA can live under a path prefix, with or without a proxy that strips it,
// and accepts the origin a proxy publishes.
func TestServedUnderAPathAndBehindAProxy(t *testing.T) {
	bootstrap.Register()
	d, _ := db.OpenMemory()
	t.Cleanup(func() { d.Close() })
	svc, _ := auth.New(auth.NewStore(d), auth.Config{Registration: auth.RegOpen, PublicURL: "https://tools.example.com/blasta"})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ui := fstest.MapFS{"index.html": {Data: []byte("<html>home</html>")}, "app.js": {Data: []byte("js")}}
	srv := httptest.NewServer(NewServer("", NewManager(logger), ui, logger, WithAuth(svc)).http.Handler)
	t.Cleanup(srv.Close)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	do := func(method, path string, hdr map[string]string) (int, string, string) {
		req, _ := http.NewRequest(method, srv.URL+path, nil)
		req.Header.Set("X-Requested-With", "blasta")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b), resp.Header.Get("Location")
	}
	// Reachable both under the prefix and at the root (a proxy that strips it).
	for _, p := range []string{"/blasta/api/health", "/api/health"} {
		if code, _, _ := do("GET", p, nil); code != 200 {
			t.Errorf("%s = %d", p, code)
		}
	}
	if code, body, _ := do("GET", "/blasta/", nil); code != 200 || !strings.Contains(body, "home") {
		t.Errorf("the page under the prefix: %d %q", code, body)
	}
	if code, body, _ := do("GET", "/blasta/app.js", nil); code != 200 || body != "js" {
		t.Errorf("an asset under the prefix: %d %q", code, body)
	}
	if code, _, loc := do("GET", "/blasta", nil); code != 301 || loc != "/blasta/" {
		t.Errorf("/blasta must redirect to /blasta/: %d %q", code, loc)
	}
	// A browser on the public address posts with that Origin; a proxy that rewrites
	// Host still works because it says what the original was.
	post := func(origin string, hdr map[string]string) int {
		h := map[string]string{"Origin": origin}
		for k, v := range hdr {
			h[k] = v
		}
		code, _, _ := do("POST", "/blasta/api/auth/login", h)
		return code
	}
	if code := post("https://tools.example.com", nil); code == 403 {
		t.Error("the Public URL's own origin must be accepted")
	}
	if code := post("https://app.example.org", map[string]string{"X-Forwarded-Host": "app.example.org"}); code == 403 {
		t.Error("the origin a proxy publishes must be accepted")
	}
	if code := post("https://evil.example", nil); code != 403 {
		t.Errorf("another site's origin must be refused, got %d", code)
	}
}

// Only administrators delete history, and only their own unless they ask for all.
func TestOnlyAdministratorsDeleteHistory(t *testing.T) {
	e := newAuthEnv(t)
	admin := e.signUp("admin@x.test")
	bob := e.signUp("bob@x.test")
	owner := func(p *person) string {
		_, b := p.do("GET", "/api/auth/me", "")
		var m struct{ ID string }
		json.Unmarshal([]byte(b), &m)
		return m.ID
	}
	adminID, bobID := owner(admin), owner(bob)
	add := func(id, ownerID, state string) {
		e.mgr.mu.Lock()
		e.mgr.runs[id] = &Run{RunView: RunView{ID: id, Owner: ownerID, State: state, StartedAt: time.Now()}}
		e.mgr.mu.Unlock()
	}
	add("run_a1", adminID, "finished")
	add("run_a2", adminID, "finished")
	add("run_live", adminID, "running")
	add("run_b1", bobID, "finished")

	if code, _ := bob.do("DELETE", "/api/runs/run_b1", ""); code != 403 {
		t.Errorf("a user deleting their own run = %d, want 403", code)
	}
	if code, _ := bob.do("DELETE", "/api/runs", ""); code != 403 {
		t.Errorf("a user clearing history = %d, want 403", code)
	}
	if code, _ := admin.do("DELETE", "/api/runs/run_b1", ""); code != 404 {
		t.Errorf("an admin deleting someone else's run = %d, want 404 (history is private)", code)
	}
	if code, _ := admin.do("DELETE", "/api/runs/run_live", ""); code != 409 {
		t.Errorf("a running test cannot be deleted: %d", code)
	}
	if code, _ := admin.do("DELETE", "/api/runs/run_a1", ""); code != 204 {
		t.Errorf("an admin deletes their own run: %d", code)
	}
	if e.mgr.RunViewOf("run_a1") != nil {
		t.Error("the run must be gone")
	}
	// Clear all: the admin's own finished runs; the running one and Bob's survive.
	if code, b := admin.do("DELETE", "/api/runs", ""); code != 200 || !strings.Contains(b, `"deleted":1`) {
		t.Errorf("clear mine: %d %s", code, b)
	}
	if e.mgr.RunViewOf("run_a2") != nil || e.mgr.RunViewOf("run_live") == nil || e.mgr.RunViewOf("run_b1") == nil {
		t.Error("clearing my history must leave the running test and other people's runs")
	}
	if code, b := admin.do("DELETE", "/api/runs?scope=all", ""); code != 200 || !strings.Contains(b, `"deleted":1`) {
		t.Errorf("clear everyone's: %d %s", code, b)
	}
	if e.mgr.RunViewOf("run_b1") != nil || e.mgr.RunViewOf("run_live") == nil {
		t.Error("scope=all removes everyone's finished runs, never a running one")
	}
}

// Deleting removes the saved copy too, so it does not come back after a restart.
func TestDeletedHistoryStaysDeleted(t *testing.T) {
	d := openTestDB(t, t.TempDir())
	_, mgr := testAPI(t)
	if err := mgr.EnableHistory(d, "", "", nil); err != nil {
		t.Fatal(err)
	}
	mgr.persist(RunView{ID: "run_1", Owner: "u1", State: "finished", StartedAt: time.Now()})
	mgr.persist(RunView{ID: "run_2", Owner: "u2", State: "finished", StartedAt: time.Now()})
	mgr.mu.Lock()
	mgr.runs["run_1"] = &Run{RunView: RunView{ID: "run_1", Owner: "u1", State: "finished"}}
	mgr.runs["run_2"] = &Run{RunView: RunView{ID: "run_2", Owner: "u2", State: "finished"}}
	mgr.mu.Unlock()
	if err := mgr.DeleteRun("run_1"); err != nil {
		t.Fatal(err)
	}
	var n int
	d.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&n)
	if n != 1 {
		t.Errorf("%d rows left, want 1", n)
	}
	if _, err := mgr.ClearRuns("", true); err != nil {
		t.Fatal(err)
	}
	d.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&n)
	if n != 0 {
		t.Errorf("%d rows left after clearing all", n)
	}
}
