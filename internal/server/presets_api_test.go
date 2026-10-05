package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func get(t *testing.T, a *API, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	req.Header.Set("X-Requested-With", "blasta")
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)
	return rec
}

func TestListPresets(t *testing.T) {
	a, _ := testAPI(t)
	rec := get(t, a, "/api/presets")
	if rec.Code != 200 {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body)
	}
	var body struct {
		Presets []presetSummary `json:"presets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Presets) == 0 {
		t.Fatal("no presets returned")
	}
	// The listing must stay lightweight: no job bodies.
	if strings.Contains(rec.Body.String(), "\"jobs\":[") {
		t.Error("listing should not embed job templates")
	}
	var wp bool
	for _, p := range body.Presets {
		if p.ID == "wordpress" {
			wp = true
		}
	}
	if !wp {
		t.Error("wordpress preset missing from listing")
	}
}

func TestListPresetsCategoryFilter(t *testing.T) {
	a, _ := testAPI(t)
	rec := get(t, a, "/api/presets?category=CMS")
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		Presets []presetSummary `json:"presets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Presets) == 0 {
		t.Fatal("category filter returned nothing")
	}
	for _, p := range body.Presets {
		if !strings.EqualFold(p.Category, "CMS") {
			t.Errorf("got %s in CMS filter", p.Category)
		}
	}
}

func TestGetPreset(t *testing.T) {
	a, _ := testAPI(t)
	rec := get(t, a, "/api/presets/wordpress")
	if rec.Code != 200 {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "{{url}}") {
		t.Error("template placeholders should be present in the raw preset")
	}
	if rec := get(t, a, "/api/presets/nope"); rec.Code != 404 {
		t.Errorf("unknown preset status = %d, want 404", rec.Code)
	}
}

func TestRenderPreset(t *testing.T) {
	a, _ := testAPI(t)
	rec := post(t, a, "/api/presets/wordpress/render",
		`{"values":{"url":"https://wp.test","post":"my-post"}}`)
	if rec.Code != 200 {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body)
	}
	var body struct {
		Jobs []renderedPresetJob `json:"jobs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Jobs) == 0 {
		t.Fatal("no jobs rendered")
	}
	var found bool
	for _, j := range body.Jobs {
		if strings.Contains(string(j.Job), "{{") {
			t.Errorf("job %s still has a placeholder: %s", j.JobID, j.Job)
		}
		if j.JobID == "single-post" {
			found = true
			if !strings.Contains(string(j.Job), "https://wp.test/my-post/") {
				t.Errorf("single-post url wrong: %s", j.Job)
			}
		}
	}
	if !found {
		t.Error("single-post job not rendered")
	}
}

func TestRenderPresetSingleJob(t *testing.T) {
	a, _ := testAPI(t)
	rec := post(t, a, "/api/presets/wordpress/render",
		`{"values":{"url":"https://wp.test","post":"p"},"job":"front"}`)
	if rec.Code != 200 {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body)
	}
	var body struct {
		Jobs []renderedPresetJob `json:"jobs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Jobs) != 1 || body.Jobs[0].JobID != "front" {
		t.Fatalf("expected only the front job, got %d", len(body.Jobs))
	}
}

func TestRenderPresetUnknownJob(t *testing.T) {
	a, _ := testAPI(t)
	rec := post(t, a, "/api/presets/wordpress/render",
		`{"values":{"url":"https://wp.test","post":"p"},"job":"nope"}`)
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// Rendering must never expand the server's environment, and must not echo a
// credential back to the caller.
func TestRenderPresetDoesNotExpandEnvOrLeakSecrets(t *testing.T) {
	t.Setenv("WP_ADMIN_COOKIE", "super-secret-session")
	a, _ := testAPI(t)
	rec := post(t, a, "/api/presets/wordpress/render",
		`{"values":{"url":"https://wp.test","post":"p","adminCookie":"also-secret"}}`)
	if rec.Code != 200 {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "super-secret-session") {
		t.Error("response leaked an environment variable")
	}
	if strings.Contains(rec.Body.String(), "also-secret") {
		t.Error("response echoed the supplied credential")
	}
}

func TestRenderPresetReportsUnresolvedDefaults(t *testing.T) {
	a, _ := testAPI(t)
	// No values at all: the placeholder defaults should be reported back so the
	// UI can warn the user.
	rec := post(t, a, "/api/presets/wordpress/render", `{}`)
	if rec.Code != 200 {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body)
	}
	var body struct {
		Warnings []unresolvedWarning `json:"warnings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Warnings) != 2 {
		t.Errorf("expected url and post warnings, got %d: %v", len(body.Warnings), body.Warnings)
	}
}

// A value containing quotes must not break the JSON the API returns.
func TestRenderPresetHostileValue(t *testing.T) {
	a, _ := testAPI(t)
	rec := post(t, a, "/api/presets/wordpress/render",
		`{"values":{"url":"https://a.test/?x=\"1\"\\y","post":"p"}}`)
	if rec.Code != 200 {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body)
	}
	var body struct {
		Jobs []renderedPresetJob `json:"jobs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if len(body.Jobs) == 0 {
		t.Fatal("no jobs rendered")
	}
}

// The listing carries job names so the UI can search by what a template tests.
func TestListPresetsIncludesJobNames(t *testing.T) {
	a, _ := testAPI(t)
	rec := get(t, a, "/api/presets")
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		Presets []struct {
			ID      string `json:"id"`
			Jobs    int    `json:"jobs"`
			JobList []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"jobList"`
		} `json:"presets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, p := range body.Presets {
		if len(p.JobList) != p.Jobs || p.Jobs == 0 {
			t.Errorf("%s: jobList has %d entries, jobs = %d", p.ID, len(p.JobList), p.Jobs)
		}
		for _, j := range p.JobList {
			if j.ID == "" || j.Name == "" {
				t.Errorf("%s: job with empty id or name: %+v", p.ID, j)
			}
		}
	}
}
