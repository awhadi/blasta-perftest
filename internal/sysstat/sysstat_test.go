package sysstat

import (
	"errors"
	"testing"
	"time"
)

// CPU is a rate: cumulative readings on 2 cores, with 1 extra CPU-second burned
// per second, is 50%.
func TestSamplerComputesCPURate(t *testing.T) {
	var cpu float64
	s := &sampler{read: func() (raw, error) {
		cpu += 0.02 // 20 ms of CPU time per call
		return raw{cpuSeconds: cpu, cores: 2, memUsed: 512, memLimit: 1024, scope: "container"}, nil
	}}
	s.sample()
	st, ok := s.latest()
	if !ok || st.CPUPct != 0 || st.MemPct != 50 || st.Scope != "container" {
		t.Fatalf("first reading must exist with CPU 0 (no rate yet): %+v ok=%v", st, ok)
	}
	time.Sleep(40 * time.Millisecond)
	s.sample()
	st, _ = s.latest()
	if st.CPUPct <= 0 || st.CPUPct > 100 {
		t.Errorf("CPUPct = %v, want within (0,100]", st.CPUPct)
	}
}

func TestSamplerSurvivesReadErrors(t *testing.T) {
	s := &sampler{read: func() (raw, error) { return raw{}, errors.New("boom") }}
	s.sample()
	if _, ok := s.latest(); ok {
		t.Error("no reading should be reported when the read fails")
	}
}

// A run records start/avg/peak and a bounded series, and reports nothing when
// the platform cannot measure.
func TestRecorderSummarisesARun(t *testing.T) {
	var n int
	rec := newRecorder(func() (raw, error) {
		n++
		return raw{cpuSeconds: float64(n) * 0.02, cores: 4, memUsed: uint64(100 + n*10), memLimit: 1000, scope: "container"}, nil
	}, 10*time.Millisecond)
	rec.Start()
	rec.Start() // a second Start must be harmless
	time.Sleep(120 * time.Millisecond)
	res := rec.Stop()
	if res == nil || res.Samples < 3 {
		t.Fatalf("expected several samples, got %+v", res)
	}
	if res.MemStart != 110 || res.MemPeak <= res.MemStart || res.MemAvg < res.MemStart || res.MemAvg > res.MemPeak {
		t.Errorf("memory start/avg/peak inconsistent: %+v", res)
	}
	if res.CPUPeakPct < res.CPUAvgPct || res.CPUPeakPct > 100 || res.Cores != 4 {
		t.Errorf("cpu avg/peak inconsistent: %+v", res)
	}
	if want := res.CPUAvgPct * 4 / 100; res.CPUAvgCores < want-1e-9 || res.CPUAvgCores > want+1e-9 {
		t.Errorf("CPUAvgCores = %v, want %v", res.CPUAvgCores, want)
	}
	if len(res.Series) == 0 || len(res.Series) > maxSeries {
		t.Errorf("series length %d outside (0, %d]", len(res.Series), maxSeries)
	}
}

func TestRecorderBoundsMemoryOnLongRuns(t *testing.T) {
	r := newRecorder(func() (raw, error) { return raw{}, nil }, time.Hour)
	r.started = time.Now()
	for i := 0; i < 5000; i++ {
		r.record(Stats{CPUPct: 10, MemUsed: 1, Cores: 1, At: r.started.Add(time.Duration(i) * time.Second)})
	}
	r.mu.Lock()
	n := len(r.points)
	r.mu.Unlock()
	if n > 2*maxSeries {
		t.Errorf("kept %d points, must stay within %d", n, 2*maxSeries)
	}
	if res := r.Summary(); res == nil || len(res.Series) > maxSeries || res.Samples != 5000 {
		t.Errorf("summary wrong: %+v", res)
	}
}

func TestRecorderReportsNothingWithoutData(t *testing.T) {
	rec := newRecorder(func() (raw, error) { return raw{}, errors.New("unsupported") }, 10*time.Millisecond)
	rec.Start()
	time.Sleep(40 * time.Millisecond)
	if res := rec.Stop(); res != nil {
		t.Errorf("an unsupported platform must record nothing, got %+v", res)
	}
	if _, ok := rec.Latest(); ok {
		t.Error("no live reading expected")
	}
}

func TestClamp(t *testing.T) {
	if clamp(-5) != 0 || clamp(150) != 100 || clamp(42) != 42 {
		t.Error("clamp wrong")
	}
}
