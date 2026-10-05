package server

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/awhadi/blasta-perftest/internal/db"
)

// maxHistory bounds how many finished runs are reloaded into memory at start-up,
// and perPersonHistory how many each person keeps in the database.
const (
	maxHistory       = 1000
	perPersonHistory = 200
)

// history persists finished runs in the database, so a restart or a pod
// reschedule does not lose results. It stores only the run summary (the numbers
// the History page and the report download need), never job bodies or headers,
// so credentials cannot end up in the database through it. Guest trial runs are
// never stored.
type history struct{ db *db.DB }

func (h *history) load() ([]RunView, error) {
	rows, err := h.db.Query(`SELECT data FROM runs ORDER BY started_at DESC LIMIT ?`, maxHistory)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RunView
	for rows.Next() {
		var raw string
		if rows.Scan(&raw) != nil {
			continue
		}
		var v RunView
		if json.Unmarshal([]byte(raw), &v) == nil && v.ID != "" {
			out = append(out, v)
		}
	}
	// Oldest first, which is what the manager expects.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}

// save stores one finished run and trims that person's oldest ones.
func (h *history) save(v RunView) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return h.db.Tx(func(t *db.Tx) error {
		if _, err := t.Exec(`DELETE FROM runs WHERE id = ?`, v.ID); err != nil {
			return err
		}
		if _, err := t.Exec(`INSERT INTO runs (id, owner, started_at, data) VALUES (?,?,?,?)`,
			v.ID, v.Owner, db.Ms(v.StartedAt), string(b)); err != nil {
			return err
		}
		_, err := t.Exec(`DELETE FROM runs WHERE owner = ? AND id NOT IN
			(SELECT id FROM (SELECT id FROM runs WHERE owner = ? ORDER BY started_at DESC LIMIT ?) AS keep)`,
			v.Owner, v.Owner, perPersonHistory)
		return err
	})
}

// importJSONL moves runs saved by earlier versions (runs.jsonl in dir) into the
// database, once. A run nobody owned goes to defaultOwner; a run whose owner is
// rejected by ownerOK (the account no longer exists) is dropped.
func (h *history) importJSONL(dir, defaultOwner string, ownerOK func(string) bool) (int, error) {
	path := filepath.Join(dir, "runs.jsonl")
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var runs []RunView
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		var v RunView
		if json.Unmarshal(sc.Bytes(), &v) != nil || v.ID == "" {
			continue // a line cut off by a crash
		}
		if v.Owner == "" {
			v.Owner = defaultOwner
		} else if ownerOK != nil && !ownerOK(v.Owner) {
			continue
		}
		runs = append(runs, v)
	}
	f.Close()
	for _, v := range runs {
		if strings.HasPrefix(v.Owner, "guest:") {
			continue
		}
		if err := h.save(v); err != nil {
			return 0, err
		}
	}
	// Keep the old file, renamed, as a backup rather than deleting someone's data.
	if err := os.Rename(path, path+".migrated"); err != nil {
		return len(runs), err
	}
	return len(runs), nil
}

// remove deletes saved runs: one by id, those of one owner, or all of them
// (owner "*"). It never touches a run that is still going: callers filter those.
func (h *history) remove(id, owner string) error {
	switch {
	case id != "":
		_, err := h.db.Exec(`DELETE FROM runs WHERE id = ?`, id)
		return err
	case owner == "*":
		_, err := h.db.Exec(`DELETE FROM runs`)
		return err
	default:
		_, err := h.db.Exec(`DELETE FROM runs WHERE owner = ?`, owner)
		return err
	}
}
