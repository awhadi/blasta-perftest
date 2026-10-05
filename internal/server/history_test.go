package server

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/awhadi/blasta-perftest/internal/db"
)

// openTestDB opens (or reopens) the file database in dir, closed when the test ends.
func openTestDB(t *testing.T, dir string) *db.DB {
	t.Helper()
	d, err := db.Open("", dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// A run that finishes while history is on must survive a restart and still
// serve its report, but must not pretend to have live metrics.
func TestHistorySurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	a, mgr := testAPI(t)
	if err := mgr.EnableHistory(openTestDB(t, dir), "", "", nil); err != nil {
		t.Fatal(err)
	}
	job, err := mgr.SaveJob(testJobForAPI())
	if err != nil {
		t.Fatal(err)
	}
	run, err := mgr.Start(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for run.view().State == "running" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if run.view().State == "running" {
		t.Fatal("run did not finish")
	}
	_ = a

	// "Restart": a fresh manager over the same database.
	a2, mgr2 := testAPI(t)
	if err := mgr2.EnableHistory(openTestDB(t, dir), "", "", nil); err != nil {
		t.Fatal(err)
	}
	v := mgr2.RunViewOf(run.ID)
	if v == nil {
		t.Fatalf("run %s not restored", run.ID)
	}
	if v.JobName != "api test" || v.Summary.Total == 0 {
		t.Errorf("restored run lost its data: %+v", v)
	}
	if rec := get(t, a2, "/api/runs/"+run.ID+"/report"); rec.Code != 200 {
		t.Errorf("report for restored run = %d (%s)", rec.Code, rec.Body)
	}
	if rec := get(t, a2, "/api/runs/"+run.ID+"/metrics"); rec.Code != 410 {
		t.Errorf("metrics for restored run = %d, want 410", rec.Code)
	}
	if rec := get(t, a2, "/api/runs/"+run.ID+"/stream"); rec.Code != 410 {
		t.Errorf("stream for restored run = %d, want 410", rec.Code)
	}
}

// Runs saved by earlier versions in runs.jsonl move into the database once.
func TestLegacyHistoryIsImported(t *testing.T) {
	dir := t.TempDir()
	mk := func(id, owner string) string {
		return fmt.Sprintf(`{"id":%q,"owner":%q,"state":"finished","startedAt":"2026-01-01T00:00:0%sZ","summary":{}}`, id, owner, id[len(id)-1:])
	}
	body := mk("run_1", "usr_a") + "\n{this is not json\n" + mk("run_2", "") + "\n" + mk("run_3", "usr_gone") + "\n{\"id\":\"run_4\",\"sta"
	if err := os.WriteFile(filepath.Join(dir, "runs.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_, mgr := testAPI(t)
	ok := func(id string) bool { return id == "usr_a" || id == "usr_admin" }
	if err := mgr.EnableHistory(openTestDB(t, dir), dir, "usr_admin", ok); err != nil {
		t.Fatal(err)
	}
	if n := len(mgr.Runs()); n != 2 {
		t.Errorf("imported %d runs, want 2 (corrupt line, truncated line and a deleted user's run dropped)", n)
	}
	if v := mgr.RunViewOf("run_2"); v == nil || v.Owner != "usr_admin" {
		t.Errorf("an unowned run must go to the default owner: %+v", v)
	}
	if _, err := os.Stat(filepath.Join(dir, "runs.jsonl.migrated")); err != nil {
		t.Error("the old file must be kept as a backup, renamed")
	}
	if _, err := os.Stat(filepath.Join(dir, "runs.jsonl")); err == nil {
		t.Error("the old file must not be imported twice")
	}
}

// Each person keeps a bounded history; one person's runs never push out another's.
func TestHistoryIsTrimmedPerPerson(t *testing.T) {
	d := openTestDB(t, t.TempDir())
	h := &history{db: d}
	base := time.Now()
	for i := 0; i < perPersonHistory+25; i++ {
		if err := h.save(RunView{ID: fmt.Sprintf("a_%04d", i), Owner: "usr_a", State: "finished", StartedAt: base.Add(time.Duration(i) * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.save(RunView{ID: "b_1", Owner: "usr_b", State: "finished", StartedAt: base}); err != nil {
		t.Fatal(err)
	}
	runs, err := h.load()
	if err != nil {
		t.Fatal(err)
	}
	var a, b int
	var newest string
	for _, r := range runs {
		switch r.Owner {
		case "usr_a":
			a++
			newest = r.ID
		case "usr_b":
			b++
		}
	}
	if a != perPersonHistory || b != 1 || newest != fmt.Sprintf("a_%04d", perPersonHistory+24) {
		t.Errorf("a=%d b=%d newest=%s", a, b, newest)
	}
}

// A restart mid-run must not leave a run claiming to be running forever.
func TestRestoredRunningStateBecomesAborted(t *testing.T) {
	d := openTestDB(t, t.TempDir())
	if err := (&history{db: d}).save(RunView{ID: "run_x", State: "running", StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	_, mgr := testAPI(t)
	if err := mgr.EnableHistory(d, "", "", nil); err != nil {
		t.Fatal(err)
	}
	if v := mgr.RunViewOf("run_x"); v == nil || v.State != "aborted" {
		t.Errorf("state = %+v, want aborted", v)
	}
}

// Guest trial runs are never written to the database.
func TestGuestRunsAreNotPersisted(t *testing.T) {
	d := openTestDB(t, t.TempDir())
	_, mgr := testAPI(t)
	if err := mgr.EnableHistory(d, "", "", nil); err != nil {
		t.Fatal(err)
	}
	mgr.persist(RunView{ID: "run_g", Owner: "guest:abc", State: "finished", StartedAt: time.Now()})
	mgr.persist(RunView{ID: "run_u", Owner: "usr_a", State: "finished", StartedAt: time.Now()})
	var n int
	d.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&n)
	if n != 1 {
		t.Errorf("%d runs stored, want only the signed-in user's", n)
	}
}
