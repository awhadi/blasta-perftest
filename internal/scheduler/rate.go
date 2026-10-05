package scheduler

import (
	"context"
	"math"
	"sync"
	"time"
)

// RateLimiter is a token bucket with optional ramping. It is the primary
// mechanism that prevents "blasting the host": the arrival rate is capped
// independently of how fast workers can actually run.
type RateLimiter struct {
	mu      sync.Mutex
	rate    float64 // tokens per second
	burst   float64
	tokens  float64
	last    time.Time
	waiters int

	// ramping
	rampDuration time.Duration
	startRate    float64
	targetRate   float64
	startTime    time.Time
}

// NewRateLimiter creates a limiter. A rate of 0 means unlimited (closed-loop).
func NewRateLimiter(rps float64, burst int, rampDuration time.Duration, targetRPS float64) *RateLimiter {
	if burst < 1 {
		burst = 1
	}
	rl := &RateLimiter{
		rate:   rps,
		burst:  float64(burst),
		tokens: float64(burst),
		last:   time.Now(),
	}
	if rampDuration > 0 && targetRPS > 0 {
		rl.rampDuration = rampDuration
		rl.startRate = rps
		rl.targetRate = targetRPS
		rl.startTime = time.Now()
	}
	return rl
}

// currentRateLocked applies ramp progress.
func (rl *RateLimiter) currentRateLocked() float64 {
	if rl.rampDuration <= 0 {
		return rl.rate
	}
	elapsed := time.Since(rl.startTime).Seconds()
	total := rl.rampDuration.Seconds()
	if total <= 0 {
		return rl.targetRate
	}
	frac := elapsed / total
	if frac >= 1 {
		return rl.targetRate
	}
	if frac < 0 {
		frac = 0
	}
	return rl.startRate + (rl.targetRate-rl.startRate)*frac
}

// Wait blocks until a token is available or ctx is done.
// Returns false if the context ended before a token was granted.
func (rl *RateLimiter) Wait(ctx context.Context) bool {
	rl.mu.Lock()
	if rl.rate <= 0 && rl.rampDuration <= 0 {
		rl.mu.Unlock()
		return true // unlimited
	}
	rl.waiters++
	rl.mu.Unlock()
	defer func() {
		rl.mu.Lock()
		rl.waiters--
		rl.mu.Unlock()
	}()

	for {
		rl.mu.Lock()
		now := time.Now()
		rl.rate = rl.currentRateLocked()
		elapsed := now.Sub(rl.last).Seconds()
		rl.last = now
		rl.tokens += elapsed * rl.rate
		if rl.tokens > rl.burst {
			rl.tokens = rl.burst
		}
		if rl.tokens >= 1 {
			rl.tokens--
			rl.mu.Unlock()
			return true
		}
		missing := 1 - rl.tokens
		delay := time.Duration(missing / math.Max(rl.rate, 0.001) * float64(time.Second))
		if delay < time.Millisecond {
			delay = time.Millisecond
		}
		rl.mu.Unlock()

		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return false
		case <-t.C:
		}
	}
}

// Rate reports the current (possibly ramping) rate.
func (rl *RateLimiter) Rate() float64 {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return rl.currentRateLocked()
}
