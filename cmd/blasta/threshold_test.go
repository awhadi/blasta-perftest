package main

import (
	"testing"
	"time"

	"github.com/awhadi/blasta-perftest/internal/collector"
	"github.com/awhadi/blasta-perftest/internal/config"
	"github.com/awhadi/blasta-perftest/internal/metrics"
)

func sum(total, errs int64, p95us float64) collector.Summary {
	return collector.Summary{Total: total, Errors: errs,
		Latency: metrics.Report{Percentiles: map[string]float64{"p95": p95us}}}
}

func TestCheckThresholds(t *testing.T) {
	cases := []struct {
		name string
		s    collector.Summary
		rate float64
		p95  time.Duration
		want int
	}{
		{"nothing set passes anything", sum(100, 100, 9e6), -1, 0, 0},
		{"error rate within limit", sum(100, 1, 1000), 1, 0, 0},
		{"error rate breached", sum(100, 5, 1000), 1, 0, 1},
		{"zero errors allowed means zero", sum(100, 1, 1000), 0, 0, 1},
		{"p95 within limit", sum(100, 0, 400_000), -1, 500 * time.Millisecond, 0},
		{"p95 breached", sum(100, 0, 600_000), -1, 500 * time.Millisecond, 1},
		{"both breached", sum(100, 50, 600_000), 1, 500 * time.Millisecond, 2},
		{"no requests fails a set threshold", sum(0, 0, 0), 1, 0, 1},
	}
	for _, c := range cases {
		if got := checkThresholds(c.s, c.rate, c.p95, 0); len(got) != c.want {
			t.Errorf("%s: got %v, want %d failure(s)", c.name, got, c.want)
		}
	}
}

func TestCheckThresholdsP99(t *testing.T) {
	s := collector.Summary{Total: 100, Latency: metrics.Report{Percentiles: map[string]float64{"p95": 100_000, "p99": 900_000}}}
	if got := checkThresholds(s, -1, 0, time.Second); len(got) != 0 {
		t.Errorf("p99 900ms within 1s, got %v", got)
	}
	if got := checkThresholds(s, -1, 0, 500*time.Millisecond); len(got) != 1 {
		t.Errorf("p99 900ms over 500ms, got %v", got)
	}
}

// A flag the user set must beat the job's SLO; an unset flag falls back to it.
func TestEffectiveThresholds(t *testing.T) {
	one, zero := 1.0, 0.0
	slo := &config.SLO{MaxErrorRate: &one, MaxP95: 500 * time.Millisecond, MaxP99: time.Second}
	e, p95, p99 := effectiveThresholds(slo, -1, 0, 0)
	if e != 1 || p95 != 500*time.Millisecond || p99 != time.Second {
		t.Errorf("unset flags should take the SLO, got %v %v %v", e, p95, p99)
	}
	e, p95, _ = effectiveThresholds(slo, 5, 2*time.Second, 0)
	if e != 5 || p95 != 2*time.Second {
		t.Errorf("flags must override the SLO, got %v %v", e, p95)
	}
	e, _, _ = effectiveThresholds(&config.SLO{MaxErrorRate: &zero}, -1, 0, 0)
	if e != 0 {
		t.Errorf("an SLO of 0%% errors must be kept (not treated as unset), got %v", e)
	}
	if e, p95, p99 := effectiveThresholds(nil, -1, 0, 0); e != -1 || p95 != 0 || p99 != 0 {
		t.Errorf("no SLO and no flags must stay off")
	}
}

func TestScaleTime(t *testing.T) {
	j := config.Job{Duration: 1000 * time.Second, Ramp: 600 * time.Second}
	if err := scaleTime(&j, 0.1); err != nil {
		t.Fatal(err)
	}
	if j.Duration != 100*time.Second || j.Ramp != 60*time.Second {
		t.Errorf("got %v / %v, want 100s / 60s", j.Duration, j.Ramp)
	}
	short := config.Job{Duration: 30 * time.Second, Ramp: 10 * time.Second}
	scaleTime(&short, 0.001)
	if short.Duration != time.Second || short.Ramp >= short.Duration {
		t.Errorf("must keep at least 1s and a ramp shorter than the run, got %v / %v", short.Duration, short.Ramp)
	}
	if err := scaleTime(&config.Job{}, -1); err == nil {
		t.Error("negative scale must be rejected")
	}
}
