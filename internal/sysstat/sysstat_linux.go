//go:build linux

package sysstat

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// root is the filesystem root the readers use; tests point it at a fake tree.
var root = "/"

func platformRead() (raw, error) {
	if r, err := readCgroup2(root); err == nil {
		return r, nil
	}
	return readHost(root)
}

func readFile(rootDir, path string) (string, error) {
	b, err := os.ReadFile(filepath.Join(rootDir, path))
	return strings.TrimSpace(string(b)), err
}

// readCgroup2 reads this container's own usage. A quota in cpu.max becomes the
// CPU capacity; "max" means unlimited, so capacity is the machine's CPU count.
func readCgroup2(rootDir string) (raw, error) {
	stat, err := readFile(rootDir, "sys/fs/cgroup/cpu.stat")
	if err != nil {
		return raw{}, err
	}
	cur, err := readFile(rootDir, "sys/fs/cgroup/memory.current")
	if err != nil {
		return raw{}, err
	}
	var usec float64
	for _, line := range strings.Split(stat, "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[0] == "usage_usec" {
			usec, _ = strconv.ParseFloat(f[1], 64)
		}
	}
	r := raw{cpuSeconds: usec / 1e6, cores: float64(runtime.NumCPU()), scope: "container"}
	if cpuMax, err := readFile(rootDir, "sys/fs/cgroup/cpu.max"); err == nil {
		if f := strings.Fields(cpuMax); len(f) == 2 && f[0] != "max" {
			q, e1 := strconv.ParseFloat(f[0], 64)
			p, e2 := strconv.ParseFloat(f[1], 64)
			if e1 == nil && e2 == nil && p > 0 && q > 0 {
				r.cores = q / p
			}
		}
	}
	used, _ := strconv.ParseUint(cur, 10, 64)
	// Like `docker stats`, do not count reclaimable page cache as used memory.
	if ms, err := readFile(rootDir, "sys/fs/cgroup/memory.stat"); err == nil {
		for _, line := range strings.Split(ms, "\n") {
			if f := strings.Fields(line); len(f) == 2 && f[0] == "inactive_file" {
				if v, e := strconv.ParseUint(f[1], 10, 64); e == nil && v < used {
					used -= v
				}
			}
		}
	}
	r.memUsed = used
	if lim, err := readFile(rootDir, "sys/fs/cgroup/memory.max"); err == nil && lim != "max" {
		r.memLimit, _ = strconv.ParseUint(lim, 10, 64)
	}
	if r.memLimit == 0 {
		r.memLimit = hostMemTotal(rootDir)
	}
	return r, nil
}

// readHost reads the whole machine from /proc, for Linux without cgroup v2.
func readHost(rootDir string) (raw, error) {
	stat, err := readFile(rootDir, "proc/stat")
	if err != nil {
		return raw{}, err
	}
	line := strings.SplitN(stat, "\n", 2)[0]
	f := strings.Fields(line)
	if len(f) < 5 || f[0] != "cpu" {
		return raw{}, errors.New("unexpected /proc/stat")
	}
	var total, idle float64
	for i, v := range f[1:] {
		n, _ := strconv.ParseFloat(v, 64)
		if i < 8 { // user nice system idle iowait irq softirq steal
			total += n
		}
		if i == 3 || i == 4 {
			idle += n
		}
	}
	const hz = 100.0 // USER_HZ is 100 on every Linux the Go runtime supports
	mem, avail := hostMem(rootDir)
	used := uint64(0)
	if mem > avail {
		used = mem - avail
	}
	return raw{cpuSeconds: (total - idle) / hz, cores: float64(runtime.NumCPU()),
		memUsed: used, memLimit: mem, scope: "host"}, nil
}

func hostMemTotal(rootDir string) uint64 { t, _ := hostMem(rootDir); return t }

// hostMem returns MemTotal and MemAvailable in bytes.
func hostMem(rootDir string) (total, avail uint64) {
	s, err := readFile(rootDir, "proc/meminfo")
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		kb, _ := strconv.ParseUint(f[1], 10, 64)
		switch f[0] {
		case "MemTotal:":
			total = kb * 1024
		case "MemAvailable:":
			avail = kb * 1024
		}
	}
	return total, avail
}
