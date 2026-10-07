// Package compare sets two runs of the same test side by side: what changed, and whether it is
// worse than the limits someone chose ("p95 may be up to 15% slower than the baseline").
package compare

import (
	"fmt"
	"math"
	"time"

	"github.com/awhadi/blasta-perftest/internal/collector"
)

// Noise is how far a number may move, in either direction, before it is called better or worse:
// two runs of the same test never match exactly.
const Noise = 5.0

// Metric is one number in both runs.
type Metric struct {
	Key      string  `json:"key"`
	Name     string  `json:"name"`
	Unit     string  `json:"unit"` // ms | rps | percent | count
	Base     float64 `json:"base"`
	Now      float64 `json:"now"`
	DeltaPct float64 `json:"deltaPct"` // change relative to the baseline; 0 when the baseline is 0
	Delta    float64 `json:"delta"`    // change in the metric's own unit
	Verdict  string  `json:"verdict"`  // better | worse | same
	// LowerIsBetter tells a reader which way is good.
	LowerIsBetter bool `json:"lowerIsBetter"`
}

// Limits are the changes a person will accept. A negative value turns a limit off.
type Limits struct {
	LatencyPct    float64 `json:"latencyPct"`    // p95 and p99 may rise by up to this percent
	ErrorPoints   float64 `json:"errorPoints"`   // the error rate may rise by up to this many points
	ThroughputPct float64 `json:"throughputPct"` // requests per second may fall by up to this percent
}

// Off is Limits with everything off: compare, do not judge.
func Off() Limits { return Limits{LatencyPct: -1, ErrorPoints: -1, ThroughputPct: -1} }

// Result is the comparison.
type Result struct {
	Metrics     []Metric `json:"metrics"`
	Limits      Limits   `json:"limits"`
	Judged      bool     `json:"judged"` // at least one limit was set
	Regressions []string `json:"regressions"`
	Passed      bool     `json:"passed"`
	Notes       []string `json:"notes,omitempty"`
}

func errRate(s collector.Summary) float64 {
	if s.Total == 0 {
		return 0
	}
	return float64(s.Errors) / float64(s.Total) * 100
}

func ms(s collector.Summary, key string) float64 { return s.Latency.Percentiles[key] / 1000 }

func metric(key, name, unit string, base, now float64, lower bool) Metric {
	m := Metric{Key: key, Name: name, Unit: unit, Base: base, Now: now, Delta: now - base, LowerIsBetter: lower, Verdict: "same"}
	if base != 0 {
		m.DeltaPct = (now - base) / math.Abs(base) * 100
	}
	// Where the baseline is 0 there is no percentage: judge a move away from 0 by size alone.
	moved := m.DeltaPct
	if base == 0 && now != 0 {
		moved = 100
	}
	if math.Abs(moved) > Noise {
		worse := (moved > 0) == lower
		if worse {
			m.Verdict = "worse"
		} else {
			m.Verdict = "better"
		}
	}
	return m
}

// Runs compares now against base.
func Runs(base, now collector.Summary, lim Limits) Result {
	r := Result{Limits: lim, Passed: true}
	r.Metrics = []Metric{
		metric("avgRps", "Requests per second", "rps", base.AvgRPS, now.AvgRPS, false),
		metric("errorRate", "Error rate", "percent", errRate(base), errRate(now), true),
		metric("p50", "Latency p50", "ms", ms(base, "p50"), ms(now, "p50"), true),
		metric("p95", "Latency p95", "ms", ms(base, "p95"), ms(now, "p95"), true),
		metric("p99", "Latency p99", "ms", ms(base, "p99"), ms(now, "p99"), true),
		metric("max", "Latency max", "ms", float64(base.Latency.Max)/float64(time.Millisecond), float64(now.Latency.Max)/float64(time.Millisecond), true),
		metric("total", "Requests sent", "count", float64(base.Total), float64(now.Total), false),
	}
	if base.Total == 0 || now.Total == 0 {
		r.Notes = append(r.Notes, "One of the runs sent no requests, so the numbers say little.")
	}
	if base.Target != now.Target && base.Target != "" && now.Target != "" {
		r.Notes = append(r.Notes, "The two runs tested different addresses.")
	}
	by := map[string]Metric{}
	for _, m := range r.Metrics {
		by[m.Key] = m
	}
	fail := func(f string, a ...any) {
		r.Regressions = append(r.Regressions, fmt.Sprintf(f, a...))
		r.Passed = false
	}
	if lim.LatencyPct >= 0 {
		r.Judged = true
		for _, k := range []string{"p95", "p99"} {
			m := by[k]
			if m.Base > 0 && m.DeltaPct > lim.LatencyPct {
				fail("%s is %.1f%% slower (%.1f ms to %.1f ms), more than the %.0f%% allowed", k, m.DeltaPct, m.Base, m.Now, lim.LatencyPct)
			}
		}
	}
	if lim.ErrorPoints >= 0 {
		r.Judged = true
		if m := by["errorRate"]; m.Delta > lim.ErrorPoints {
			fail("the error rate rose by %.2f points (%.2f%% to %.2f%%), more than the %.2f allowed", m.Delta, m.Base, m.Now, lim.ErrorPoints)
		}
	}
	if lim.ThroughputPct >= 0 {
		r.Judged = true
		if m := by["avgRps"]; m.Base > 0 && -m.DeltaPct > lim.ThroughputPct {
			fail("throughput fell %.1f%% (%.1f to %.1f requests/s), more than the %.0f%% allowed", -m.DeltaPct, m.Base, m.Now, lim.ThroughputPct)
		}
	}
	return r
}
