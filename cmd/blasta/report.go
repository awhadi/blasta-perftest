package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/awhadi/blasta-perftest/internal/collector"
)

func printReport(s collector.Summary) {
	w := os.Stdout
	line := strings.Repeat("-", 62)

	fmt.Fprintln(w, line)
	fmt.Fprintf(w, "  run          %s\n", s.RunID)
	fmt.Fprintf(w, "  target       %s\n", s.Target)
	fmt.Fprintf(w, "  duration     %.1fs\n", float64(s.DurationMs)/1000)
	fmt.Fprintln(w, line)

	okRate := 0.0
	if s.Total > 0 {
		okRate = float64(s.Success) / float64(s.Total) * 100
	}
	fmt.Fprintf(w, "  requests     %d  (%.2f/s avg)\n", s.Total, s.AvgRPS)
	fmt.Fprintf(w, "  success      %d  (%.2f%%)\n", s.Success, okRate)
	fmt.Fprintf(w, "  errors       %d\n", s.Errors)
	if s.Skipped > 0 {
		fmt.Fprintf(w, "  skipped      %d   <- backpressure (queue full)\n", s.Skipped)
	}
	if s.RowsTotal > 0 {
		// Row-oriented executors (sql) have no meaningful byte count.
		fmt.Fprintf(w, "  rows         %d  (%.0f/s)\n", s.RowsTotal, float64(s.RowsTotal)/elapsedSecs(s))
	} else {
		fmt.Fprintf(w, "  transferred  %s\n", humanBytes(s.BytesTotal))
		fmt.Fprintf(w, "  throughput   %s/s\n", humanBytes(int64(s.Throughput)))
	}
	fmt.Fprintln(w, line)

	fmt.Fprintln(w, "  latency")
	order := []string{"p50", "p75", "p90", "p95", "p99", "p99_9", "p99_99", "p99_999"}
	fmt.Fprint(w, "    min ")
	fmt.Fprintf(w, "%-10s", durUs(s.Latency.Min.Microseconds()))
	fmt.Fprintf(w, "max %s\n", durUs(s.Latency.Max.Microseconds()))
	for _, k := range order {
		if v, ok := s.Latency.Latency[k]; ok {
			fmt.Fprintf(w, "    %-8s %s\n", k, durUs(v))
		}
	}
	if len(s.WaitTime.Latency) > 0 {
		p := s.WaitTime.Latency["p99"]
		fmt.Fprintf(w, "\n  queue wait p99 %s  <- scheduler lag (open-loop accuracy)\n", durUs(p))
	}
	fmt.Fprintln(w, line)

	if len(s.StatusCodes) > 0 {
		fmt.Fprintln(w, "  status codes")
		for _, k := range collector.SortCounts(s.StatusCodes) {
			fmt.Fprintf(w, "    %-6s %d\n", k, s.StatusCodes[k])
		}
	}
	if len(s.ErrorKinds) > 0 {
		fmt.Fprintln(w, "  errors")
		for _, k := range collector.SortCounts(s.ErrorKinds) {
			fmt.Fprintf(w, "    %-20s %d\n", k, s.ErrorKinds[k])
		}
	}
	fmt.Fprintln(w, line)
}

func durUs(us int64) string {
	if us <= 0 {
		return "-"
	}
	if us < 1000 {
		return fmt.Sprintf("%dus", us)
	}
	if us < 1_000_000 {
		return fmt.Sprintf("%.2fms", float64(us)/1000)
	}
	return fmt.Sprintf("%.3fs", float64(us)/1_000_000)
}

// elapsedSecs is the run duration in seconds, guarding against a zero-length run.
func elapsedSecs(s collector.Summary) float64 {
	if s.DurationMs <= 0 {
		return 0
	}
	return float64(s.DurationMs) / 1000
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f%cB", float64(n)/float64(div), "KMGTPE"[exp])
}
