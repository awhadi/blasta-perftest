//go:build linux

package sysstat

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadCgroup2(t *testing.T) {
	d := t.TempDir()
	write(t, d, "sys/fs/cgroup/cpu.stat", "usage_usec 2500000\nuser_usec 1\nsystem_usec 2\n")
	write(t, d, "sys/fs/cgroup/cpu.max", "250000 100000\n") // 2.5 CPUs
	write(t, d, "sys/fs/cgroup/memory.current", "300000000\n")
	write(t, d, "sys/fs/cgroup/memory.stat", "anon 1\ninactive_file 100000000\n")
	write(t, d, "sys/fs/cgroup/memory.max", "1000000000\n")
	r, err := readCgroup2(d)
	if err != nil {
		t.Fatal(err)
	}
	if r.cpuSeconds != 2.5 || r.cores != 2.5 {
		t.Errorf("cpu = %v s on %v cores, want 2.5 s on 2.5", r.cpuSeconds, r.cores)
	}
	if r.memUsed != 200000000 {
		t.Errorf("memUsed = %d, want 200000000 (page cache excluded)", r.memUsed)
	}
	if r.memLimit != 1000000000 || r.scope != "container" {
		t.Errorf("limit/scope = %d %s", r.memLimit, r.scope)
	}
}

// With no quota and no memory limit the machine's capacity is the denominator.
func TestReadCgroup2Unlimited(t *testing.T) {
	d := t.TempDir()
	write(t, d, "sys/fs/cgroup/cpu.stat", "usage_usec 1000000\n")
	write(t, d, "sys/fs/cgroup/cpu.max", "max 100000\n")
	write(t, d, "sys/fs/cgroup/memory.current", "5000\n")
	write(t, d, "sys/fs/cgroup/memory.max", "max\n")
	write(t, d, "proc/meminfo", "MemTotal:        8000 kB\nMemAvailable:    4000 kB\n")
	r, err := readCgroup2(d)
	if err != nil {
		t.Fatal(err)
	}
	if r.cores < 1 || r.memLimit != 8000*1024 {
		t.Errorf("cores=%v memLimit=%d, want >=1 and the host total", r.cores, r.memLimit)
	}
}

func TestReadHost(t *testing.T) {
	d := t.TempDir()
	write(t, d, "proc/stat", "cpu  100 0 50 800 50 0 0 0 0 0\ncpu0 1 2 3\n")
	write(t, d, "proc/meminfo", "MemTotal:       1000 kB\nMemAvailable:     400 kB\n")
	r, err := readHost(d)
	if err != nil {
		t.Fatal(err)
	}
	// busy = user+nice+system = 150 jiffies = 1.5 s at 100 Hz (idle+iowait excluded)
	if r.cpuSeconds != 1.5 {
		t.Errorf("cpuSeconds = %v, want 1.5", r.cpuSeconds)
	}
	if r.memUsed != 600*1024 || r.memLimit != 1000*1024 || r.scope != "host" {
		t.Errorf("mem = %d/%d %s", r.memUsed, r.memLimit, r.scope)
	}
	if _, err := readHost(t.TempDir()); err == nil {
		t.Error("a missing /proc must be an error")
	}
}
