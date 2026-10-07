package server

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/awhadi/blasta-perftest/internal/auth"
	"github.com/awhadi/blasta-perftest/internal/config"
	"github.com/awhadi/blasta-perftest/internal/db"
)

// People can save the setup of a test as a template of their own and run it again later. A
// template is private to its owner. Its configuration (which may hold an Authorization header or
// a login body) is kept encrypted with the site's key; only the name, description, protocol and
// a short "GET https://host/path" line (no query string) are readable in the database.
const (
	maxUserTemplates   = 100
	maxTemplateBytes   = 256 << 10
	maxTemplateNameLen = 120
	maxTemplateDesc    = 500
)

var secretHeaderWords = []string{"authorization", "cookie", "token", "secret", "key", "password", "passwd", "auth", "session", "signature", "credential"}

// isSecretHeader says whether a header probably carries a credential.
func isSecretHeader(name string) bool {
	n := strings.ToLower(name)
	for _, w := range secretHeaderWords {
		if strings.Contains(n, w) {
			return true
		}
	}
	return false
}

// scrubbed is a copy of the job without the values of credential-like headers, for anything that
// leaves the site (an exported file, the data download).
func scrubbed(j config.Job) config.Job {
	if len(j.Headers) == 0 {
		return j
	}
	h := make(map[string]string, len(j.Headers))
	for k, v := range j.Headers {
		if isSecretHeader(k) {
			v = ""
		}
		h[k] = v
	}
	j.Headers = h
	return j
}

type templateRow struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Executor    string    `json:"executor"`
	Summary     string    `json:"summary"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type templateFull struct {
	templateRow
	Job json.RawMessage `json:"job"`
}

type templateBody struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Job         json.RawMessage `json:"job"`
}

func newTemplateID() string {
	b := make([]byte, 9)
	_, _ = rand.Read(b)
	return "tpl_" + hex.EncodeToString(b)
}

// templateSummary is the readable line kept next to a template: the method and where it goes,
// without a query string or credentials in the address.
func templateSummary(j config.Job) string {
	t := j.Target.URL
	if u, err := url.Parse(t); err == nil && u.Host != "" {
		u.RawQuery, u.Fragment, u.User = "", "", nil
		t = u.String()
	}
	s := strings.TrimSpace(j.Method + " " + t)
	if len(s) > 250 {
		s = s[:250]
	}
	return s
}

func (a *API) templatesReady(w http.ResponseWriter, r *http.Request) (string, *db.DB, bool) {
	u := auth.UserFrom(r.Context())
	d := a.mgr.DB()
	if a.auth == nil || u == nil || d == nil {
		writeErr(w, http.StatusUnauthorized, "sign in to save favorites")
		return "", nil, false
	}
	return u.ID, d, true
}

func rowFrom(sc interface{ Scan(...any) error }) (templateRow, error) {
	var t templateRow
	var c, u int64
	if err := sc.Scan(&t.ID, &t.Name, &t.Description, &t.Executor, &t.Summary, &c, &u); err != nil {
		return t, err
	}
	t.CreatedAt, t.UpdatedAt = time.UnixMilli(c).UTC(), time.UnixMilli(u).UTC()
	return t, nil
}

const templateCols = `id, name, description, executor, summary, created_at, updated_at`

func (a *API) handleListMyFavorites(w http.ResponseWriter, r *http.Request) {
	owner, d, ok := a.templatesReady(w, r)
	if !ok {
		return
	}
	rows, err := d.Query(`SELECT `+templateCols+` FROM user_templates WHERE owner = ? ORDER BY updated_at DESC`, owner)
	if err != nil {
		writeErr(w, 500, "could not read your favorites")
		return
	}
	defer rows.Close()
	out := []templateRow{}
	for rows.Next() {
		if t, err := rowFrom(rows); err == nil {
			out = append(out, t)
		}
	}
	writeJSON(w, 200, map[string]any{"favorites": out, "max": maxUserTemplates})
}

// cleanBody checks a template's name, description and job, and returns the job normalised and
// ready to be sealed.
func cleanBody(in templateBody, needJob bool) (string, string, *config.Job, error) {
	name := strings.TrimSpace(in.Name)
	desc := strings.TrimSpace(in.Description)
	if name == "" {
		return "", "", nil, errors.New("give the favorite a name")
	}
	if len([]rune(name)) > maxTemplateNameLen {
		return "", "", nil, errors.New("the name is too long")
	}
	if len([]rune(desc)) > maxTemplateDesc {
		return "", "", nil, errors.New("the description is too long")
	}
	if len(in.Job) == 0 {
		if needJob {
			return "", "", nil, errors.New("there is no test setup to save")
		}
		return name, desc, nil, nil
	}
	if len(in.Job) > maxTemplateBytes {
		return "", "", nil, errors.New("this setup is too large to save")
	}
	job, err := config.Decode(in.Job)
	if err != nil {
		return "", "", nil, errors.New("this setup cannot be saved yet: " + err.Error())
	}
	if err := job.Validate(); err != nil {
		return "", "", nil, errors.New("this setup cannot be saved yet: " + err.Error())
	}
	job.Name, job.ID = name, ""
	return name, desc, &job, nil
}

func (a *API) sealJob(job config.Job) (string, error) {
	b, err := json.Marshal(job)
	if err != nil {
		return "", err
	}
	return a.auth.SealText(string(b))
}

func (a *API) openJob(sealed string) (json.RawMessage, error) {
	plain, err := a.auth.OpenText(sealed)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(plain), nil
}

func (a *API) handleCreateMyFavorite(w http.ResponseWriter, r *http.Request) {
	owner, d, ok := a.templatesReady(w, r)
	if !ok {
		return
	}
	var in templateBody
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxTemplateBytes+4096))
	if err != nil || json.Unmarshal(body, &in) != nil {
		writeErr(w, 400, "invalid request")
		return
	}
	name, desc, job, err := cleanBody(in, true)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	var n int
	_ = d.QueryRow(`SELECT COUNT(*) FROM user_templates WHERE owner = ?`, owner).Scan(&n)
	if n >= maxUserTemplates {
		writeErr(w, 409, "you have saved the most favorites allowed; delete one first")
		return
	}
	sealed, err := a.sealJob(*job)
	if err != nil {
		writeErr(w, 503, "favorites cannot be saved on this site: "+err.Error())
		return
	}
	now := time.Now().UTC()
	t := templateRow{ID: newTemplateID(), Name: name, Description: desc, Executor: job.Executor, Summary: templateSummary(*job), CreatedAt: now, UpdatedAt: now}
	if _, err := d.Exec(`INSERT INTO user_templates (id, owner, name, description, executor, summary, data, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?)`,
		t.ID, owner, t.Name, t.Description, t.Executor, t.Summary, sealed, now.UnixMilli(), now.UnixMilli()); err != nil {
		writeErr(w, 500, "could not save the favorite")
		return
	}
	writeJSON(w, 201, t)
}

// ownTemplate loads a template of the signed-in person, or answers 404 (so ids cannot be probed).
func (a *API) ownTemplate(w http.ResponseWriter, r *http.Request) (string, *db.DB, templateRow, string, bool) {
	owner, d, ok := a.templatesReady(w, r)
	if !ok {
		return "", nil, templateRow{}, "", false
	}
	var sealed string
	var t templateRow
	var c, u int64
	err := d.QueryRow(`SELECT `+templateCols+`, data FROM user_templates WHERE id = ? AND owner = ?`, r.PathValue("id"), owner).
		Scan(&t.ID, &t.Name, &t.Description, &t.Executor, &t.Summary, &c, &u, &sealed)
	if errors.Is(err, sql.ErrNoRows) || err != nil {
		writeErr(w, 404, "favorite not found")
		return "", nil, templateRow{}, "", false
	}
	t.CreatedAt, t.UpdatedAt = time.UnixMilli(c).UTC(), time.UnixMilli(u).UTC()
	return owner, d, t, sealed, true
}

func (a *API) handleGetMyFavorite(w http.ResponseWriter, r *http.Request) {
	_, _, t, sealed, ok := a.ownTemplate(w, r)
	if !ok {
		return
	}
	job, err := a.openJob(sealed)
	if err != nil {
		writeErr(w, 500, "this favorite cannot be read (the site's key may have changed)")
		return
	}
	writeJSON(w, 200, templateFull{templateRow: t, Job: job})
}

func (a *API) handleUpdateMyFavorite(w http.ResponseWriter, r *http.Request) {
	owner, d, t, sealed, ok := a.ownTemplate(w, r)
	if !ok {
		return
	}
	var in templateBody
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxTemplateBytes+4096))
	if err != nil || json.Unmarshal(body, &in) != nil {
		writeErr(w, 400, "invalid request")
		return
	}
	name, desc, job, err := cleanBody(in, false)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	t.Name, t.Description, t.UpdatedAt = name, desc, time.Now().UTC()
	if job != nil {
		if sealed, err = a.sealJob(*job); err != nil {
			writeErr(w, 503, "favorites cannot be saved on this site: "+err.Error())
			return
		}
		t.Executor, t.Summary = job.Executor, templateSummary(*job)
	}
	if _, err := d.Exec(`UPDATE user_templates SET name = ?, description = ?, executor = ?, summary = ?, data = ?, updated_at = ? WHERE id = ? AND owner = ?`,
		t.Name, t.Description, t.Executor, t.Summary, sealed, t.UpdatedAt.UnixMilli(), t.ID, owner); err != nil {
		writeErr(w, 500, "could not save the favorite")
		return
	}
	writeJSON(w, 200, t)
}

func (a *API) handleDeleteMyFavorite(w http.ResponseWriter, r *http.Request) {
	owner, d, t, _, ok := a.ownTemplate(w, r)
	if !ok {
		return
	}
	if _, err := d.Exec(`DELETE FROM user_templates WHERE id = ? AND owner = ?`, t.ID, owner); err != nil {
		writeErr(w, 500, "could not delete the favorite")
		return
	}
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

func (a *API) handleDuplicateMyFavorite(w http.ResponseWriter, r *http.Request) {
	owner, d, t, sealed, ok := a.ownTemplate(w, r)
	if !ok {
		return
	}
	var n int
	_ = d.QueryRow(`SELECT COUNT(*) FROM user_templates WHERE owner = ?`, owner).Scan(&n)
	if n >= maxUserTemplates {
		writeErr(w, 409, "you have saved the most favorites allowed; delete one first")
		return
	}
	now := time.Now().UTC()
	name := t.Name
	if len([]rune(name)) > maxTemplateNameLen-7 {
		name = string([]rune(name)[:maxTemplateNameLen-7])
	}
	t.ID, t.Name, t.CreatedAt, t.UpdatedAt = newTemplateID(), name+" (copy)", now, now
	if _, err := d.Exec(`INSERT INTO user_templates (id, owner, name, description, executor, summary, data, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?)`,
		t.ID, owner, t.Name, t.Description, t.Executor, t.Summary, sealed, now.UnixMilli(), now.UnixMilli()); err != nil {
		writeErr(w, 500, "could not duplicate the favorite")
		return
	}
	writeJSON(w, 201, t)
}

var nonFile = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// handleExportMyFavorite sends a template as a job file (it also runs with `blasta run`). Values of
// credential-like headers are left empty: a file travels, the secrets stay on the site.
func (a *API) handleExportMyFavorite(w http.ResponseWriter, r *http.Request) {
	_, _, t, sealed, ok := a.ownTemplate(w, r)
	if !ok {
		return
	}
	raw, err := a.openJob(sealed)
	if err != nil {
		writeErr(w, 500, "this favorite cannot be read")
		return
	}
	job, err := config.Decode(raw)
	if err != nil {
		writeErr(w, 500, "this favorite cannot be read")
		return
	}
	job = scrubbed(job)
	job.Name, job.Description = t.Name, t.Description
	file := strings.Trim(nonFile.ReplaceAllString(t.Name, "-"), "-")
	if file == "" {
		file = "job"
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+file+`.json"`)
	writeJSON(w, 200, job)
}

// exportFavorites is the people's-data download: their templates, without credential values.
func (a *API) exportFavorites(owner string) []map[string]any {
	d := a.mgr.DB()
	out := []map[string]any{}
	if d == nil || a.auth == nil {
		return out
	}
	rows, err := d.Query(`SELECT `+templateCols+`, data FROM user_templates WHERE owner = ? ORDER BY updated_at DESC`, owner)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var t templateRow
		var c, u int64
		var sealed string
		if rows.Scan(&t.ID, &t.Name, &t.Description, &t.Executor, &t.Summary, &c, &u, &sealed) != nil {
			continue
		}
		item := map[string]any{"id": t.ID, "name": t.Name, "description": t.Description, "createdAt": time.UnixMilli(c).UTC(), "updatedAt": time.UnixMilli(u).UTC()}
		if raw, err := a.openJob(sealed); err == nil {
			if job, err := config.Decode(raw); err == nil {
				item["job"] = scrubbed(job)
			}
		}
		out = append(out, item)
	}
	return out
}
