// Package cgstats reads a process's resource usage from cgroup v2 and /proc.
// Used by nyxd for docker/podman-like `stats` without extra dependencies.
package cgstats

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Sample is one point-in-time reading for a container's init process.
type Sample struct {
	CPUUsageUsec uint64 // cumulative CPU time from cgroup cpu.stat or /proc
	MemCurrent   uint64 // bytes in use
	MemLimit     uint64 // bytes; 0 means unknown/unlimited
	NetRx        uint64 // bytes received (sum of non-loopback ifaces)
	NetTx        uint64 // bytes transmitted
	BlockRead    uint64 // bytes read (cgroup io.stat)
	BlockWrite   uint64 // bytes written
	PIDs         uint64 // tasks in the cgroup, or 1 when unknown
	Timestamp    time.Time
	Source       string // "cgroup" or "proc" (fallback when cgroups are unavailable)
}

// Read collects a sample for the given host PID (container init).
func Read(pid int) (Sample, error) {
	if pid <= 0 {
		return Sample{}, fmt.Errorf("invalid pid %d", pid)
	}
	s := Sample{Timestamp: time.Now().UTC(), PIDs: 1}
	cg, err := cgroupPath(pid)
	if err == nil && cg != "" {
		if fillFromCgroup(&s, cg) {
			s.Source = "cgroup"
			fillNet(&s, pid)
			return s, nil
		}
	}
	if err := fillFromProc(&s, pid); err != nil {
		return Sample{}, err
	}
	s.Source = "proc"
	fillNet(&s, pid)
	return s, nil
}

// CPUPercent returns CPU usage as a percentage of one CPU between two samples.
// Values above 100 mean the container used more than one core.
func CPUPercent(prev, cur Sample) float64 {
	if cur.Timestamp.Before(prev.Timestamp) || cur.Timestamp.Equal(prev.Timestamp) {
		return 0
	}
	if cur.CPUUsageUsec < prev.CPUUsageUsec {
		return 0
	}
	wall := cur.Timestamp.Sub(prev.Timestamp).Microseconds()
	if wall <= 0 {
		return 0
	}
	return float64(cur.CPUUsageUsec-prev.CPUUsageUsec) / float64(wall) * 100
}

// MemPercent is memory usage relative to limit (0 when limit unknown).
func MemPercent(s Sample) float64 {
	if s.MemLimit == 0 {
		return 0
	}
	return float64(s.MemCurrent) / float64(s.MemLimit) * 100
}

func cgroupPath(pid int) (string, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", pid))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// cgroup v2: "0::/path"
		if strings.HasPrefix(line, "0::") {
			rel := strings.TrimPrefix(line, "0::")
			if rel == "" {
				rel = "/"
			}
			return filepath.Join("/sys/fs/cgroup", rel), nil
		}
		// legacy hybrid: controllers:path — take the path after the second colon
		parts := strings.SplitN(line, ":", 3)
		if len(parts) == 3 && parts[2] != "" {
			return filepath.Join("/sys/fs/cgroup", parts[2]), nil
		}
	}
	return "", fmt.Errorf("no cgroup path for pid %d", pid)
}

func fillFromCgroup(s *Sample, cg string) bool {
	got := false
	if v, err := readUintFile(filepath.Join(cg, "memory.current")); err == nil {
		s.MemCurrent = v
		got = true
	}
	if v, err := readUintFile(filepath.Join(cg, "memory.max")); err == nil {
		s.MemLimit = v
	} else if b, err := os.ReadFile(filepath.Join(cg, "memory.max")); err == nil {
		if strings.TrimSpace(string(b)) == "max" {
			s.MemLimit = hostMemTotal()
		}
	}
	if s.MemLimit == 0 {
		s.MemLimit = hostMemTotal()
	}
	if u, err := readCPUUsageUsec(filepath.Join(cg, "cpu.stat")); err == nil {
		s.CPUUsageUsec = u
		got = true
	}
	if v, err := readUintFile(filepath.Join(cg, "pids.current")); err == nil {
		s.PIDs = v
		got = true
	}
	if r, w, err := readIOStat(filepath.Join(cg, "io.stat")); err == nil {
		s.BlockRead, s.BlockWrite = r, w
	}
	return got
}

func fillFromProc(s *Sample, pid int) error {
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return fmt.Errorf("read status: %w", err)
	}
	for _, line := range strings.Split(string(status), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, _ := strconv.ParseUint(fields[1], 10, 64)
				s.MemCurrent = kb * 1024
			}
		}
	}
	s.MemLimit = hostMemTotal()
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return fmt.Errorf("read stat: %w", err)
	}
	// After the comm field in parentheses: utime(14) stime(15) in clock ticks.
	closeParen := strings.LastIndexByte(string(stat), ')')
	if closeParen < 0 || closeParen+2 >= len(stat) {
		return fmt.Errorf("parse stat: missing comm")
	}
	fields := strings.Fields(string(stat[closeParen+2:]))
	if len(fields) < 13 {
		return fmt.Errorf("parse stat: too few fields")
	}
	utime, _ := strconv.ParseUint(fields[11], 10, 64)
	stime, _ := strconv.ParseUint(fields[12], 10, 64)
	// Convert ticks to microseconds (Linux USER_HZ is typically 100).
	hz := uint64(clockTicks())
	if hz == 0 {
		hz = 100
	}
	s.CPUUsageUsec = (utime + stime) * 1_000_000 / hz
	return nil
}

func fillNet(s *Sample, pid int) {
	f, err := os.Open(fmt.Sprintf("/proc/%d/net/dev", pid))
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.Contains(line, ":") || strings.HasPrefix(line, "Inter") || strings.HasPrefix(line, "face") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		if name == "lo" {
			continue
		}
		fields := strings.Fields(parts[1])
		if len(fields) < 9 {
			continue
		}
		rx, _ := strconv.ParseUint(fields[0], 10, 64)
		tx, _ := strconv.ParseUint(fields[8], 10, 64)
		s.NetRx += rx
		s.NetTx += tx
	}
}

func readUintFile(path string) (uint64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(string(b))
	if s == "" || s == "max" {
		return 0, fmt.Errorf("not a number")
	}
	return strconv.ParseUint(s, 10, 64)
}

func readCPUUsageUsec(path string) (uint64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "usage_usec ") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				return strconv.ParseUint(fields[1], 10, 64)
			}
		}
	}
	return 0, fmt.Errorf("usage_usec not found")
}

func readIOStat(path string) (read, write uint64, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		for _, f := range strings.Fields(line) {
			if v, ok := cutUintPrefix(f, "rbytes="); ok {
				read += v
			}
			if v, ok := cutUintPrefix(f, "wbytes="); ok {
				write += v
			}
		}
	}
	return read, write, nil
}

func cutUintPrefix(s, prefix string) (uint64, bool) {
	if !strings.HasPrefix(s, prefix) {
		return 0, false
	}
	v, err := strconv.ParseUint(strings.TrimPrefix(s, prefix), 10, 64)
	return v, err == nil
}

func hostMemTotal() uint64 {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, _ := strconv.ParseUint(fields[1], 10, 64)
				return kb * 1024
			}
		}
	}
	return 0
}
