package compare

import (
	"strings"
	"testing"
	"time"

	"github.com/awhadi/blasta-perftest/internal/collector"
)

func run(total, errs int64, rps, p50, p95, p99 float64) collector.Summary {
	var s collector.Summary
	s.Total, s.Errors, s.AvgRPS, s.Target = total, errs, rps, "https://x.test"
	s.Latency.Percentiles = map[string]float64{"p50": p50 * 1000, "p95": p95 * 1000, "p99": p99 * 1000}
	s.Latency.Max = time.Duration(p99*2) * time.Millisecond
	return s
}

func TestComparisonShowsWhatChanged(t *testing.T) {
	base := run(1000, 5, 100, 20, 100, 200)
	now := run(900, 45, 80, 21, 130, 180)
	r := Runs(base, now, Off())
	by := map[string]Metric{}
	for _, m := range r.Metrics {
		by[m.Key] = m
	}
	if by["p95"].DeltaPct != 30 || by["p95"].Verdict != "worse" {
		t.Errorf("p95 rose 30%%: %+v", by["p95"])
	}
	if by["p50"].Verdict != "same" {
		t.Errorf("a 5%% move is noise: %+v", by["p50"])
	}
	if by["p99"].Verdict != "better" || by["avgRps"].Verdict != "worse" || by["errorRate"].Verdict != "worse" {
		t.Errorf("verdicts: p99=%s rps=%s err=%s", by["p99"].Verdict, by["avgRps"].Verdict, by["errorRate"].Verdict)
	}
	if r.Judged || !r.Passed || len(r.Regressions) != 0 {
		t.Errorf("with no limits nothing is judged: %+v", r)
	}
}

func TestLimitsGateTheComparison(t *testing.T) {
	base := run(1000, 5, 100, 20, 100, 200)
	now := run(900, 45, 80, 21, 130, 180)
	r := Runs(base, now, Limits{LatencyPct: 15, ErrorPoints: 1, ThroughputPct: 10})
	if r.Passed || !r.Judged || len(r.Regressions) != 3 {
		t.Fatalf("all three limits are broken: %+v", r.Regressions)
	}
	for _, want := range []string{"p95 is 30.0% slower", "error rate rose by 4.50 points", "throughput fell 20.0%"} {
		found := false
		for _, g := range r.Regressions {
			found = found || strings.Contains(g, want)
		}
		if !found {
			t.Errorf("missing %q in %v", want, r.Regressions)
		}
	}
	// Within the limits it passes; p99 improving is never a regression.
	if r := Runs(base, now, Limits{LatencyPct: 40, ErrorPoints: 5, ThroughputPct: 25}); !r.Passed || !r.Judged {
		t.Errorf("within the limits: %+v", r.Regressions)
	}
	// A limit that is off does not judge.
	if r := Runs(base, now, Limits{LatencyPct: -1, ErrorPoints: -1, ThroughputPct: 10}); r.Passed || len(r.Regressions) != 1 {
		t.Errorf("only the throughput limit applies: %+v", r.Regressions)
	}
}

func TestEdgeCases(t *testing.T) {
	zero := run(0, 0, 0, 0, 0, 0)
	r := Runs(zero, run(100, 0, 10, 5, 9, 12), Limits{LatencyPct: 10, ErrorPoints: 0, ThroughputPct: 10})
	if !r.Passed || len(r.Notes) == 0 || !strings.Contains(r.Notes[0], "no requests") {
		t.Errorf("an empty baseline cannot fail anything and says so: %+v %v", r.Regressions, r.Notes)
	}
	a, b := run(100, 0, 10, 5, 9, 12), run(100, 0, 10, 5, 9, 12)
	b.Target = "https://other.test"
	if r := Runs(a, b, Off()); len(r.Notes) == 0 || !strings.Contains(r.Notes[0], "different addresses") {
		t.Errorf("different targets should be flagged: %v", r.Notes)
	}
	// From no errors to some is worse even though a percentage is meaningless.
	if r := Runs(run(100, 0, 10, 5, 9, 12), run(100, 3, 10, 5, 9, 12), Off()); r.Metrics[1].Verdict != "worse" {
		t.Errorf("0 to 3%% errors: %+v", r.Metrics[1])
	}
}
