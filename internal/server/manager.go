// Package server hosts the local web UI and its JSON API. It binds to
// 127.0.0.1 by default and exposes no authentication surface.
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/awhadi/blasta-perftest/internal/bootstrap"
	"github.com/awhadi/blasta-perftest/internal/collector"
	"github.com/awhadi/blasta-perftest/internal/config"
	"github.com/awhadi/blasta-perftest/internal/db"
	"github.com/awhadi/blasta-perftest/internal/engine"
	"github.com/awhadi/blasta-perftest/internal/guard"
	"github.com/awhadi/blasta-perftest/internal/sysstat"
)

// RunView is the immutable JSON projection of a run. Handlers snapshot this
// under the run lock and encode the copy, so concurrent state updates from the
// execution goroutine can never race with a JSON read.
type RunView struct {
	ID        string            `json:"id"`
	JobID     string            `json:"jobId"`
	JobName   string            `json:"jobName"`
	Executor  string            `json:"executor"`
	Target    string            `json:"target"`
	State     string            `json:"state"` // running|finished|aborted|error
	StartedAt time.Time         `json:"startedAt"`
	EndedAt   *time.Time        `json:"endedAt,omitempty"`
	Reason    string            `json:"reason,omitempty"`
	Summary   collector.Summary `json:"summary"`
	// Owner is the id of the user who started the run, OwnerName their email.
	// Both are empty when sign-in is off.
	Owner     string `json:"owner,omitempty"`
	OwnerName string `json:"ownerName,omitempty"`
	// Plan is how the run was configured: the non-sensitive load settings and
	// SLO. Headers, bodies and credentials are never stored.
	Plan *RunPlan `json:"plan,omitempty"`
	// Baseline marks the run its owner compares later runs of the same test against.
	Baseline bool `json:"baseline,omitempty"`
}

// RunPlan is the load profile a run used, kept so History can show it and judge
// the run against its SLO.
type RunPlan struct {
	Method       string        `json:"method,omitempty"`
	RPS          int           `json:"rps"`
	Concurrency  int           `json:"concurrency"`
	Duration     time.Duration `json:"duration"`
	Ramp         time.Duration `json:"ramp,omitempty"`
	Timeout      time.Duration `json:"timeout,omitempty"`
	ExpectStatus []int         `json:"expectStatus,omitempty"`
	SLO          *config.SLO   `json:"slo,omitempty"`
}

func planOf(j config.Job) *RunPlan {
	return &RunPlan{Method: j.Method, RPS: j.RPS, Concurrency: j.Concurrency, Duration: j.Duration,
		Ramp: j.Ramp, Timeout: j.Timeout, ExpectStatus: j.ExpectStatus, SLO: j.SLO}
}

// RunViewOf returns a snapshot of a run, or nil if the id is unknown.
func (m *Manager) RunViewOf(id string) *RunView {
	m.mu.RLock()
	r, ok := m.runs[id]
	m.mu.RUnlock()
	if !ok {
		return nil
	}
	v := r.view()
	return &v
}

// Run is one execution of a job. Mutable fields are guarded by mu.
type Run struct {
	RunView

	col    *collector.Collector
	rec    *sysstat.Recorder // CPU and memory of this server while the run lasts
	cancel context.CancelFunc
	mu     sync.Mutex
	// stopRequested records that the user pressed stop. finish() consults it so
	// a manually stopped run is not relabelled "finished" when the runner's
	// graceful drain returns.
	stopRequested bool
}

// view returns a consistent copy of the run for serialization.
func (r *Run) view() RunView {
	r.mu.Lock()
	defer r.mu.Unlock()
	v := r.RunView
	if r.EndedAt != nil {
		end := *r.EndedAt
		v.EndedAt = &end
	}
	return v
}

// Manager owns job storage and live runs.
type Manager struct {
	mu   sync.RWMutex
	jobs map[string]*config.Job
	runs map[string]*Run
	// jobOwner maps a job id to the user who created it (sign-in on).
	jobOwner  map[string]string
	ownerName map[string]string
	jobAt     map[string]time.Time // when each owned job was created

	subMu sync.Mutex
	subs  map[string]map[chan collector.Snapshot]struct{}
	log   *slog.Logger
	hist  *history // nil when persistence is off
	// onFinish, if set, hears about every run that ends (notifications use it).
	onFinish func(RunView)
	// newRecorder builds the per-run resource recorder; tests replace it.
	newRecorder func() *sysstat.Recorder
}

// EnableHistory keeps finished runs in the database and reloads those saved by
// an earlier process. Reloaded runs are read-only: they have a summary and a
// report, but no live metrics. A runs.jsonl left in legacyDir by an earlier
// version is imported once; runs nobody owned go to defaultOwner, and ownerOK
// (if set) says which owners still exist.
func (m *Manager) EnableHistory(d *db.DB, legacyDir, defaultOwner string, ownerOK func(string) bool) error {
	h := &history{db: d}
	if legacyDir != "" {
		var n int
		_ = d.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&n)
		if n == 0 {
			if imported, err := h.importJSONL(legacyDir, defaultOwner, ownerOK); err != nil {
				m.log.Warn("could not import the old run history", "err", err)
			} else if imported > 0 {
				m.log.Info("imported run history into the database", "runs", imported)
			}
		}
	}
	runs, err := h.load()
	if err != nil {
		m.log.Warn("history partly unreadable", "err", err)
	}
	m.mu.Lock()
	for _, v := range runs {
		if v.State == "running" {
			v.State = "aborted"
		}
		m.runs[v.ID] = &Run{RunView: v}
	}
	m.hist = h
	m.mu.Unlock()
	m.log.Info("history enabled", "restored", len(runs))
	return nil
}

// persist saves a finished run, logging rather than failing the run on error.
// Guest trial runs are not kept.
func (m *Manager) persist(v RunView) {
	if m.hist == nil || strings.HasPrefix(v.Owner, "guest:") {
		return
	}
	if err := m.hist.save(v); err != nil {
		m.log.Warn("could not save run history", "runId", v.ID, "err", err)
	}
}

// timeSuffix is the " · 10:15 AM" that manual runs carry in their name, so runs can be told apart
// in the history. It is not part of what the test is.
var timeSuffix = regexp.MustCompile(`\s·\s\d{1,2}:\d{2}(\s?[AaPp][Mm])?$`)

// sameTest says whether two runs are runs of one test: the same protocol, target and name (not
// counting the time of day a manual run is named with).
func sameTest(a, b RunView) bool {
	return a.Owner == b.Owner && a.Executor == b.Executor && a.Target == b.Target &&
		timeSuffix.ReplaceAllString(a.JobName, "") == timeSuffix.ReplaceAllString(b.JobName, "")
}

// SetBaseline marks a finished run as the baseline for its test (or takes the mark away). A test is
// the same job name, protocol and target; only one run of it is the baseline at a time.
func (m *Manager) SetBaseline(runID string, on bool) error {
	m.mu.RLock()
	run, ok := m.runs[runID]
	var peers []*Run
	for _, r := range m.runs {
		peers = append(peers, r)
	}
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("run %s not found", runID)
	}
	run.mu.Lock()
	if run.State == "running" {
		run.mu.Unlock()
		return errors.New("wait for the run to finish before using it as a baseline")
	}
	run.Baseline = on
	v := run.RunView
	run.mu.Unlock()
	m.persist(v)
	if !on {
		return nil
	}
	for _, p := range peers {
		if p == run {
			continue
		}
		p.mu.Lock()
		same := p.Baseline && sameTest(p.RunView, v)
		if same {
			p.Baseline = false
		}
		pv := p.RunView
		p.mu.Unlock()
		if same {
			m.persist(pv)
		}
	}
	return nil
}

// SetOnFinish registers what happens when a run ends.
func (m *Manager) SetOnFinish(f func(RunView)) { m.onFinish = f }

// DB is the database that holds history (and people's saved templates), or nil without one.
func (m *Manager) DB() *db.DB {
	if m.hist == nil {
		return nil
	}
	return m.hist.db
}

func NewManager(log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{
		jobs:      map[string]*config.Job{},
		runs:      map[string]*Run{},
		jobOwner:  map[string]string{},
		ownerName: map[string]string{},
		jobAt:     map[string]time.Time{},
		subs:      map[string]map[chan collector.Snapshot]struct{}{},
		log:       log,

		newRecorder: sysstat.NewRecorder,
	}
}

// PruneGuests forgets guest trial jobs and finished guest runs older than an hour,
// so visitors who never come back do not accumulate in memory.
func (m *Manager) PruneGuests() {
	cut := time.Now().Add(-time.Hour)
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, r := range m.runs {
		v := r.view()
		if strings.HasPrefix(v.Owner, "guest:") && v.State != "running" && v.StartedAt.Before(cut) {
			delete(m.runs, id)
		}
	}
	for id, owner := range m.jobOwner {
		if !strings.HasPrefix(owner, "guest:") {
			continue
		}
		if at, ok := m.jobAt[id]; ok && at.Before(cut) && !m.guestJobInUseLocked(id, cut) {
			delete(m.jobs, id)
			delete(m.jobOwner, id)
			delete(m.jobAt, id)
		}
	}
}

// guestJobInUseLocked reports whether a recent or running run uses the job.
func (m *Manager) guestJobInUseLocked(jobID string, cut time.Time) bool {
	for _, r := range m.runs {
		v := r.view()
		if v.JobID == jobID && (v.State == "running" || !v.StartedAt.Before(cut)) {
			return true
		}
	}
	return false
}

// CountOwned returns how many jobs an owner has, and whether they have a run going.
func (m *Manager) CountOwned(owner string) (jobs int, running bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, o := range m.jobOwner {
		if o == owner {
			jobs++
		}
	}
	for _, r := range m.runs {
		if v := r.view(); v.Owner == owner && v.State == "running" {
			running = true
		}
	}
	return
}

// ErrRunning is returned when asked to delete a run that has not finished.
var ErrRunning = errors.New("that test is still running: stop it first")

// DeleteRun removes one finished run from memory and from the database.
func (m *Manager) DeleteRun(id string) error {
	m.mu.Lock()
	r, ok := m.runs[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("run %s not found", id)
	}
	if r.view().State == "running" {
		m.mu.Unlock()
		return ErrRunning
	}
	delete(m.runs, id)
	m.mu.Unlock()
	if m.hist != nil {
		return m.hist.remove(id, "")
	}
	return nil
}

// ClearRuns removes every finished run owned by owner, or everyone's when all is
// true, and returns how many. Runs still going are left alone.
func (m *Manager) ClearRuns(owner string, all bool) (int, error) {
	m.mu.Lock()
	n := 0
	for id, r := range m.runs {
		v := r.view()
		if v.State == "running" || (!all && v.Owner != owner) {
			continue
		}
		delete(m.runs, id)
		n++
	}
	m.mu.Unlock()
	if m.hist == nil {
		return n, nil
	}
	// The database may hold runs that were never loaded into memory (older than the
	// reload limit); clear those too, but spare anything running right now.
	if all {
		return n, m.hist.remove("", "*")
	}
	return n, m.hist.remove("", owner)
}

func newID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}

// SaveJob stores or updates a job.
func (m *Manager) SaveJob(j config.Job) (config.Job, error) {
	if err := j.Validate(); err != nil {
		return j, err
	}
	// Any executor that dials by URL is subject to the same SSRF/private-network
	// policy as http; sql has no URL and is guarded by its read-only rule instead.
	if j.Target.URL != "" {
		var err error
		switch j.Executor {
		case "http":
			err = guard.CheckURL(j.Target.URL, j.Allowlist, j.BlockPrivate)
		case "tcp":
			// The tcp executor accepts both "tcp://host:port" and "host:port".
			if strings.HasPrefix(strings.ToLower(j.Target.URL), "tcp://") {
				err = guard.CheckDialTarget(j.Target.URL, j.BlockPrivate, "tcp")
			} else {
				err = guard.CheckHostPort(j.Target.URL, j.Allowlist, j.BlockPrivate)
			}
		case "ws":
			err = guard.CheckDialTarget(j.Target.URL, j.BlockPrivate, "ws", "wss")
		case "grpc":
			err = guard.CheckDialTarget(j.Target.URL, j.BlockPrivate, "grpc")
		default:
			err = guard.CheckURL(j.Target.URL, j.Allowlist, j.BlockPrivate)
		}
		if err != nil {
			return j, err
		}
	}
	if j.ID == "" {
		j.ID = newID("job")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs[j.ID] = &j
	m.log.Info("job saved", "id", j.ID, "name", j.Name, "target", j.Target.URL)
	return j, nil
}

// SetJobOwner records who created a job.
func (m *Manager) SetJobOwner(jobID, ownerID, ownerName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobOwner[jobID] = ownerID
	m.jobAt[jobID] = time.Now()
	m.ownerName[ownerID] = ownerName
}

// JobOwner returns the id of the user who created a job ("" if unowned).
func (m *Manager) JobOwner(jobID string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.jobOwner[jobID]
}

func (m *Manager) Job(id string) (config.Job, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	j, ok := m.jobs[id]
	if !ok {
		return config.Job{}, false
	}
	return *j, true
}

func (m *Manager) Jobs() []config.Job {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]config.Job, 0, len(m.jobs))
	for _, j := range m.jobs {
		out = append(out, *j)
	}
	return out
}

// Start launches a run for the given job.
func (m *Manager) Start(jobID string) (*Run, error) {
	m.mu.RLock()
	owner := m.jobOwner[jobID]
	name := m.ownerName[owner]
	m.mu.RUnlock()
	return m.start(jobID, owner, name)
}

func (m *Manager) start(jobID, owner, ownerName string) (*Run, error) {
	job, ok := m.Job(jobID)
	if !ok {
		return nil, fmt.Errorf("job %s not found", jobID)
	}
	exec, cleanup, err := bootstrap.ExecutorFor(job)
	if err != nil {
		return nil, err
	}

	col := collector.New()
	rec := m.newRecorder()
	rec.Start()
	run := &Run{
		RunView: RunView{
			ID:        newID("run"),
			JobID:     job.ID,
			JobName:   job.Name,
			Executor:  job.Executor,
			Target:    job.Target.URL,
			State:     "running",
			StartedAt: time.Now(),
			Plan:      planOf(job),
			Owner:     owner,
			OwnerName: ownerName,
		},
		col: col,
		rec: rec,
	}
	runner := engine.NewRunner(run.ID, job, exec, col, m.logAdapter())

	ctx, cancel := context.WithCancel(context.Background())
	run.cancel = cancel
	// Release per-run resources (connection pools) once the runner returns.
	stopCleanup := sync.OnceFunc(cleanup)

	m.mu.Lock()
	m.runs[run.ID] = run
	m.mu.Unlock()

	m.log.Info("run started", "runId", run.ID, "job", job.Name,
		"rps", job.RPS, "concurrency", job.Concurrency, "executor", job.Executor)

	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		done := make(chan struct{})
		go func() {
			defer func() {
				if rec := recover(); rec != nil {
					m.log.Error("run panicked", "runId", run.ID, "panic", rec)
					run.mu.Lock()
					run.State = "error"
					run.Reason = fmt.Sprint(rec)
					now := time.Now()
					run.EndedAt = &now
					run.Summary = col.Summary(run.ID, run.JobName, run.Executor, run.Target, true, fmt.Sprint(rec))
					run.Summary.Resources = run.rec.Stop()
					run.mu.Unlock()
					m.persist(run.view())
					m.closeSubs(run.ID)
					stopCleanup()
				}
				close(done)
			}()
			runner.Run(ctx)
		}()
		for {
			select {
			case <-done:
				m.broadcast(run.ID, m.snapshotOf(run))
				m.finish(run, false, "")
				stopCleanup()
				return
			case <-ticker.C:
				m.broadcast(run.ID, m.snapshotOf(run))
			}
		}
	}()
	return run, nil
}

func (m *Manager) finish(run *Run, aborted bool, reason string) {
	run.mu.Lock()
	if run.stopRequested && !aborted {
		aborted = true
		if reason == "" {
			reason = "stopped by user"
		}
	}
	// Build the final state on a copy: the run keeps saying "running" until the
	// result is safely on disk, so nothing (a client, or a restart) can see
	// "finished" for a run that was not saved yet.
	final := run.RunView
	final.State = "finished"
	if aborted {
		final.State = "aborted"
	}
	final.Reason = reason
	now := time.Now()
	final.EndedAt = &now
	final.Summary = run.col.Summary(run.ID, run.JobName, run.Executor, run.Target, aborted, reason)
	final.Summary.Resources = run.rec.Stop()
	run.mu.Unlock()

	m.persist(final)

	// Only the fields that change: identity (ID, owner, plan) is never rewritten.
	run.mu.Lock()
	run.State, run.Reason, run.EndedAt, run.Summary = final.State, final.Reason, final.EndedAt, final.Summary
	run.mu.Unlock()
	if m.onFinish != nil {
		go m.onFinish(final)
	}
	m.log.Info("run finished", "runId", run.ID, "total", final.Summary.Total,
		"errors", final.Summary.Errors, "avgRps", int64(final.Summary.AvgRPS))
	m.closeSubs(run.ID)
}

// snapshotOf is the live snapshot of a run with this server's own CPU and
// memory attached, so the UI can show what the load test costs.
func (m *Manager) snapshotOf(run *Run) collector.Snapshot {
	s := run.col.Snapshot()
	if run.rec != nil {
		if st, ok := run.rec.Latest(); ok {
			s.Server = &st
		}
	}
	return s
}

func (m *Manager) logAdapter() engine.Logger { return slogAdapter{m.log} }

type slogAdapter struct{ l *slog.Logger }

func (s slogAdapter) Debug(msg string, args ...any) { s.l.Debug(msg, args...) }
func (s slogAdapter) Warn(msg string, args ...any)  { s.l.Warn(msg, args...) }

// Stop aborts a run.
func (m *Manager) Stop(runID string) error {
	m.mu.RLock()
	run, ok := m.runs[runID]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("run %s not found", runID)
	}
	run.mu.Lock()
	if run.State != "running" {
		state := run.State
		run.mu.Unlock()
		return fmt.Errorf("run %s is already %s", runID, state)
	}
	run.stopRequested = true
	run.Reason = "stopped by user"
	run.State = "aborted"
	// Publish a summary immediately so a client polling right after the stop
	// request does not see a finished run with an empty summary; finish() will
	// replace it with the final numbers once the drain completes.
	run.Summary = run.col.Summary(run.ID, run.JobName, run.Executor, run.Target, true, run.Reason)
	run.Summary.Resources = run.rec.Summary()
	cancel := run.cancel
	run.mu.Unlock()
	// Cancel outside the lock: the runner goroutine takes run.mu when it
	// finishes, and cancelling here while holding it would serialise (and
	// potentially block) the drain path.
	if cancel != nil {
		cancel()
	}
	return nil
}

func (m *Manager) Run(id string) (*Run, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.runs[id]
	return r, ok
}

// Runs returns snapshots of every run, safe to serialize concurrently.
func (m *Manager) Runs() []RunView {
	m.mu.RLock()
	list := make([]*Run, 0, len(m.runs))
	for _, r := range m.runs {
		list = append(list, r)
	}
	m.mu.RUnlock()
	out := make([]RunView, 0, len(list))
	for _, r := range list {
		out = append(out, r.view())
	}
	// Oldest first, so callers get a stable order (the map iterates randomly).
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}

// Subscribe returns a channel of live snapshots for a run and an unsubscribe
// function. The channel is closed by closeSubs when the run ends; unsubscribe
// must therefore not close it again, or a subscriber that disconnects after
// completion would panic.
//
// If the run has already finished the returned channel is closed immediately and
// receives nothing: broadcast only happens while a run is live, so a late
// subscriber would otherwise wait forever for a frame that never arrives.
func (m *Manager) Subscribe(runID string) (<-chan collector.Snapshot, func()) {
	ch := make(chan collector.Snapshot, 8)
	m.subMu.Lock()
	if m.subs[runID] == nil {
		m.subs[runID] = map[chan collector.Snapshot]struct{}{}
	}
	m.subs[runID][ch] = struct{}{}
	m.subMu.Unlock()

	// Check after registering so a run finishing between registration and this
	// check still closes the channel via closeSubs. If it is already finished,
	// no broadcast is coming, so end the stream here.
	if v := m.RunViewOf(runID); v != nil && v.State != "running" {
		m.subMu.Lock()
		if set, ok := m.subs[runID]; ok {
			if _, present := set[ch]; present {
				delete(set, ch)
				close(ch)
			}
			if len(set) == 0 {
				delete(m.subs, runID)
			}
		}
		m.subMu.Unlock()
	}
	return ch, func() {
		m.subMu.Lock()
		defer m.subMu.Unlock()
		if set, ok := m.subs[runID]; ok {
			delete(set, ch)
			if len(set) == 0 {
				delete(m.subs, runID)
			}
		}
	}
}

func (m *Manager) broadcast(runID string, s collector.Snapshot) {
	m.subMu.Lock()
	defer m.subMu.Unlock()
	for ch := range m.subs[runID] {
		select {
		case ch <- s:
		default: // drop slow subscriber rather than block the engine
		}
	}
}

func (m *Manager) closeSubs(runID string) {
	m.subMu.Lock()
	defer m.subMu.Unlock()
	for ch := range m.subs[runID] {
		close(ch)
		delete(m.subs[runID], ch)
	}
	delete(m.subs, runID)
}
