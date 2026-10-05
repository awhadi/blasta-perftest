package metrics

import (
	"testing"
	"time"
)

func TestPercentilesApproximateKnownDistribution(t *testing.T) {
	h := NewLatencyHistogram()
	// 1..100ms uniform; p50 should land near 50ms.
	for i := 1; i <= 100; i++ {
		h.Record(time.Duration(i) * time.Millisecond)
	}
	r := h.Report(DefaultQuantiles)

	if r.Count != 100 {
		t.Fatalf("count = %d, want 100", r.Count)
	}
	p50 := r.Latency["p50"]
	if p50 < 45_000 || p50 > 55_000 { // microseconds
		t.Fatalf("p50 = %dus, want ~50000us", p50)
	}
	p99 := r.Latency["p99"]
	if p99 < 97_000 || p99 > 100_000 {
		t.Fatalf("p99 = %dus, want ~99000-100000us", p99)
	}
}

func TestMinMaxMean(t *testing.T) {
	h := NewLatencyHistogram()
	h.Record(10 * time.Millisecond)
	h.Record(20 * time.Millisecond)
	h.Record(30 * time.Millisecond)
	r := h.Report(nil)
	if r.Min < 9*time.Millisecond || r.Min > 11*time.Millisecond {
		t.Errorf("min = %v, want ~10ms", r.Min)
	}
	// HDR quantizes at 3 significant figures, so allow a small delta.
	if r.Max < 29*time.Millisecond || r.Max > 31*time.Millisecond {
		t.Errorf("max = %v, want ~30ms", r.Max)
	}
	if r.Mean < 19*time.Millisecond || r.Mean > 21*time.Millisecond {
		t.Errorf("mean = %v, want ~20ms", r.Mean)
	}
}

func TestSubMicrosecondIsClampedNotDropped(t *testing.T) {
	h := NewLatencyHistogram()
	h.Record(200 * time.Nanosecond)
	r := h.Report(nil)
	if r.Count != 1 {
		t.Fatalf("count = %d, want 1 (sub-microsecond must still record)", r.Count)
	}
}

func TestMergeCombinesDistributions(t *testing.T) {
	a := NewLatencyHistogram()
	b := NewLatencyHistogram()
	for i := 0; i < 50; i++ {
		a.Record(time.Duration(i+1) * time.Millisecond)
	}
	for i := 50; i < 100; i++ {
		b.Record(time.Duration(i+1) * time.Millisecond)
	}
	a.Merge(b)
	r := a.Report(nil)
	if r.Count != 100 {
		t.Fatalf("after merge count = %d, want 100", r.Count)
	}
}

func TestPercentileKeyMapping(t *testing.T) {
	cases := map[float64]string{
		50: "p50", 95: "p95", 99: "p99", 99.9: "p99_9", 99.99: "p99_99",
	}
	for q, want := range cases {
		if got := PercentileKey(q); got != want {
			t.Errorf("PercentileKey(%v) = %q, want %q", q, got, want)
		}
	}
}
