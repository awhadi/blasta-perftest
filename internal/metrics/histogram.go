package metrics

import (
	"fmt"
	"sync"
	"time"

	hdr "github.com/HdrHistogram/hdrhistogram-go"
)

// maxTrackableUs bounds the histogram range: 1us .. 1 hour.
const (
	minTrackableUs = 1
	maxTrackableUs = int64(time.Hour / time.Microsecond)
	sigfigs        = 3
)

// LatencyHistogram wraps an HDR histogram with a mutex so worker goroutines
// can record concurrently. Memory is fixed at construction (~185KB), so
// recording never allocates and never grows the heap.
type LatencyHistogram struct {
	mu sync.Mutex
	h  *hdr.Histogram
}

func NewLatencyHistogram() *LatencyHistogram {
	return &LatencyHistogram{h: hdr.New(minTrackableUs, maxTrackableUs, sigfigs)}
}

func (l *LatencyHistogram) Record(d time.Duration) {
	us := d.Microseconds()
	if us < minTrackableUs {
		us = minTrackableUs
	}
	if us > maxTrackableUs {
		us = maxTrackableUs
	}
	l.mu.Lock()
	_ = l.h.RecordValue(us)
	l.mu.Unlock()
}

// Merge folds another histogram into this one. Both must share the same
// trackable range and precision (which ours do by construction).
func (l *LatencyHistogram) Merge(o *LatencyHistogram) {
	if o == nil {
		return
	}
	o.mu.Lock()
	other := o.h
	o.mu.Unlock()

	l.mu.Lock()
	l.h.Merge(other)
	l.mu.Unlock()
}

// Report is the JSON-friendly snapshot of a histogram.
type Report struct {
	Count       int64              `json:"count"`
	Min         time.Duration      `json:"min"`
	Max         time.Duration      `json:"max"`
	Mean        time.Duration      `json:"mean"`
	StdDev      time.Duration      `json:"stddev"`
	Percentiles map[string]float64 `json:"percentiles"` // microseconds
	Latency     map[string]int64   `json:"latencyMicros"`
}

// DefaultQuantiles are the percentiles reported for every run.
var DefaultQuantiles = []float64{50, 75, 90, 95, 99, 99.9, 99.99, 99.999}

// Report snapshots the histogram. Live series use Report; final reports
// additionally include the raw latency map.
func (l *LatencyHistogram) Report(quantiles []float64) Report {
	if len(quantiles) == 0 {
		quantiles = DefaultQuantiles
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	pct := make(map[string]float64, len(quantiles))
	raw := make(map[string]int64, len(quantiles))
	for _, q := range quantiles {
		key := PercentileKey(q)
		v := l.h.ValueAtQuantile(q)
		pct[key] = float64(v)
		raw[key] = v
	}
	return Report{
		Count:       l.h.TotalCount(),
		Min:         time.Duration(l.h.Min()) * time.Microsecond,
		Max:         time.Duration(l.h.Max()) * time.Microsecond,
		Mean:        time.Duration(l.h.Mean()) * time.Microsecond,
		StdDev:      time.Duration(l.h.StdDev()) * time.Microsecond,
		Percentiles: pct,
		Latency:     raw,
	}
}

// PercentileKey renders a quantile as a stable JSON key.
func PercentileKey(q float64) string {
	switch {
	case q >= 99.999:
		return "p99_999"
	case q >= 99.99:
		return "p99_99"
	case q >= 99.9:
		return "p99_9"
	case q >= 99:
		return "p99"
	case q >= 95:
		return "p95"
	case q >= 90:
		return "p90"
	case q >= 75:
		return "p75"
	case q >= 50:
		return "p50"
	}
	return fmt.Sprintf("p%g", q)
}
