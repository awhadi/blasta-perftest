package server

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/awhadi/blasta-perftest/internal/auth"
	"github.com/awhadi/blasta-perftest/internal/db"
	"github.com/awhadi/blasta-perftest/internal/presets"
)

// "My templates" are a person's own copies of the built-in templates: which template it is, what
// they call it, and the settings they filled in for it (the address of their system, and so on).
// Open one and the template page comes up with those settings already in. A copy keeps its own
// settings, whatever the built-in template's defaults are. The settings are kept sealed.
const (
	maxUserSets   = 100
	maxSetValues  = 40
	maxSetValue   = 1000
	maxSetNameLen = 120
)

type setRow struct {
	ID          string    `json:"id"`
	PresetID    string    `json:"presetId"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Title       string    `json:"title"`    // the built-in template's own title
	Category    string    `json:"category"` // and its category
	Jobs        int       `json:"jobs"`     // how many jobs it has
	Missing     bool      `json:"missing,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type setBody struct {
	PresetID    string            `json:"presetId"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Values      map[string]string `json:"values"`
}

func newSetID() string {
	b := make([]byte, 9)
	_, _ = rand.Read(b)
	return "tmpl_" + hex.EncodeToString(b)
}

func (a *API) setsReady(w http.ResponseWriter, r *http.Request) (string, *db.DB, bool) {
	u := auth.UserFrom(r.Context())
	d := a.mgr.DB()
	if a.auth == nil || u == nil || d == nil {
		writeErr(w, http.StatusUnauthorized, "sign in to add templates")
		return "", nil, false
	}
	return u.ID, d, true
}

// cleanValues keeps only settings the template really has (and that are typed in, not credentials,
// which stay environment references), within sensible sizes.
func cleanValues(p presets.Preset, in map[string]string) (map[string]string, error) {
	allowed := map[string]bool{}
	for _, v := range p.Variables {
		if v.Derived == "" && !v.Sensitive {
			allowed[v.Name] = true
		}
	}
	out := map[string]string{}
	for k, v := range in {
		if !allowed[k] {
			continue
		}
		if len([]rune(v)) > maxSetValue {
			return nil, errors.New("the setting " + k + " is too long")
		}
		if strings.TrimSpace(v) != "" {
			out[k] = strings.TrimSpace(v)
		}
	}
	if len(out) > maxSetValues {
		return nil, errors.New("too many settings")
	}
	return out, nil
}

func (a *API) sealValues(v map[string]string) (string, error) {
	b, err := json.Marshal(map[string]any{"values": v})
	if err != nil {
		return "", err
	}
	return a.auth.SealText(string(b))
}

func (a *API) openValues(sealed string) (map[string]string, error) {
	plain, err := a.auth.OpenText(sealed)
	if err != nil {
		return nil, err
	}
	var w struct {
		Values map[string]string `json:"values"`
	}
	if err := json.Unmarshal([]byte(plain), &w); err != nil {
		return nil, err
	}
	if w.Values == nil {
		w.Values = map[string]string{}
	}
	return w.Values, nil
}

func decorate(t setRow) setRow {
	if p, err := presets.Get(t.PresetID); err == nil {
		t.Title, t.Category, t.Jobs = p.Title, p.Category, len(p.Jobs)
	} else {
		t.Missing = true
	}
	return t
}

const setCols = `id, preset_id, name, description, created_at, updated_at`

func scanSet(sc interface{ Scan(...any) error }, extra ...any) (setRow, error) {
	var t setRow
	var c, u int64
	dest := append([]any{&t.ID, &t.PresetID, &t.Name, &t.Description, &c, &u}, extra...)
	if err := sc.Scan(dest...); err != nil {
		return t, err
	}
	t.CreatedAt, t.UpdatedAt = time.UnixMilli(c).UTC(), time.UnixMilli(u).UTC()
	return decorate(t), nil
}

func (a *API) handleListMySets(w http.ResponseWriter, r *http.Request) {
	owner, d, ok := a.setsReady(w, r)
	if !ok {
		return
	}
	rows, err := d.Query(`SELECT `+setCols+` FROM user_presets WHERE owner = ? ORDER BY updated_at DESC`, owner)
	if err != nil {
		writeErr(w, 500, "could not read your templates")
		return
	}
	defer rows.Close()
	out := []setRow{}
	for rows.Next() {
		if t, err := scanSet(rows); err == nil {
			out = append(out, t)
		}
	}
	writeJSON(w, 200, map[string]any{"templates": out, "max": maxUserSets})
}

func (a *API) ownSet(w http.ResponseWriter, r *http.Request) (string, *db.DB, setRow, string, bool) {
	owner, d, ok := a.setsReady(w, r)
	if !ok {
		return "", nil, setRow{}, "", false
	}
	var sealed string
	t, err := scanSet(d.QueryRow(`SELECT `+setCols+`, data FROM user_presets WHERE id = ? AND owner = ?`, r.PathValue("id"), owner), &sealed)
	if errors.Is(err, sql.ErrNoRows) || err != nil {
		writeErr(w, 404, "template not found")
		return "", nil, setRow{}, "", false
	}
	return owner, d, t, sealed, true
}

func (a *API) handleCreateMySet(w http.ResponseWriter, r *http.Request) {
	owner, d, ok := a.setsReady(w, r)
	if !ok {
		return
	}
	var in setBody
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil || json.Unmarshal(body, &in) != nil {
		writeErr(w, 400, "invalid request")
		return
	}
	p, err := presets.Get(in.PresetID)
	if err != nil {
		writeErr(w, 400, "that template does not exist")
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = p.Title
	}
	desc := strings.TrimSpace(in.Description)
	if len([]rune(name)) > maxSetNameLen || len([]rune(desc)) > maxTemplateDesc {
		writeErr(w, 400, "the name or description is too long")
		return
	}
	vals, err := cleanValues(p, in.Values)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	var n int
	_ = d.QueryRow(`SELECT COUNT(*) FROM user_presets WHERE owner = ?`, owner).Scan(&n)
	if n >= maxUserSets {
		writeErr(w, 409, "you have added the most templates allowed; remove one first")
		return
	}
	sealed, err := a.sealValues(vals)
	if err != nil {
		writeErr(w, 503, "templates cannot be added on this site: "+err.Error())
		return
	}
	now := time.Now().UTC()
	t := decorate(setRow{ID: newSetID(), PresetID: p.ID, Name: name, Description: desc, CreatedAt: now, UpdatedAt: now})
	if _, err := d.Exec(`INSERT INTO user_presets (id, owner, preset_id, name, description, data, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?)`,
		t.ID, owner, t.PresetID, t.Name, t.Description, sealed, now.UnixMilli(), now.UnixMilli()); err != nil {
		writeErr(w, 500, "could not add the template")
		return
	}
	writeJSON(w, 201, t)
}

func (a *API) handleGetMySet(w http.ResponseWriter, r *http.Request) {
	_, _, t, sealed, ok := a.ownSet(w, r)
	if !ok {
		return
	}
	vals, err := a.openValues(sealed)
	if err != nil {
		writeErr(w, 500, "this template cannot be read (the site's key may have changed)")
		return
	}
	writeJSON(w, 200, map[string]any{"template": t, "values": vals})
}

func (a *API) handleUpdateMySet(w http.ResponseWriter, r *http.Request) {
	owner, d, t, sealed, ok := a.ownSet(w, r)
	if !ok {
		return
	}
	var in struct {
		Name        *string           `json:"name"`
		Description *string           `json:"description"`
		Values      map[string]string `json:"values"`
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil || json.Unmarshal(body, &in) != nil {
		writeErr(w, 400, "invalid request")
		return
	}
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" || len([]rune(n)) > maxSetNameLen {
			writeErr(w, 400, "give the template a name (up to 120 characters)")
			return
		}
		t.Name = n
	}
	if in.Description != nil {
		dsc := strings.TrimSpace(*in.Description)
		if len([]rune(dsc)) > maxTemplateDesc {
			writeErr(w, 400, "the description is too long")
			return
		}
		t.Description = dsc
	}
	if in.Values != nil {
		p, err := presets.Get(t.PresetID)
		if err != nil {
			writeErr(w, 400, "the built-in template this was made from is no longer there")
			return
		}
		vals, err := cleanValues(p, in.Values)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		if sealed, err = a.sealValues(vals); err != nil {
			writeErr(w, 503, "templates cannot be saved on this site: "+err.Error())
			return
		}
	}
	t.UpdatedAt = time.Now().UTC()
	if _, err := d.Exec(`UPDATE user_presets SET name = ?, description = ?, data = ?, updated_at = ? WHERE id = ? AND owner = ?`,
		t.Name, t.Description, sealed, t.UpdatedAt.UnixMilli(), t.ID, owner); err != nil {
		writeErr(w, 500, "could not save the template")
		return
	}
	writeJSON(w, 200, t)
}

func (a *API) handleDeleteMySet(w http.ResponseWriter, r *http.Request) {
	owner, d, t, _, ok := a.ownSet(w, r)
	if !ok {
		return
	}
	if _, err := d.Exec(`DELETE FROM user_presets WHERE id = ? AND owner = ?`, t.ID, owner); err != nil {
		writeErr(w, 500, "could not remove the template")
		return
	}
	writeJSON(w, 200, map[string]string{"status": "removed"})
}

func (a *API) handleDuplicateMySet(w http.ResponseWriter, r *http.Request) {
	owner, d, t, sealed, ok := a.ownSet(w, r)
	if !ok {
		return
	}
	var n int
	_ = d.QueryRow(`SELECT COUNT(*) FROM user_presets WHERE owner = ?`, owner).Scan(&n)
	if n >= maxUserSets {
		writeErr(w, 409, "you have added the most templates allowed; remove one first")
		return
	}
	now := time.Now().UTC()
	name := t.Name
	if len([]rune(name)) > maxSetNameLen-7 {
		name = string([]rune(name)[:maxSetNameLen-7])
	}
	t.ID, t.Name, t.CreatedAt, t.UpdatedAt = newSetID(), name+" (copy)", now, now
	if _, err := d.Exec(`INSERT INTO user_presets (id, owner, preset_id, name, description, data, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?)`,
		t.ID, owner, t.PresetID, t.Name, t.Description, sealed, now.UnixMilli(), now.UnixMilli()); err != nil {
		writeErr(w, 500, "could not duplicate the template")
		return
	}
	writeJSON(w, 201, t)
}

// exportSets is the people's-data download: their templates and the settings they filled in.
func (a *API) exportSets(owner string) []map[string]any {
	out := []map[string]any{}
	d := a.mgr.DB()
	if d == nil || a.auth == nil {
		return out
	}
	rows, err := d.Query(`SELECT `+setCols+`, data FROM user_presets WHERE owner = ? ORDER BY updated_at DESC`, owner)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var sealed string
		t, err := scanSet(rows, &sealed)
		if err != nil {
			continue
		}
		item := map[string]any{"id": t.ID, "template": t.PresetID, "name": t.Name, "description": t.Description,
			"createdAt": t.CreatedAt, "updatedAt": t.UpdatedAt}
		if vals, err := a.openValues(sealed); err == nil {
			item["settings"] = vals
		}
		out = append(out, item)
	}
	return out
}
