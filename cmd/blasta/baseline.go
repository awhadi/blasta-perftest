package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/awhadi/blasta-perftest/internal/collector"
	"github.com/awhadi/blasta-perftest/internal/compare"
)

// saveReport writes a run's summary as JSON, to use later as a baseline.
func saveReport(path string, s collector.Summary) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

// loadBaseline reads a summary saved by --save-report (or printed by --json).
func loadBaseline(path string) (collector.Summary, error) {
	var s collector.Summary
	b, err := os.ReadFile(path)
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("%s is not a saved run summary: %w", path, err)
	}
	if s.Latency.Percentiles == nil {
		return s, fmt.Errorf("%s holds no latency numbers: save a baseline with --save-report", path)
	}
	return s, nil
}

func printComparison(res compare.Result) {
	w := os.Stdout
	fmt.Fprintln(w, "  compared with the baseline")
	for _, m := range res.Metrics {
		unit := map[string]string{"ms": " ms", "rps": "/s", "percent": "%", "count": ""}[m.Unit]
		sign := ""
		if m.Delta > 0 {
			sign = "+"
		}
		pct := ""
		if m.Base != 0 {
			pct = fmt.Sprintf(" (%s%.1f%%)", sign, m.DeltaPct)
		}
		fmt.Fprintf(w, "    %-22s %10.2f%s -> %10.2f%s%s  %s\n", m.Name, m.Base, unit, m.Now, unit, pct, m.Verdict)
	}
	for _, n := range res.Notes {
		fmt.Fprintf(w, "    note: %s\n", n)
	}
}

// checkBaseline compares a finished run with a saved one and returns what broke the limits.
func checkBaseline(path string, now collector.Summary, lim compare.Limits) ([]string, error) {
	base, err := loadBaseline(path)
	if err != nil {
		return nil, err
	}
	res := compare.Runs(base, now, lim)
	printComparison(res)
	return res.Regressions, nil
}

func baselineFlagsOK(baseline string, lim compare.Limits) error {
	if baseline == "" && (lim.LatencyPct >= 0 || lim.ErrorPoints >= 0 || lim.ThroughputPct >= 0) {
		return errors.New("--max-latency-regression, --max-error-increase and --max-throughput-drop need --baseline <file>")
	}
	return nil
}
