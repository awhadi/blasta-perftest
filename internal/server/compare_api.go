package server

import (
	"net/http"
	"strconv"

	"github.com/awhadi/blasta-perftest/internal/compare"
)

// handleSetBaseline marks a finished run as the one to compare that test's later runs against.
func (a *API) handleSetBaseline(w http.ResponseWriter, r *http.Request) {
	run, ok := a.runFor(w, r)
	if !ok {
		return
	}
	var in struct {
		Baseline bool `json:"baseline"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if err := a.mgr.SetBaseline(run.ID, in.Baseline); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, run.view())
}

func limitParam(r *http.Request, key string) float64 {
	if v := r.URL.Query().Get(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 && f <= 1e6 {
			return f
		}
	}
	return -1
}

// handleCompare sets this run beside another of the same person's (?to=<id>, the baseline), and
// judges it against the limits in the address: latency, errors and throughput.
func (a *API) handleCompare(w http.ResponseWriter, r *http.Request) {
	run, ok := a.runFor(w, r)
	if !ok {
		return
	}
	other, found := a.mgr.Run(r.URL.Query().Get("to"))
	if !found || !a.may(r, other.view().Owner) {
		writeErr(w, http.StatusNotFound, "the run to compare with was not found")
		return
	}
	now, base := run.view(), other.view()
	if now.State == "running" || base.State == "running" {
		writeErr(w, http.StatusConflict, "wait for both runs to finish")
		return
	}
	lim := compare.Limits{LatencyPct: limitParam(r, "latency"), ErrorPoints: limitParam(r, "errors"), ThroughputPct: limitParam(r, "throughput")}
	writeJSON(w, http.StatusOK, map[string]any{
		"result": compare.Runs(base.Summary, now.Summary, lim),
		"run":    map[string]any{"id": now.ID, "startedAt": now.StartedAt, "jobName": now.JobName},
		"base":   map[string]any{"id": base.ID, "startedAt": base.StartedAt, "jobName": base.JobName, "baseline": base.Baseline},
	})
}
