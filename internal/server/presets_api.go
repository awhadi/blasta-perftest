package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/awhadi/blasta-perftest/internal/presets"
)

// presetSummary is the listing shape. It omits the job bodies so the UI can
// populate a picker cheaply.
type presetSummary struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Category string `json:"category"`
	Summary  string `json:"summary,omitempty"`
	// Description says what the template covers, for the cards and the detail page.
	Description string `json:"description,omitempty"`
	Stack       string `json:"stack,omitempty"`
	Jobs        int    `json:"jobs"`
	// JobList names every job so the UI can search across templates by what
	// they test (for example "spike" or "login storm") without fetching all 77.
	JobList []presetJobRef `json:"jobList"`
}

type presetJobRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// handleListPresets returns the catalogue, optionally filtered by ?category=.
func (a *API) handleListPresets(w http.ResponseWriter, r *http.Request) {
	cat := strings.TrimSpace(r.URL.Query().Get("category"))
	out := []presetSummary{}
	for _, p := range presets.All() {
		if cat != "" && !strings.EqualFold(p.Category, cat) {
			continue
		}
		refs := make([]presetJobRef, 0, len(p.Jobs))
		for _, j := range p.Jobs {
			refs = append(refs, presetJobRef{ID: j.ID, Name: j.Name})
		}
		out = append(out, presetSummary{
			ID: p.ID, Title: p.Title, Category: p.Category,
			Summary: p.Summary, Description: p.Description, Stack: p.Stack, Jobs: len(p.Jobs), JobList: refs,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"presets": out})
}

// handleGetPreset returns one preset with its variables and job templates.
func (a *API) handleGetPreset(w http.ResponseWriter, r *http.Request) {
	p, err := presets.Get(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

type renderPresetRequest struct {
	Values map[string]string `json:"values"`
	// Job restricts the response to a single job id.
	Job string `json:"job,omitempty"`
}

type renderedPresetJob struct {
	JobID  string          `json:"jobId"`
	Name   string          `json:"name"`
	Notes  string          `json:"notes,omitempty"`
	Safety string          `json:"safety,omitempty"`
	Job    json.RawMessage `json:"job"`
	// Values omits credentials so a rendered response never echoes a secret.
	Values map[string]string `json:"values,omitempty"`
}

// handleRenderPreset turns a preset into concrete job files.
//
// It substitutes only what the caller sent; variable defaults still apply, so a
// UI can post just the site URL. No environment variable is read here, by design:
// the API must never be able to read the server's environment back to a caller.
func (a *API) handleRenderPreset(w http.ResponseWriter, r *http.Request) {
	p, err := presets.Get(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	var req renderPresetRequest
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &req); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	rendered, err := p.Render(req.Values)
	if err != nil {
		// Name the variables the caller still has to supply.
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	out := []renderedPresetJob{}
	for _, rj := range rendered {
		if req.Job != "" && rj.JobID != req.Job {
			continue
		}
		out = append(out, renderedPresetJob{
			JobID: rj.JobID, Name: rj.Name, Notes: rj.Notes,
			Safety: rj.Safety, Job: rj.JSON, Values: rj.Values,
		})
	}
	if req.Job != "" && len(out) == 0 {
		writeErr(w, http.StatusNotFound, "preset "+p.ID+" has no job "+req.Job)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"preset":   p.ID,
		"jobs":     out,
		"warnings": unresolvedWarnings(p, req.Values),
	})
}

type unresolvedWarning struct {
	Name        string `json:"name"`
	Default     string `json:"default"`
	Description string `json:"description,omitempty"`
}

func unresolvedWarnings(p presets.Preset, values map[string]string) []unresolvedWarning {
	out := []unresolvedWarning{}
	for _, v := range p.Unresolved(values) {
		out = append(out, unresolvedWarning{
			Name: v.Name, Default: v.Default, Description: v.Description,
		})
	}
	return out
}
