// Package sysstat measures how much CPU and memory the machine (or container)
// BLASTA runs on uses WHILE A LOAD TEST RUNS, so a result can say what the load
// generator itself cost, and whether it was saturated.
//
// It has no dependencies and is Linux-only, because BLASTA runs in a Linux
// container: it reads cgroup v2 (the container's own usage and limits) and
// falls back to /proc for the whole host. Other systems record nothing.
package sysstat

import (
	"sync"
	"time"
)

// Stats is one reading.
type Stats struct {
	// CPUPct is the share of the available CPU capacity in use, 0-100.
	CPUPct float64 `json:"cpuPct"`
	// Cores is the CPU capacity CPUPct is measured against (a container quota
	// such as 2.5, or the number of CPUs when there is no quota).
	Cores float64 `json:"cores"`
	// MemUsed and MemLimit are bytes. MemLimit is the container limit, or the
	// machine's memory when there is none; 0 when unknown.
	MemUsed  uint64  `json:"memUsed"`
	MemLimit uint64  `json:"memLimit"`
	MemPct   float64 `json:"memPct"`
	// Scope says what was measured: "container" or "host".
	Scope string    `json:"scope"`
	At    time.Time `json:"at"`
}

// Point is one sample in a run's time series.
type Point struct {
	T       float64 `json:"t"` // seconds since the run started
	CPUPct  float64 `json:"cpuPct"`
	MemUsed uint64  `json:"memUsed"`
}

// Resources summarises the server's CPU and memory over one run. It is stored
// with the run, so History and the downloaded report show it.
type Resources struct {
	Scope        string  `json:"scope"`
	Cores        float64 `json:"cores"`
	MemLimit     uint64  `json:"memLimit"`
	Samples      int     `json:"samples"`
	CPUAvgPct    float64 `json:"cpuAvgPct"`
	CPUPeakPct   float64 `json:"cpuPeakPct"`
	CPUAvgCores  float64 `json:"cpuAvgCores"`
	CPUPeakCores float64 `json:"cpuPeakCores"`
	MemStart     uint64  `json:"memStart"`
	MemAvg       uint64  `json:"memAvg"`
	MemPeak      uint64  `json:"memPeak"`
	// Series is the time series, thinned to at most maxSeries points.
	Series []Point `json:"series,omitempty"`
}

// maxSeries bounds how many points a stored run keeps.
const maxSeries = 120

// raw is one cumulative reading: CPU seconds consumed since boot or start, and
// the memory figures at that instant.
type raw struct {
	cpuSeconds float64
	cores      float64
	memUsed    uint64
	memLimit   uint64
	scope      string
}

// sampler turns cumulative CPU readings into a rate.
type sampler struct {
	read func() (raw, error)

	mu      sync.Mutex
	last    Stats
	hasLast bool
	prev    raw
	prevAt  time.Time
	hasPrev bool
}

func (s *sampler) sample() {
	r, err := s.read()
	now := time.Now()
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Stats{Cores: r.cores, MemUsed: r.memUsed, MemLimit: r.memLimit, Scope: r.scope, At: now}
	if r.memLimit > 0 {
		st.MemPct = clamp(float64(r.memUsed) / float64(r.memLimit) * 100)
	}
	if s.hasPrev && r.cores > 0 {
		if dt := now.Sub(s.prevAt).Seconds(); dt > 0 {
			st.CPUPct = clamp((r.cpuSeconds - s.prev.cpuSeconds) / (dt * r.cores) * 100)
		}
	}
	s.prev, s.prevAt, s.hasPrev = r, now, true
	s.last, s.hasLast = st, true
}

func (s *sampler) latest() (Stats, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last, s.hasLast
}

// Recorder samples once a second for the length of a run and keeps bounded
// statistics (never more than a few hundred bytes per sample).
type Recorder struct {
	s     *sampler
	every time.Duration

	mu       sync.Mutex
	started  time.Time
	points   []Point
	sumCPU   float64
	sumMem   float64
	n        int
	peakCPU  float64
	peakMem  uint64
	startMem uint64
	scope    string
	cores    float64
	limit    uint64
	stop     chan struct{}
	done     chan struct{}
}

// NewRecorder returns a Recorder for this platform.
func NewRecorder() *Recorder { return newRecorder(platformRead, time.Second) }

func newRecorder(read func() (raw, error), every time.Duration) *Recorder {
	return &Recorder{s: &sampler{read: read}, every: every}
}

// Start takes the baseline reading and begins sampling. It is safe to call
// once; later calls do nothing.
func (r *Recorder) Start() {
	r.mu.Lock()
	if r.stop != nil {
		r.mu.Unlock()
		return
	}
	r.started = time.Now()
	stop, done := make(chan struct{}), make(chan struct{})
	r.stop, r.done = stop, done
	r.mu.Unlock()

	r.s.sample()
	if st, ok := r.s.latest(); ok {
		r.mu.Lock()
		r.startMem = st.MemUsed
		r.mu.Unlock()
	}
	// The goroutine uses its own copies of the channels: Stop clears r.stop, and
	// reading the field here would then wait forever on a nil channel.
	go func() {
		defer close(done)
		t := time.NewTicker(r.every)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				r.s.sample()
				if st, ok := r.s.latest(); ok {
					r.record(st)
				}
			}
		}
	}()
}

func (r *Recorder) record(st Stats) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.n++
	r.sumCPU += st.CPUPct
	r.sumMem += float64(st.MemUsed)
	if st.CPUPct > r.peakCPU {
		r.peakCPU = st.CPUPct
	}
	if st.MemUsed > r.peakMem {
		r.peakMem = st.MemUsed
	}
	r.scope, r.cores, r.limit = st.Scope, st.Cores, st.MemLimit
	// Keep every point while the series is short; beyond that thin it by
	// dropping every other point, so memory stays bounded however long the run.
	r.points = append(r.points, Point{T: st.At.Sub(r.started).Seconds(), CPUPct: st.CPUPct, MemUsed: st.MemUsed})
	if len(r.points) > 2*maxSeries {
		kept := r.points[:0]
		for i := 0; i < len(r.points); i += 2 {
			kept = append(kept, r.points[i])
		}
		r.points = kept
	}
}

// Reading is one cumulative reading supplied by NewRecorderFrom.
type Reading struct {
	CPUSeconds float64 // CPU time consumed so far
	Cores      float64
	MemUsed    uint64
	MemLimit   uint64
	Scope      string
}

// NewRecorderFrom records from a caller-supplied source instead of the
// operating system, sampling every `every`. It exists so code that depends on a
// Recorder can be tested without a Linux host.
func NewRecorderFrom(read func() (Reading, error), every time.Duration) *Recorder {
	return newRecorder(func() (raw, error) {
		r, err := read()
		return raw{cpuSeconds: r.CPUSeconds, cores: r.Cores, memUsed: r.MemUsed, memLimit: r.MemLimit, scope: r.Scope}, err
	}, every)
}

// Latest is the newest reading, for the live view. ok is false before the first
// reading or on platforms that cannot measure.
func (r *Recorder) Latest() (Stats, bool) { return r.s.latest() }

// Stop ends sampling and returns the summary, or nil if nothing was measured
// (an unsupported platform, or a run shorter than one sample).
func (r *Recorder) Stop() *Resources {
	r.mu.Lock()
	stop, done := r.stop, r.done
	r.stop = nil
	r.mu.Unlock()
	if stop != nil {
		close(stop)
		<-done
	}
	return r.Summary()
}

// Summary returns the figures so far without stopping.
func (r *Recorder) Summary() *Resources {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.n == 0 {
		return nil
	}
	avgPct := r.sumCPU / float64(r.n)
	res := &Resources{
		Scope: r.scope, Cores: r.cores, MemLimit: r.limit, Samples: r.n,
		CPUAvgPct: avgPct, CPUPeakPct: r.peakCPU,
		CPUAvgCores: avgPct * r.cores / 100, CPUPeakCores: r.peakCPU * r.cores / 100,
		MemStart: r.startMem, MemAvg: uint64(r.sumMem / float64(r.n)), MemPeak: r.peakMem,
	}
	// Report at most maxSeries points, evenly spaced.
	step := 1
	if len(r.points) > maxSeries {
		step = (len(r.points) + maxSeries - 1) / maxSeries
	}
	for i := 0; i < len(r.points); i += step {
		res.Series = append(res.Series, r.points[i])
	}
	return res
}

func clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}
