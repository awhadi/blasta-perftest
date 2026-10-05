package auth

import (
	"sync"
	"time"
)

// Throttle slows password guessing. Each key (an email address, or a client
// address) may fail maxFails times inside the window; after that it is locked
// out until the window passes. A success clears the key.
type Throttle struct {
	mu       sync.Mutex
	maxFails int
	window   time.Duration
	fails    map[string][]time.Time
}

// NewThrottle returns a Throttle allowing maxFails failures per window.
func NewThrottle(maxFails int, window time.Duration) *Throttle {
	return &Throttle{maxFails: maxFails, window: window, fails: map[string][]time.Time{}}
}

func (t *Throttle) prune(key string, now time.Time) []time.Time {
	recent := t.fails[key][:0]
	for _, at := range t.fails[key] {
		if now.Sub(at) < t.window {
			recent = append(recent, at)
		}
	}
	if len(recent) == 0 {
		delete(t.fails, key)
	} else {
		t.fails[key] = recent
	}
	return recent
}

// Locked reports whether key is locked out, and for how much longer.
func (t *Throttle) Locked(key string, now time.Time) (bool, time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	recent := t.prune(key, now)
	if len(recent) < t.maxFails {
		return false, 0
	}
	return true, t.window - now.Sub(recent[0])
}

// Fail records a failure.
func (t *Throttle) Fail(key string, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.prune(key, now)
	t.fails[key] = append(t.fails[key], now)
}

// Reset clears a key after a successful sign-in.
func (t *Throttle) Reset(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.fails, key)
}
