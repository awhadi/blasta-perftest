package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/awhadi/blasta-perftest/internal/collector"
	"github.com/awhadi/blasta-perftest/internal/compare"
)

func summary(total, errs int64, rps, p95 float64) collector.Summary {
	var s collector.Summary
	s.Total, s.Errors, s.AvgRPS = total, errs, rps
	s.Latency.Percentiles = map[string]float64{"p50": 10000, "p95": p95 * 1000, "p99": p95 * 2000}
	return s
}

func TestBaselineFromAnEarlierRunGatesAReleaseInCI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.json")
	if err := saveReport(path, summary(1000, 1, 100, 100)); err != nil {
		t.Fatal(err)
	}
	// Similar run: within 15%.
	fails, err := checkBaseline(path, summary(980, 1, 98, 108), compare.Limits{LatencyPct: 15, ErrorPoints: 1, ThroughputPct: 10})
	if err != nil || len(fails) != 0 {
		t.Fatalf("a similar run passes: %v %v", err, fails)
	}
	// Slower and failing more: caught, with reasons.
	fails, err = checkBaseline(path, summary(700, 70, 70, 160), compare.Limits{LatencyPct: 15, ErrorPoints: 1, ThroughputPct: 10})
	if err != nil || len(fails) != 4 {
		t.Fatalf("a regression is caught: %v %v", err, fails)
	}
	if !strings.Contains(strings.Join(fails, ";"), "slower") {
		t.Errorf("reasons: %v", fails)
	}
	// With no limits it only compares.
	if fails, _ := checkBaseline(path, summary(700, 70, 70, 160), compare.Off()); len(fails) != 0 {
		t.Errorf("no limits, no failures: %v", fails)
	}
}

func TestBaselineFilesAndFlagsAreChecked(t *testing.T) {
	dir := t.TempDir()
	if _, err := loadBaseline(filepath.Join(dir, "missing.json")); err == nil {
		t.Error("a missing baseline must be an error")
	}
	empty := filepath.Join(dir, "empty.json")
	if err := saveReport(empty, collector.Summary{}); err != nil {
		t.Fatal(err)
	}
	if _, err := loadBaseline(empty); err == nil || !strings.Contains(err.Error(), "--save-report") {
		t.Errorf("a file with no latency numbers must say how to make one: %v", err)
	}
	if err := baselineFlagsOK("", compare.Limits{LatencyPct: 10, ErrorPoints: -1, ThroughputPct: -1}); err == nil {
		t.Error("a limit with no baseline must be refused")
	}
	if err := baselineFlagsOK("b.json", compare.Limits{LatencyPct: 10}); err != nil {
		t.Errorf("a limit with a baseline: %v", err)
	}
}
