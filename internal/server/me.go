package server

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/awhadi/blasta-perftest/internal/auth"
)

// handleExportMe sends the signed-in person everything held about them, as a file: their account,
// their sign-in sessions and their test history.
func (a *API) handleExportMe(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if a.auth == nil || u == nil {
		writeErr(w, 401, "authentication required")
		return
	}
	runs := []RunView{}
	for _, v := range a.mgr.Runs() {
		if v.Owner == u.ID {
			runs = append(runs, v)
		}
	}
	w.Header().Set("Content-Disposition", `attachment; filename="blasta-my-data.json"`)
	writeJSON(w, 200, map[string]any{
		"exportedAt": time.Now().UTC(),
		"account":    a.auth.ExportAccount(u.ID),
		"runs":       runs,
	})
}

// handleDeleteMe deletes the signed-in person's account and test history, once they prove it is them.
func (a *API) handleDeleteMe(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if a.auth == nil || u == nil {
		writeErr(w, 401, "authentication required")
		return
	}
	var in struct{ Password, Confirm string }
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, 400, "invalid request")
		return
	}
	if err := a.auth.DeleteOwnAccount(u, in.Password, in.Confirm); err != nil {
		var th *auth.ThrottledError
		switch {
		case errors.As(err, &th):
			w.Header().Set("Retry-After", strconv.Itoa(int(th.RetryAfter.Seconds())+1))
			writeErr(w, 429, err.Error())
		case errors.Is(err, auth.ErrSelfDeleteOff):
			writeErr(w, 403, err.Error())
		case errors.Is(err, auth.ErrLastAdmin):
			writeErr(w, 409, err.Error())
		default:
			writeErr(w, 403, err.Error())
		}
		return
	}
	// The history is gone from the database with the account; drop the copy held in memory too.
	_, _ = a.mgr.ClearRuns(u.ID, false)
	a.auth.ForgetBrowser(w, r)
	writeJSON(w, 200, map[string]string{"status": "your account and test history were deleted"})
}
