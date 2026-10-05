package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/awhadi/blasta-perftest/internal/collector"
	"github.com/awhadi/blasta-perftest/internal/config"
)

// exitThreshold is the process exit status when a run completes but misses a
// pass/fail threshold. It differs from 1 (could not run at all) so CI and
// Kubernetes Jobs can tell "the target is too slow" from "the job is broken".
const exitThreshold = 2

// thresholdError reports a finished run that breached --max-error-rate or
// --max-p95.
type thresholdError struct{ failures []string }

func (e *thresholdError) Error() string {
	return "thresholds not met: " + strings.Join(e.failures, "; ")
}

// checkThresholds returns one message per breached limit. A negative
// maxErrorRate and a zero maxP95 mean "not set". A run that sent no requests
// fails any set threshold, since silence is not success.
func checkThresholds(s collector.Summary, maxErrorRate float64, maxP95, maxP99 time.Duration) []string {
	var out []string
	if maxErrorRate >= 0 {
		if s.Total == 0 {
			out = append(out, "no requests completed")
		} else if rate := float64(s.Errors) / float64(s.Total) * 100; rate > maxErrorRate {
			out = append(out, fmt.Sprintf("error rate %.2f%% > %.2f%%", rate, maxErrorRate))
		}
	}
	for _, c := range []struct {
		key   string
		limit time.Duration
	}{{"p95", maxP95}, {"p99", maxP99}} {
		if c.limit <= 0 {
			continue
		}
		v, ok := s.Latency.Percentiles[c.key]
		if !ok || s.Total == 0 {
			out = append(out, "no latency data for "+c.key)
		} else if got := time.Duration(v * float64(time.Microsecond)); got > c.limit {
			out = append(out, fmt.Sprintf("%s %s > %s", c.key, got.Round(time.Microsecond), c.limit))
		}
	}
	return out
}

// effectiveThresholds merges command-line flags with the job's own SLO. A flag
// that was set wins; otherwise the job's target applies.
func effectiveThresholds(slo *config.SLO, flagErr float64, flagP95, flagP99 time.Duration) (float64, time.Duration, time.Duration) {
	if slo == nil {
		return flagErr, flagP95, flagP99
	}
	if flagErr < 0 && slo.MaxErrorRate != nil {
		flagErr = *slo.MaxErrorRate
	}
	if flagP95 == 0 {
		flagP95 = slo.MaxP95
	}
	if flagP99 == 0 {
		flagP99 = slo.MaxP99
	}
	return flagErr, flagP95, flagP99
}

// scaleTime shortens (or lengthens) a job's duration and ramp by f, so a plan
// that takes hours can be dry-run in minutes. The rate is unchanged, so the
// shape of the run stays the same and only its length changes. A run never
// drops below one second.
func scaleTime(j *config.Job, f float64) error {
	if f == 1 {
		return nil
	}
	if f <= 0 {
		return fmt.Errorf("--time-scale must be positive, got %v", f)
	}
	scale := func(d time.Duration) time.Duration {
		if d <= 0 {
			return d
		}
		n := time.Duration(float64(d) * f)
		if n < time.Second {
			n = time.Second
		}
		return n
	}
	j.Duration = scale(j.Duration)
	if j.Ramp > 0 {
		j.Ramp = scale(j.Ramp)
		if j.Ramp >= j.Duration {
			j.Ramp = j.Duration / 2
		}
	}
	return nil
}
