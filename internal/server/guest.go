package server

import (
	"fmt"
	"time"

	"github.com/awhadi/blasta-perftest/internal/auth"
	"github.com/awhadi/blasta-perftest/internal/config"
)

// maxGuestJobs bounds how many jobs a visitor can leave around.
const maxGuestJobs = 10

// guestJobError refuses a job a guest may not run, and tightens the rest. Guests
// get plain HTTP(S) tests against public addresses within the admin's limits:
// no databases, raw sockets or private networks, whatever the job asks for.
func guestJobError(j *config.Job, g *auth.Guest, mgr *Manager) error {
	lim := g.Limits
	if j.Executor != "http" {
		return fmt.Errorf("the free trial runs web (HTTP) tests only: sign in or create an account for %s tests", j.Executor)
	}
	if j.DB != nil {
		return fmt.Errorf("the free trial cannot run database tests: sign in or create an account")
	}
	if g.Expired {
		return auth.ErrGuestExpired
	}
	if n, _ := mgr.CountOwned(g.Owner); n >= maxGuestJobs {
		return fmt.Errorf("you have too many trial tests set up: run one of them, or sign in or create an account")
	}
	if j.RPS > lim.MaxRPS || j.RPS <= 0 {
		return fmt.Errorf("the free trial allows up to %d requests per second: lower it, or sign in or create an account for more", lim.MaxRPS)
	}
	if j.Concurrency > lim.MaxConcurrency {
		return fmt.Errorf("the free trial allows up to %d connections: lower it, or sign in or create an account for more", lim.MaxConcurrency)
	}
	if j.MaxWorkers > lim.MaxConcurrency*2 {
		j.MaxWorkers = lim.MaxConcurrency * 2
	}
	if j.Duration > time.Duration(lim.MaxDurationSec)*time.Second || j.Duration <= 0 {
		return fmt.Errorf("the free trial allows tests up to %d seconds long: shorten it, or sign in or create an account for longer", lim.MaxDurationSec)
	}
	if j.Timeout > 10*time.Second {
		j.Timeout = 10 * time.Second
	}
	if len(j.Body) > 16<<10 {
		return fmt.Errorf("the free trial allows a request body up to 16 KB")
	}
	j.BlockPrivate = true // never reach private networks
	j.Allowlist = nil
	return nil
}
