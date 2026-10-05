package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/awhadi/blasta-perftest/internal/collector"
	"github.com/awhadi/blasta-perftest/internal/config"
	"github.com/awhadi/blasta-perftest/internal/sysstat"
)

// fakeRecorder reports a machine burning 25% of 4 cores with 100 MB in use.
func fakeRecorder() *sysstat.Recorder {
	var cpu float64
	return sysstat.NewRecorderFrom(func() (sysstat.Reading, error) {
		cpu += 0.02 * 4 * 0.25 // 25% of 4 cores over a 20 ms sample
		return sysstat.Reading{CPUSeconds: cpu, Cores: 4, MemUsed: 100 << 20, MemLimit: 1 << 30, Scope: "container"}, nil
	}, 20*time.Millisecond)
}

func waitFinished(t *testing.T, run *Run) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for run.view().State == "running" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if run.view().State == "running" {
		t.Fatal("run did not finish")
	}
}

// A finished run carries what the load generator itself used, in the summary,
// the JSON report and the CSV, and it survives a restart.
func TestRunRecordsServerResources(t *testing.T) {
	dir := t.TempDir()
	a, mgr := testAPI(t)
	mgr.newRecorder = fakeRecorder
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

	// While the run is live, snapshots carry the server's current usage.
	time.Sleep(120 * time.Millisecond)
	if snap := mgr.snapshotOf(run); snap.Server == nil || snap.Server.Cores != 4 {
		t.Errorf("live snapshot has no server stats: %+v", snap.Server)
	}
	waitFinished(t, run)

	res := run.view().Summary.Resources
	if res == nil || res.Samples < 3 {
		t.Fatalf("finished run has no resources: %+v", res)
	}
	if res.CPUAvgPct < 10 || res.CPUAvgPct > 40 || res.Cores != 4 || res.MemPeak != 100<<20 || res.MemLimit != 1<<30 {
		t.Errorf("recorded figures are wrong: %+v", res)
	}
	if len(res.Series) == 0 {
		t.Error("no time series recorded")
	}

	rep := get(t, a, "/api/runs/"+run.ID+"/report")
	var got collector.Summary
	if err := json.Unmarshal(rep.Body.Bytes(), &got); err != nil || got.Resources == nil {
		t.Fatalf("JSON report lacks resources (err=%v): %s", err, rep.Body)
	}
	csv := get(t, a, "/api/runs/"+run.ID+"/report?format=csv").Body.String()
	for _, want := range []string{"server_cpu_avg_pct,", "server_cpu_peak_pct,", "server_mem_peak_bytes,104857600", "server_cores,4.0"} {
		if !strings.Contains(csv, want) {
			t.Errorf("CSV missing %q:\n%s", want, csv)
		}
	}

	// A restart must keep it: History shows the figures for old runs too.
	_, mgr2 := testAPI(t)
	if err := mgr2.EnableHistory(openTestDB(t, dir), "", "", nil); err != nil {
		t.Fatal(err)
	}
	v := mgr2.RunViewOf(run.ID)
	if v == nil || v.Summary.Resources == nil || v.Summary.Resources.Samples != res.Samples {
		t.Errorf("resources lost across a restart: %+v", v)
	}
}

// Where the platform cannot measure, runs still work and simply have no figures.
func TestRunWithoutResourceSupport(t *testing.T) {
	_, mgr := testAPI(t)
	mgr.newRecorder = func() *sysstat.Recorder {
		return sysstat.NewRecorderFrom(func() (sysstat.Reading, error) {
			return sysstat.Reading{}, errNoStats
		}, 20*time.Millisecond)
	}
	job, _ := mgr.SaveJob(testJobForAPI())
	run, err := mgr.Start(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitFinished(t, run)
	if run.view().Summary.Resources != nil || run.view().State != "finished" {
		t.Errorf("unsupported platform must give a normal run with no resources: %+v", run.view())
	}
}

type statsErr string

func (e statsErr) Error() string { return string(e) }

const errNoStats = statsErr("unsupported")

// The load plan and the time series are saved with the run, never headers or
// bodies, and they survive a restart: History needs them to chart and judge it.
func TestRunStoresPlanAndSeries(t *testing.T) {
	dir := t.TempDir()
	_, mgr := testAPI(t)
	mgr.newRecorder = fakeRecorder
	if err := mgr.EnableHistory(openTestDB(t, dir), "", "", nil); err != nil {
		t.Fatal(err)
	}
	j := testJobForAPI()
	j.Headers = map[string]string{"Authorization": "Bearer super-secret"}
	j.Body = "password=hunter2"
	rate := 1.5
	j.SLO = &config.SLO{MaxErrorRate: &rate, MaxP95: 500 * time.Millisecond}
	job, err := mgr.SaveJob(j)
	if err != nil {
		t.Fatal(err)
	}
	run, err := mgr.Start(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitFinished(t, run)

	v := run.view()
	if v.Plan == nil || v.Plan.RPS != 20 || v.Plan.Concurrency != 2 || v.Plan.SLO == nil || *v.Plan.SLO.MaxErrorRate != 1.5 {
		t.Fatalf("plan not stored: %+v", v.Plan)
	}
	if len(v.Summary.Series) == 0 {
		t.Error("no time series stored")
	}
	var raw string
	rows, _ := openTestDB(t, dir).Query(`SELECT data FROM runs`)
	for rows.Next() {
		var d string
		rows.Scan(&d)
		raw += d
	}
	rows.Close()
	if raw == "" || strings.Contains(raw, "super-secret") || strings.Contains(raw, "hunter2") {
		t.Fatal("a header or body value reached the database (or nothing was stored)")
	}

	_, mgr2 := testAPI(t)
	if err := mgr2.EnableHistory(openTestDB(t, dir), "", "", nil); err != nil {
		t.Fatal(err)
	}
	r := mgr2.RunViewOf(run.ID)
	if r == nil || r.Plan == nil || r.Plan.RPS != 20 || len(r.Summary.Series) == 0 {
		t.Errorf("plan or series lost across a restart: %+v", r)
	}
}

// The run list must be in a stable, chronological order: History shows newest first.
func TestRunsAreListedOldestFirst(t *testing.T) {
	_, mgr := testAPI(t)
	base := time.Now()
	for i, id := range []string{"run_c", "run_a", "run_b"} {
		mgr.runs[id] = &Run{RunView: RunView{ID: id, State: "finished", StartedAt: base.Add(time.Duration([]int{2, 0, 1}[i]) * time.Minute)}}
	}
	got := mgr.Runs()
	if len(got) != 3 || got[0].ID != "run_a" || got[1].ID != "run_b" || got[2].ID != "run_c" {
		t.Errorf("runs not oldest-first: %v %v %v", got[0].ID, got[1].ID, got[2].ID)
	}
}
