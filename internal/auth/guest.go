package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/awhadi/blasta-perftest/internal/db"
	"github.com/awhadi/blasta-perftest/internal/settings"
)

// GuestCookie identifies a visitor who has not registered.
const GuestCookie = "blasta_guest"

// Why a guest was refused. The messages are shown to the visitor.
var (
	ErrGuestExpired = errors.New("your free trial has ended: sign in or create an account to keep testing")
	ErrGuestRuns    = errors.New("you have used all the test runs of the free trial: sign in or create an account to keep testing")
	ErrGuestBusy    = errors.New("too many trial tests have been started from your network today: sign in or create an account to continue")
)

// Guest is an unregistered visitor trying BLASTA for a short while.
type Guest struct {
	ID        string
	Owner     string // the owner id their runs carry: "guest:" + ID
	Started   time.Time
	RunsUsed  int
	Expired   bool
	Verified  bool // has passed the bot check
	Limits    settings.Guest
	TrialEnds time.Time
}

type guestKey struct{}

// GuestFrom returns the guest a request belongs to, if it is one.
func GuestFrom(ctx context.Context) *Guest {
	g, _ := ctx.Value(guestKey{}).(*Guest)
	return g
}

// IsGuestOwner reports whether a run owner id belongs to a guest.
func IsGuestOwner(owner string) bool { return strings.HasPrefix(owner, "guest:") }

// guestPath lists what a guest may touch: their own jobs and runs, and nothing
// else. Templates, other people's history, settings and users are not here.
func guestPath(p string) bool {
	return p == "/api/executors" || p == "/api/jobs" || strings.HasPrefix(p, "/api/jobs/") ||
		p == "/api/runs" || strings.HasPrefix(p, "/api/runs/")
}

const day = 24 * time.Hour

// guestFor finds the visitor behind a request. With create it starts a trial for
// a visitor who has none; otherwise a visitor without a trial is nil.
func (s *Service) guestFor(w http.ResponseWriter, r *http.Request, create bool) *Guest {
	cfg := s.conf()
	if !cfg.Guest.Enabled {
		return nil
	}
	now := s.now()
	var token, id string
	if c, err := r.Cookie(GuestCookie); err == nil && len(c.Value) >= 20 {
		token = c.Value
		id = tokenHash(token)
	}
	var first int64
	var runs, verified int
	found := false
	if id != "" {
		found = s.store.db.QueryRow(`SELECT first_seen, runs, verified FROM guests WHERE id = ?`, id).Scan(&first, &runs, &verified) == nil
	}
	if !found {
		if !create || w == nil {
			return nil
		}
		var err error
		if token, err = randomToken(24); err != nil {
			return nil
		}
		id, first, runs, verified = tokenHash(token), now.UnixMilli(), 0, 0
		if _, err := s.store.db.Exec(`INSERT INTO guests (id, first_seen, runs) VALUES (?,?,0)`, id, first); err != nil {
			return nil
		}
		http.SetCookie(w, &http.Cookie{Name: GuestCookie, Value: token, Path: "/", HttpOnly: true,
			SameSite: http.SameSiteLaxMode, Secure: s.secure(r), MaxAge: int(2 * day / time.Second)})
	} else if now.Sub(time.UnixMilli(first)) >= day {
		// A day later the visitor gets a fresh trial.
		first, runs, verified = now.UnixMilli(), 0, 0
		_, _ = s.store.db.Exec(`UPDATE guests SET first_seen = ?, runs = 0, verified = 0 WHERE id = ?`, first, id)
	}
	start := time.UnixMilli(first)
	ends := start.Add(time.Duration(cfg.Guest.TrialMinutes) * time.Minute)
	return &Guest{ID: id, Owner: "guest:" + id, Started: start, RunsUsed: runs, Limits: cfg.Guest,
		TrialEnds: ends, Expired: now.After(ends), Verified: verified == 1}
}

// GuestNeedsCheck reports whether a visitor must still pass the bot check.
func (s *Service) GuestNeedsCheck(g *Guest) bool {
	return !g.Verified && s.CaptchaRequired(ScopeGuest)
}

// GuestStartRun checks a guest may start another run and counts it. The
// per-address counter is the backstop for someone who keeps clearing cookies.
func (s *Service) GuestStartRun(g *Guest, ip string) error {
	if g.Expired {
		return ErrGuestExpired
	}
	if g.RunsUsed >= g.Limits.MaxRuns {
		return ErrGuestRuns
	}
	today := s.now().Unix() / int64(day/time.Second)
	return s.store.db.Tx(func(t *db.Tx) error {
		var n int
		if err := t.QueryRow(`SELECT runs FROM guest_ips WHERE ip = ? AND day = ?`, ip, today).Scan(&n); err != nil {
			n = 0
			if _, err := t.Exec(`INSERT INTO guest_ips (ip, day, runs) VALUES (?,?,0)`, ip, today); err != nil {
				return err
			}
		}
		if n >= g.Limits.DailyPerIP {
			return ErrGuestBusy
		}
		if _, err := t.Exec(`UPDATE guest_ips SET runs = runs + 1 WHERE ip = ? AND day = ?`, ip, today); err != nil {
			return err
		}
		_, err := t.Exec(`UPDATE guests SET runs = runs + 1 WHERE id = ?`, g.ID)
		return err
	})
}

// ClientIP is the address a request came from, as the sign-in throttle sees it.
func (s *Service) ClientIP(r *http.Request) string { return s.clientIP(r) }

// pruneGuests forgets old trials.
func (s *Service) pruneGuests(now time.Time) {
	_, _ = s.store.db.Exec(`DELETE FROM guests WHERE first_seen < ?`, now.Add(-3*day).UnixMilli())
	_, _ = s.store.db.Exec(`DELETE FROM guest_ips WHERE day < ?`, now.Unix()/int64(day/time.Second)-3)
}

type guestStatus struct {
	NeedsCheck bool           `json:"needsCheck"` // the bot check is still to be passed
	Enabled    bool           `json:"enabled"`
	Active     bool           `json:"active"`
	Expired    bool           `json:"expired"`
	EndsAt     *time.Time     `json:"endsAt,omitempty"`
	RunsUsed   int            `json:"runsUsed"`
	RunsMax    int            `json:"runsMax"`
	Limits     settings.Guest `json:"limits"`
	ServerNow  time.Time      `json:"now"`
}

// guestStatus describes a visitor's trial for the page.
func (s *Service) guestStatus(r *http.Request) guestStatus {
	cfg := s.conf()
	st := guestStatus{Enabled: cfg.Guest.Enabled, Limits: cfg.Guest, RunsMax: cfg.Guest.MaxRuns, ServerNow: s.now()}
	if !cfg.Guest.Enabled {
		return st
	}
	st.NeedsCheck = s.CaptchaRequired(ScopeGuest)
	if g := s.guestFor(nil, r, false); g != nil {
		st.NeedsCheck = s.GuestNeedsCheck(g)
		st.Active, st.Expired, st.RunsUsed = true, g.Expired, g.RunsUsed
		e := g.TrialEnds
		st.EndsAt = &e
	}
	return st
}
