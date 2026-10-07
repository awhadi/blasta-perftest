package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/awhadi/blasta-perftest/internal/auth"
	"github.com/awhadi/blasta-perftest/internal/collector"
	"github.com/awhadi/blasta-perftest/internal/config"
	"github.com/awhadi/blasta-perftest/internal/engine"
	"github.com/awhadi/blasta-perftest/internal/version"
)

// API exposes the JSON control plane. Every handler binds to localhost only.
type API struct {
	mgr  *Manager
	log  *slog.Logger
	mux  *http.ServeMux
	auth *auth.Service // nil when sign-in is off
	// basePath is a fixed path prefix BLASTA is mounted under ("/blasta"), for a
	// reverse proxy that does not strip it. Empty: none configured.
	basePath string
	// runBase remembers the address each run was started from, for the link in a notification.
	runBase sync.Map
}

// WithBasePath serves BLASTA under a path prefix such as "/blasta" as well as at
// the root, so it works behind a proxy whether or not the proxy strips the prefix.
func WithBasePath(p string) Option { return func(a *API) { a.basePath = auth.CleanBase(p) } }

// base is the prefix to strip from incoming paths: the configured one, else the
// path of the Public URL.
func (a *API) base() string {
	if a.basePath != "" {
		return a.basePath
	}
	if a.auth != nil {
		return a.auth.PublicPath()
	}
	return ""
}

// allowedHosts are the hosts a browser may legitimately send as the Origin of a
// same-site request: the one this request was addressed to, the one a trusted
// proxy says it was originally addressed to, and the Public URL's.
func (a *API) allowedHost(r *http.Request, host string) bool {
	if strings.EqualFold(host, r.Host) {
		return true
	}
	// A browser cannot set X-Forwarded-Host on a cross-site request, so this is
	// only ever the proxy's doing.
	if fh := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Host"), ",")[0]); fh != "" && strings.EqualFold(host, fh) {
		return true
	}
	if a.auth != nil {
		if u, err := url.Parse(a.auth.PublicURL()); err == nil && u.Host != "" && strings.EqualFold(host, u.Host) {
			return true
		}
	}
	return false
}

// Option customises an API.
type Option func(*API)

// WithAuth turns on sign-in: every API call except health and the sign-in
// endpoints needs a session, and jobs and runs belong to the user who made them.
func WithAuth(svc *auth.Service) Option { return func(a *API) { a.auth = svc } }

func NewAPI(mgr *Manager, log *slog.Logger, opts ...Option) *API {
	a := &API{mgr: mgr, log: log, mux: http.NewServeMux()}
	for _, o := range opts {
		o(a)
	}
	a.routes()
	if a.auth != nil {
		a.auth.Routes(a.mux)
		mgr.SetOnFinish(a.notifyRun)
	}
	return a
}

// owner identifies who a request acts as: a signed-in user, or a guest on trial.
// With sign-in off there is no owner and everything is shared.
func (a *API) owner(r *http.Request) (id, name string) {
	if u := auth.UserFrom(r.Context()); u != nil {
		return u.ID, u.Email
	}
	if g := auth.GuestFrom(r.Context()); g != nil {
		return g.Owner, "guest"
	}
	return "", ""
}

// may reports whether the caller may act on something owned by ownerID. With
// sign-in off everything is allowed. With it on, people see only their own work:
// administrators manage accounts and settings, but their test history is their
// own like anyone else's. Unowned work is nobody's.
func (a *API) may(r *http.Request, ownerID string) bool {
	if a.auth == nil {
		return true
	}
	id, _ := a.owner(r)
	return id != "" && ownerID == id
}

// runFor finds a run the caller may see. Someone else's run answers 404, not 403,
// so ids cannot be probed.
func (a *API) runFor(w http.ResponseWriter, r *http.Request) (*Run, bool) {
	run, ok := a.mgr.Run(r.PathValue("id"))
	if !ok || !a.may(r, run.view().Owner) {
		writeErr(w, 404, "run not found")
		return nil, false
	}
	return run, true
}

func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !a.sameOrigin(r) {
		writeErr(w, 403, "cross-origin requests are not allowed")
		return
	}
	// The load generator is driven by fetch/EventSource, which send no
	// Content-Type for simple requests. Rejecting anything else would break the
	// UI, so require an explicit marker header instead: browsers will not add
	// it cross-origin without a preflight, and this API never opts into CORS.
	if r.Method == http.MethodPost || r.Method == http.MethodDelete {
		if r.Header.Get("X-Requested-With") != "blasta" {
			writeErr(w, 403, "missing X-Requested-With: blasta header")
			return
		}
	}
	if a.auth != nil {
		var ok bool
		if r, ok = a.auth.Gate(w, r); !ok {
			return
		}
	}
	a.mux.ServeHTTP(w, r)
}

// sameOrigin allows requests with no Origin header (curl, the CLI, same-origin
// navigations) and rejects browser-initiated cross-origin requests.
func (a *API) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return a.allowedHost(r, u.Host)
}

func (a *API) routes() {
	// Local-only control plane. Cross-origin requests are rejected so a page on
	// another site cannot drive the load generator via the user's browser.
	a.mux.HandleFunc("GET /api/health", a.handleHealth)
	a.mux.HandleFunc("GET /api/executors", a.handleExecutors)
	a.mux.HandleFunc("GET /api/presets", a.handleListPresets)
	a.mux.HandleFunc("GET /api/presets/{id}", a.handleGetPreset)
	a.mux.HandleFunc("POST /api/presets/{id}/render", a.handleRenderPreset)
	a.mux.HandleFunc("POST /api/import", a.handleImport)
	a.mux.HandleFunc("GET /api/my-templates", a.handleListMySets)
	a.mux.HandleFunc("POST /api/my-templates", a.handleCreateMySet)
	a.mux.HandleFunc("GET /api/my-templates/{id}", a.handleGetMySet)
	a.mux.HandleFunc("PUT /api/my-templates/{id}", a.handleUpdateMySet)
	a.mux.HandleFunc("DELETE /api/my-templates/{id}", a.handleDeleteMySet)
	a.mux.HandleFunc("POST /api/my-templates/{id}/duplicate", a.handleDuplicateMySet)
	a.mux.HandleFunc("GET /api/my-favorites", a.handleListMyFavorites)
	a.mux.HandleFunc("POST /api/my-favorites", a.handleCreateMyFavorite)
	a.mux.HandleFunc("GET /api/my-favorites/{id}", a.handleGetMyFavorite)
	a.mux.HandleFunc("PUT /api/my-favorites/{id}", a.handleUpdateMyFavorite)
	a.mux.HandleFunc("DELETE /api/my-favorites/{id}", a.handleDeleteMyFavorite)
	a.mux.HandleFunc("POST /api/my-favorites/{id}/duplicate", a.handleDuplicateMyFavorite)
	a.mux.HandleFunc("GET /api/me/export", a.handleExportMe)
	a.mux.HandleFunc("DELETE /api/me", a.handleDeleteMe)
	a.mux.HandleFunc("GET /api/jobs", a.handleListJobs)
	a.mux.HandleFunc("POST /api/jobs", a.handleCreateJob)
	a.mux.HandleFunc("GET /api/jobs/{id}", a.handleGetJob)
	a.mux.HandleFunc("DELETE /api/jobs/{id}", a.handleDeleteJob)
	a.mux.HandleFunc("POST /api/jobs/{id}/start", a.handleStart)
	a.mux.HandleFunc("GET /api/runs", a.handleListRuns)
	a.mux.HandleFunc("DELETE /api/runs", a.handleClearRuns)
	a.mux.HandleFunc("GET /api/runs/{id}", a.handleGetRun)
	a.mux.HandleFunc("POST /api/runs/{id}/baseline", a.handleSetBaseline)
	a.mux.HandleFunc("GET /api/runs/{id}/compare", a.handleCompare)
	a.mux.HandleFunc("DELETE /api/runs/{id}", a.handleDeleteRun)
	a.mux.HandleFunc("POST /api/runs/{id}/stop", a.handleStop)
	a.mux.HandleFunc("GET /api/runs/{id}/metrics", a.handleMetrics)
	a.mux.HandleFunc("GET /api/runs/{id}/stream", a.handleStream)
	a.mux.HandleFunc("GET /api/runs/{id}/report", a.handleReport)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

// maxJobBytes caps job documents so an oversized request cannot be buffered.
const maxJobBytes = 1 << 20

func (a *API) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"status": "ok", "time": time.Now(), "version": version.Version})
}

func (a *API) handleExecutors(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"executors": engine.Registered()})
}

func (a *API) handleListJobs(w http.ResponseWriter, r *http.Request) {
	jobs := []config.Job{}
	for _, j := range a.mgr.Jobs() {
		if a.may(r, a.mgr.JobOwner(j.ID)) {
			jobs = append(jobs, j)
		}
	}
	writeJSON(w, 200, map[string]any{"jobs": jobs})
}

func (a *API) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, maxJobBytes))
	if err != nil {
		writeErr(w, 400, "could not read body: "+err.Error())
		return
	}
	// Decode applies defaults for fields the document omitted, based on which
	// JSON keys were actually present.
	job, err := config.Decode(body)
	if err != nil {
		writeErr(w, 400, "invalid json: "+err.Error())
		return
	}
	if g := auth.GuestFrom(r.Context()); g != nil {
		if a.auth.GuestNeedsCheck(g) {
			writeJSON(w, 428, map[string]string{"error": auth.ErrGuestCaptcha.Error(), "code": "captcha_required"})
			return
		}
		if err := guestJobError(&job, g, a.mgr); err != nil {
			writeErr(w, 403, err.Error())
			return
		}
	}
	saved, err := a.mgr.SaveJob(job)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if id, name := a.owner(r); id != "" {
		a.mgr.SetJobOwner(saved.ID, id, name)
	}
	writeJSON(w, 201, saved)
}

func (a *API) handleGetJob(w http.ResponseWriter, r *http.Request) {
	job, ok := a.mgr.Job(r.PathValue("id"))
	if !ok || !a.may(r, a.mgr.JobOwner(job.ID)) {
		writeErr(w, 404, "job not found")
		return
	}
	writeJSON(w, 200, job)
}

func (a *API) handleDeleteJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !a.may(r, a.mgr.JobOwner(id)) {
		writeErr(w, 404, "job not found")
		return
	}
	a.mgr.mu.Lock()
	delete(a.mgr.jobs, id)
	delete(a.mgr.jobOwner, id)
	delete(a.mgr.jobAt, id)
	a.mgr.mu.Unlock()
	w.WriteHeader(204)
}

func (a *API) handleStart(w http.ResponseWriter, r *http.Request) {
	if !a.may(r, a.mgr.JobOwner(r.PathValue("id"))) {
		writeErr(w, 404, "job not found")
		return
	}
	if g := auth.GuestFrom(r.Context()); g != nil {
		if _, running := a.mgr.CountOwned(g.Owner); running {
			writeErr(w, 409, "a trial test is already running: wait for it to finish")
			return
		}
		if err := a.auth.GuestStartRun(g, a.auth.ClientIP(r)); err != nil {
			writeErr(w, 403, err.Error())
			return
		}
		a.mgr.PruneGuests()
	}
	if a.auth != nil {
		// Several jobs may run at once; the limit keeps one person from taking the whole machine.
		if id, _ := a.owner(r); id != "" && a.mgr.RunningOwned(id) >= maxRunningPerPerson() {
			writeErr(w, 429, fmt.Sprintf("you already have %d jobs running: stop one or wait for it to finish", maxRunningPerPerson()))
			return
		}
	}
	run, err := a.mgr.Start(r.PathValue("id"))
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if a.auth != nil {
		a.runBase.Store(run.ID, a.siteBase(r)) // where to link to from a notification
	}
	writeJSON(w, 202, run.view())
}

func (a *API) handleListRuns(w http.ResponseWriter, r *http.Request) {
	runs := []RunView{}
	state := r.URL.Query().Get("state") // ?state=running: only what is running now (small, cheap to poll)
	for _, v := range a.mgr.Runs() {
		if a.may(r, v.Owner) && (state == "" || v.State == state) {
			runs = append(runs, v)
		}
	}
	writeJSON(w, 200, map[string]any{"runs": runs})
}

// isAdmin reports whether the caller may delete history. With sign-in off everyone
// is the operator; with it on, only administrators (ordinary users keep a record
// they cannot edit).
func (a *API) isAdmin(r *http.Request) bool {
	if a.auth == nil {
		return true
	}
	u := auth.UserFrom(r.Context())
	return u != nil && u.Role == auth.RoleAdmin
}

// handleDeleteRun removes one run from the caller's own history. Administrators only.
func (a *API) handleDeleteRun(w http.ResponseWriter, r *http.Request) {
	if !a.isAdmin(r) {
		writeErr(w, 403, "only administrators can delete history")
		return
	}
	run, ok := a.runFor(w, r)
	if !ok {
		return
	}
	if err := a.mgr.DeleteRun(run.view().ID); err != nil {
		code := 404
		if errors.Is(err, ErrRunning) {
			code = 409
		}
		writeErr(w, code, err.Error())
		return
	}
	w.WriteHeader(204)
}

// handleClearRuns deletes finished runs: the caller's own, or with ?scope=all
// everyone's. Administrators only.
func (a *API) handleClearRuns(w http.ResponseWriter, r *http.Request) {
	if !a.isAdmin(r) {
		writeErr(w, 403, "only administrators can clear history")
		return
	}
	owner, _ := a.owner(r)
	all := r.URL.Query().Get("scope") == "all"
	n, err := a.mgr.ClearRuns(owner, all)
	if err != nil {
		writeErr(w, 500, "could not clear the history: "+err.Error())
		return
	}
	a.log.Info("history cleared", "by", owner, "all", all, "runs", n)
	writeJSON(w, 200, map[string]any{"deleted": n})
}

func (a *API) handleGetRun(w http.ResponseWriter, r *http.Request) {
	run, ok := a.runFor(w, r)
	if !ok {
		return
	}
	writeJSON(w, 200, run.view())
}

func (a *API) handleStop(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.runFor(w, r); !ok {
		return
	}
	if err := a.mgr.Stop(r.PathValue("id")); err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	writeJSON(w, 202, map[string]string{"status": "stopping"})
}

func (a *API) handleMetrics(w http.ResponseWriter, r *http.Request) {
	run, ok := a.runFor(w, r)
	if !ok {
		return
	}
	if run.col == nil {
		writeErr(w, 410, "live metrics are not kept for runs restored from history; download the report instead")
		return
	}
	writeJSON(w, 200, map[string]any{
		"current": a.mgr.snapshotOf(run),
		"series":  run.col.Series(),
	})
}

// handleStream pushes live snapshots over Server-Sent Events.
func (a *API) handleStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if r0, ok := a.runFor(w, r); !ok {
		return
	} else if r0.col == nil {
		writeErr(w, 410, "run was restored from history and has no live stream")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch, unsubscribe := a.mgr.Subscribe(id)
	defer unsubscribe()

	// Seed with current state.
	if run, ok := a.mgr.Run(id); ok {
		if err := writeSSE(w, a.mgr.snapshotOf(run)); err != nil {
			return
		}
		flusher.Flush()
	}

	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case snap, ok := <-ch:
			if !ok {
				return
			}
			if err := writeSSE(w, snap); err != nil {
				return
			}
			flusher.Flush()
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeSSE(w http.ResponseWriter, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", b)
	return err
}

// handleReport exports the final run report as JSON or CSV. If the run is
// still active it reports the live aggregate rather than an empty summary.
func (a *API) handleReport(w http.ResponseWriter, r *http.Request) {
	run, ok := a.runFor(w, r)
	if !ok {
		return
	}
	v := run.view()
	summary := v.Summary
	if v.State == "running" {
		summary = run.col.Summary(v.ID, v.JobName, v.Executor, v.Target, false, "in progress")
		if run.rec != nil {
			summary.Resources = run.rec.Summary()
		}
	}

	// Path segments come from routing, but the filename is still sanitised
	// before it reaches a Content-Disposition header.
	if format := r.URL.Query().Get("format"); format == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition",
			`attachment; filename="`+safeFilename(v.ID)+`.csv"`)
		_, _ = w.Write([]byte(summaryCSV(summary)))
		return
	}
	w.Header().Set("Content-Disposition",
		`attachment; filename="`+safeFilename(v.ID)+`.json"`)
	writeJSON(w, 200, summary)
}

// summaryCSV renders the headline numbers plus the latency distribution.
func summaryCSV(s collector.Summary) string {
	var b strings.Builder
	b.WriteString("metric,value\n")
	row := func(k string, v any) {
		fmt.Fprintf(&b, "%s,%v\n", k, v)
	}
	row("run_id", s.RunID)
	row("job_name", s.JobName)
	row("executor", s.Executor)
	row("target", s.Target)
	row("duration_ms", s.DurationMs)
	row("total", s.Total)
	row("success", s.Success)
	row("errors", s.Errors)
	row("skipped", s.Skipped)
	row("avg_rps", fmt.Sprintf("%.2f", s.AvgRPS))
	row("bytes", s.BytesTotal)
	row("throughput_bps", fmt.Sprintf("%.2f", s.Throughput))
	row("aborted", s.Aborted)
	if r := s.Resources; r != nil {
		// What the load generator itself used, so a result can be judged against it.
		row("server_scope", r.Scope)
		row("server_cores", fmt.Sprintf("%.1f", r.Cores))
		row("server_cpu_avg_pct", fmt.Sprintf("%.1f", r.CPUAvgPct))
		row("server_cpu_peak_pct", fmt.Sprintf("%.1f", r.CPUPeakPct))
		row("server_cpu_avg_cores", fmt.Sprintf("%.2f", r.CPUAvgCores))
		row("server_cpu_peak_cores", fmt.Sprintf("%.2f", r.CPUPeakCores))
		row("server_mem_start_bytes", r.MemStart)
		row("server_mem_avg_bytes", r.MemAvg)
		row("server_mem_peak_bytes", r.MemPeak)
		row("server_mem_limit_bytes", r.MemLimit)
	}

	b.WriteString("\nstatus_codes,count\n")
	for _, k := range collector.SortCounts(s.StatusCodes) {
		row(k, s.StatusCodes[k])
	}
	b.WriteString("\nerror_kinds,count\n")
	for _, k := range collector.SortCounts(s.ErrorKinds) {
		row(k, s.ErrorKinds[k])
	}
	b.WriteString("\nlatency_percentile,microseconds\n")
	for _, k := range []string{"p50", "p75", "p90", "p95", "p99", "p99_9", "p99_99", "p99_999"} {
		if v, ok := s.Latency.Latency[k]; ok {
			row(k, v)
		}
	}
	return b.String()
}

// safeFilename strips anything that could break out of a quoted
// Content-Disposition filename or inject a header.
func safeFilename(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if out == "" {
		return "run"
	}
	return out
}

// parseInt is a small helper for query params.
func parseInt(s string, def int) int {
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}

// maxRunningPerPerson is how many jobs one signed-in person may have running at once (default 5;
// the BLASTA_MAX_RUNNING environment variable changes it).
func maxRunningPerPerson() int {
	if n, err := strconv.Atoi(os.Getenv("BLASTA_MAX_RUNNING")); err == nil && n > 0 {
		return n
	}
	return 5
}
