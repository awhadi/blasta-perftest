package collector

import (
	"testing"
	"time"
)

// A long run is stored as a bounded, evenly spaced series that still ends on the
// final sample, so a chart drawn later matches what was seen live.
func TestSummarySeriesIsBounded(t *testing.T) {
	c := New()
	for i := 0; i < 1000; i++ {
		c.series = append(c.series, Snapshot{ElapsedMs: int64(i) * 100, RPS: float64(i)})
	}
	s := c.Summary("r", "j", "http", "http://x", false, "")
	if n := len(s.Series); n == 0 || n > maxSummarySeries+1 {
		t.Fatalf("series has %d points, want 1..%d", n, maxSummarySeries+1)
	}
	if last := s.Series[len(s.Series)-1]; last.RPS != 999 {
		t.Errorf("last point RPS = %v, want the final sample (999)", last.RPS)
	}
	for i := 1; i < len(s.Series); i++ {
		if s.Series[i].T <= s.Series[i-1].T {
			t.Fatalf("series is not in time order at %d", i)
		}
	}
}

func TestSummarySeriesEmptyRun(t *testing.T) {
	s := New().Summary("r", "j", "http", "http://x", false, "")
	if s.Series != nil {
		t.Errorf("a run with no snapshots must have no series, got %d points", len(s.Series))
	}
	_ = time.Now
}
