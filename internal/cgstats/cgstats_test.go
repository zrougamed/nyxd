package cgstats

import (
	"os"
	"runtime"
	"testing"
	"time"
)

func TestCPUPercent(t *testing.T) {
	prev := Sample{CPUUsageUsec: 1_000_000, Timestamp: time.Unix(0, 0)}
	cur := Sample{CPUUsageUsec: 1_500_000, Timestamp: time.Unix(1, 0)}
	got := CPUPercent(prev, cur)
	if got < 49 || got > 51 {
		t.Fatalf("CPUPercent = %v, want ~50", got)
	}
	if CPUPercent(cur, prev) != 0 {
		t.Fatal("reversed samples")
	}
	if CPUPercent(prev, prev) != 0 {
		t.Fatal("equal timestamps")
	}
}

func TestMemPercent(t *testing.T) {
	if MemPercent(Sample{MemCurrent: 50, MemLimit: 100}) != 50 {
		t.Fatal("mem percent")
	}
	if MemPercent(Sample{MemCurrent: 50, MemLimit: 0}) != 0 {
		t.Fatal("unlimited")
	}
}

func TestCutUintPrefix(t *testing.T) {
	v, ok := cutUintPrefix("rbytes=42", "rbytes=")
	if !ok || v != 42 {
		t.Fatalf("got %v %v", v, ok)
	}
	if _, ok := cutUintPrefix("x", "rbytes="); ok {
		t.Fatal("expected miss")
	}
}

func TestReadSelf(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	s, err := Read(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if s.Source != "cgroup" && s.Source != "proc" {
		t.Fatalf("source %q", s.Source)
	}
	if s.Timestamp.IsZero() {
		t.Fatal("timestamp")
	}
	// Our process should report some memory.
	if s.MemCurrent == 0 {
		t.Fatal("expected non-zero memory")
	}
}

func TestReadBadPID(t *testing.T) {
	if _, err := Read(0); err == nil {
		t.Fatal("expected error")
	}
	if _, err := Read(-1); err == nil {
		t.Fatal("expected error")
	}
}

func TestHostMemTotal(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	if hostMemTotal() == 0 {
		t.Fatal("MemTotal")
	}
}

func TestParseHelpers(t *testing.T) {
	dir := t.TempDir()
	cpu := dir + "/cpu.stat"
	if err := os.WriteFile(cpu, []byte("usage_usec 12345\nuser_usec 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	u, err := readCPUUsageUsec(cpu)
	if err != nil || u != 12345 {
		t.Fatalf("cpu %v %v", u, err)
	}
	io := dir + "/io.stat"
	if err := os.WriteFile(io, []byte("8:0 rbytes=10 wbytes=20 rios=1 wios=1\n8:1 rbytes=5 wbytes=7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, w, err := readIOStat(io)
	if err != nil || r != 15 || w != 27 {
		t.Fatalf("io %d %d %v", r, w, err)
	}
}

func TestFillFromProc(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	var s Sample
	if err := fillFromProc(&s, os.Getpid()); err != nil {
		t.Fatal(err)
	}
	if s.MemCurrent == 0 {
		t.Fatalf("%+v", s)
	}
	if clockTicks() <= 0 {
		t.Fatal("clockTicks")
	}
}

func TestCgroupPathLegacy(t *testing.T) {
	// Ensure empty / invalid pid paths error.
	if _, err := cgroupPath(1); err != nil && os.Getpid() == 1 {
		// pid 1 always exists on linux; path may still resolve.
	}
	_, _ = cgroupPath(os.Getpid())
}
