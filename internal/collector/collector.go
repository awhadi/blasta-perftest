package collector

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/awhadi/blasta-perftest/internal/metrics"
	"github.com/awhadi/blasta-perftest/internal/model"
	"github.com/awhadi/blasta-perftest/internal/sysstat"
)

const (
	// maxSeries bounds retained live snapshots (each ~ a few hundred bytes).
	maxSeries = 4096
	// defaultQuantiles reported per snapshot.
	defaultRPSWindow = time.Second
)

// Snapshot is the live view streamed to the UI and used for the final report.
type Snapshot struct {
	Timestamp     time.Time        `json:"timestamp"`
	ElapsedMs     int64            `json:"elapsedMs"`
	Total         int64            `json:"total"`
	Success       int64            `json:"success"`
	Errors        int64            `json:"errors"`
	Skipped       int64            `json:"skipped"`
	RPS           float64          `json:"rps"`
	AvgRPS        float64          `json:"avgRps"`
	BytesTotal    int64            `json:"bytesTotal"`
	RowsTotal     int64            `json:"rowsTotal"`
	ThroughputBps float64          `json:"throughputBps"`
	StatusCodes   map[string]int64 `json:"statusCodes"`
	ErrorKinds    map[string]int64 `json:"errorKinds"`
	Latency       metrics.Report   `json:"latency"`
	WaitTime      metrics.Report   `json:"waitTime"`
	ProgressPct   float64          `json:"progressPct"`
	TargetRPS     float64          `json:"targetRps"`
	AchievedPct   float64          `json:"achievedPct"`
	Running       bool             `json:"running"`
	// Server is the load generator's own CPU and memory right now, attached by
	// the manager while a run is live.
	Server *sysstat.Stats `json:"server,omitempty"`
}

// SeriesPoint is one thinned sample of a run, saved with its summary so a
// finished run can be charted later, including after a restart.
type SeriesPoint struct {
	T   float64 `json:"t"` // seconds since the run started
	RPS float64 `json:"rps"`
	P50 float64 `json:"p50"` // latency in microseconds
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
}

// maxSummarySeries bounds how many points a stored run keeps.
const maxSummarySeries = 120

// Collector aggregates results with bounded memory: two HDR histograms
// (fixed ~185KB each) plus counters. It never retains per-request records,
// which is what keeps a 100k RPS run inside a few MB of heap.
type Collector struct {
	mu sync.Mutex

	start    time.Time
	end      time.Time
	total    int64
	success  int64
	errors   int64
	skipped  int64
	bytes    int64
	rows     int64
	latency  *metrics.LatencyHistogram
	wait     *metrics.LatencyHistogram
	statuses map[string]int64
	kinds    map[string]int64

	// rolling window state for instantaneous RPS
	winStart  time.Time
	winCount  int64
	winBytes  int64
	winErrs   int64
	winPeriod time.Duration

	planned   int64
	targetRPS float64
	series    []Snapshot
	running   bool
}

// New returns a collector using a 1s rolling window.
func New() *Collector {
	return &Collector{
		latency:   metrics.NewLatencyHistogram(),
		wait:      metrics.NewLatencyHistogram(),
		statuses:  make(map[string]int64),
		kinds:     make(map[string]int64),
		winPeriod: defaultRPSWindow,
	}
}

// Begin starts a fresh run, resetting all state.
func (c *Collector) Begin(targetRPS float64, planned int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.start = time.Now()
	c.end = time.Time{}
	c.total, c.success, c.errors, c.skipped, c.bytes, c.rows = 0, 0, 0, 0, 0, 0
	c.latency = metrics.NewLatencyHistogram()
	c.wait = metrics.NewLatencyHistogram()
	c.statuses = make(map[string]int64)
	c.kinds = make(map[string]int64)
	c.winStart = c.start
	c.winCount, c.winBytes, c.winErrs = 0, 0, 0
	c.planned = planned
	c.targetRPS = targetRPS
	c.series = c.series[:0]
	c.running = true
}

// End marks the run finished.
func (c *Collector) End() {
	c.mu.Lock()
	c.end = time.Now()
	c.running = false
	c.mu.Unlock()
}

// SetPlanned updates the expected total (for progress %).
func (c *Collector) SetPlanned(n int64) {
	c.mu.Lock()
	c.planned = n
	c.mu.Unlock()
}

// AddSkipped records work the scheduler refused to run (backpressure signal).
func (c *Collector) AddSkipped(n int64) {
	c.mu.Lock()
	c.skipped += n
	c.mu.Unlock()
}

// Add records one completed result.
func (c *Collector) Add(r model.Result) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.total++
	if r.Bytes > 0 {
		c.bytes += r.Bytes
	}
	c.winCount++
	c.winBytes += r.Bytes
	if r.Rows > 0 {
		c.rows += r.Rows
	}
	c.latency.Record(r.Duration)
	c.wait.Record(r.WaitTime)

	if r.Err != nil {
		c.errors++
		c.winErrs++
		c.kinds[ClassifyError(r.Err)]++
		c.statuses["error"]++
		return
	}
	c.statuses[statusKey(r.Status)]++
	if r.Failed {
		// The request completed but the response was not acceptable, so it is an
		// error for reporting purposes without inventing a transport error.
		c.errors++
		c.winErrs++
		kind := "http_" + statusKey(r.Status)
		if r.Tag != "" {
			kind = r.Tag
		}
		c.kinds[kind]++
		return
	}
	c.success++
}

// SetRunning toggles the running flag reported to the UI.
func (c *Collector) SetRunning(v bool) {
	c.mu.Lock()
	c.running = v
	c.mu.Unlock()
}

func statusKey(code int) string {
	switch {
	case code >= 100 && code < 200:
		return "1xx"
	case code >= 200 && code < 300:
		return "2xx"
	case code >= 300 && code < 400:
		return "3xx"
	case code >= 400 && code < 500:
		return "4xx"
	case code >= 500 && code < 600:
		return "5xx"
	}
	return "other"
}

// ClassifyError buckets errors so the UI can show a breakdown.
func ClassifyError(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	switch {
	case strings.HasPrefix(s, "unexpected reply"):
		// A protocol probe got an answer that did not match what the job expects.
		return "unexpected_reply"
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(s, "timeout") ||
		strings.Contains(s, "deadline exceeded") || strings.Contains(s, "Client.Timeout"):
		return "timeout"
	case strings.Contains(s, "connection refused"):
		return "connection_refused"
	case strings.Contains(s, "connection reset"):
		return "connection_reset"
	case strings.Contains(s, "no such host"):
		return "dns"
	case strings.Contains(s, "certificate") || strings.Contains(s, "tls"):
		return "tls"
	case strings.Contains(s, "EOF"):
		return "eof"
	case strings.Contains(s, "too many open files"):
		return "fd_exhausted"
	case strings.Contains(s, "context canceled"):
		return "canceled"
	}
	return "other"
}

// Snapshot computes the current aggregate view and appends it to the series.
func (c *Collector) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshotLocked()
}

func (c *Collector) snapshotLocked() Snapshot {
	now := time.Now()
	elapsed := now.Sub(c.start)
	if elapsed < 0 {
		elapsed = 0
	}
	secs := elapsed.Seconds()

	// Instantaneous RPS over the rolling window.
	if now.Sub(c.winStart) >= c.winPeriod {
		c.winStart = now
		c.winCount, c.winBytes, c.winErrs = 0, 0, 0
	}
	winSecs := now.Sub(c.winStart).Seconds()
	rps := 0.0
	bps := 0.0
	if winSecs > 0.001 {
		rps = float64(c.winCount) / winSecs
		bps = float64(c.winBytes) / winSecs
	}
	avgRPS := 0.0
	if secs > 0.001 {
		avgRPS = float64(c.total) / secs
	}

	progress := 0.0
	if c.planned > 0 {
		progress = float64(c.total) / float64(c.planned) * 100
		if progress > 100 {
			progress = 100
		}
	}
	achieved := 0.0
	if c.targetRPS > 0 && avgRPS > 0 {
		achieved = avgRPS / c.targetRPS * 100
	}

	s := Snapshot{
		Timestamp:     now,
		ElapsedMs:     elapsed.Milliseconds(),
		Total:         c.total,
		Success:       c.success,
		Errors:        c.errors,
		Skipped:       c.skipped,
		RPS:           rps,
		AvgRPS:        avgRPS,
		BytesTotal:    c.bytes,
		RowsTotal:     c.rows,
		ThroughputBps: bps,
		StatusCodes:   copyCounts(c.statuses),
		ErrorKinds:    copyCounts(c.kinds),
		Latency:       c.latency.Report(metrics.DefaultQuantiles),
		WaitTime:      c.wait.Report(metrics.DefaultQuantiles),
		ProgressPct:   progress,
		TargetRPS:     c.targetRPS,
		AchievedPct:   achieved,
		Running:       c.running,
	}
	c.series = append(c.series, s)
	if len(c.series) > maxSeries {
		// Drop the oldest half to amortize the copy.
		c.series = append(c.series[:0], c.series[len(c.series)/2:]...)
	}
	return s
}

func copyCounts(m map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Series returns a copy of retained snapshots, oldest first.
func (c *Collector) Series() []Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Snapshot, len(c.series))
	copy(out, c.series)
	return out
}

// Summary is the final aggregate report for a finished run.
type Summary struct {
	RunID       string           `json:"runId"`
	JobName     string           `json:"jobName"`
	Executor    string           `json:"executor"`
	Target      string           `json:"target"`
	StartedAt   time.Time        `json:"startedAt"`
	EndedAt     time.Time        `json:"endedAt"`
	DurationMs  int64            `json:"durationMs"`
	Total       int64            `json:"total"`
	Success     int64            `json:"success"`
	Errors      int64            `json:"errors"`
	Skipped     int64            `json:"skipped"`
	AvgRPS      float64          `json:"avgRps"`
	BytesTotal  int64            `json:"bytesTotal"`
	RowsTotal   int64            `json:"rowsTotal"`
	Throughput  float64          `json:"throughputBps"`
	StatusCodes map[string]int64 `json:"statusCodes"`
	ErrorKinds  map[string]int64 `json:"errorKinds"`
	Latency     metrics.Report   `json:"latency"`
	WaitTime    metrics.Report   `json:"waitTime"`
	Aborted     bool             `json:"aborted"`
	Reason      string           `json:"reason,omitempty"`
	// Resources is what the load generator itself used during the run.
	Resources *sysstat.Resources `json:"resources,omitempty"`
	// Series is requests/second and latency over time, thinned to at most
	// maxSummarySeries points.
	Series []SeriesPoint `json:"series,omitempty"`
}

// Summary builds the final report. Safe to call at any point.
func (c *Collector) Summary(runID, jobName, executor, target string, aborted bool, reason string) Summary {
	c.mu.Lock()
	defer c.mu.Unlock()

	end := c.end
	if end.IsZero() {
		end = time.Now()
	}
	elapsed := end.Sub(c.start)
	secs := elapsed.Seconds()
	avgRPS := 0.0
	throughput := 0.0
	if secs > 0.001 {
		avgRPS = float64(c.total) / secs
		throughput = float64(c.bytes) / secs
	}
	return Summary{
		Series:      c.thinnedSeriesLocked(),
		RunID:       runID,
		JobName:     jobName,
		Executor:    executor,
		Target:      target,
		StartedAt:   c.start,
		EndedAt:     end,
		DurationMs:  elapsed.Milliseconds(),
		Total:       c.total,
		Success:     c.success,
		Errors:      c.errors,
		Skipped:     c.skipped,
		AvgRPS:      avgRPS,
		BytesTotal:  c.bytes,
		RowsTotal:   c.rows,
		Throughput:  throughput,
		StatusCodes: copyCounts(c.statuses),
		ErrorKinds:  copyCounts(c.kinds),
		Latency:     c.latency.Report(metrics.DefaultQuantiles),
		WaitTime:    c.wait.Report(metrics.DefaultQuantiles),
		Aborted:     aborted,
		Reason:      reason,
	}
}

// SortCounts returns map keys sorted for stable JSON output.
func SortCounts(m map[string]int64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// thinnedSeriesLocked returns the retained snapshots as at most
// maxSummarySeries evenly spaced points. The caller holds c.mu.
func (c *Collector) thinnedSeriesLocked() []SeriesPoint {
	n := len(c.series)
	if n == 0 {
		return nil
	}
	step := 1
	if n > maxSummarySeries {
		step = (n + maxSummarySeries - 1) / maxSummarySeries
	}
	out := make([]SeriesPoint, 0, n/step+1)
	for i := 0; i < n; i += step {
		out = append(out, seriesPoint(c.series[i]))
	}
	if last := c.series[n-1]; (n-1)%step != 0 {
		out = append(out, seriesPoint(last)) // always keep the final sample
	}
	return out
}

func seriesPoint(s Snapshot) SeriesPoint {
	p := s.Latency.Percentiles
	return SeriesPoint{T: float64(s.ElapsedMs) / 1000, RPS: s.RPS, P50: p["p50"], P95: p["p95"], P99: p["p99"]}
}
