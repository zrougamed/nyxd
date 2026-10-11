//go:build unix

package daemonlock

import (
	"os"
	"testing"
)

func TestAcquireExclusive(t *testing.T) {
	dir := t.TempDir()
	l1, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Acquire(dir)
	if err == nil {
		t.Fatal("expected second Acquire to fail")
	}
	if err := l1.Close(); err != nil {
		t.Fatal(err)
	}
	l3, err := Acquire(dir)
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	if err := l3.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestHolder(t *testing.T) {
	dir := t.TempDir()
	if pid, err := Holder(dir); err != nil || pid != 0 {
		t.Fatalf("no lock: pid=%d err=%v", pid, err)
	}
	l, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	if pid, err := Holder(dir); err != nil || pid != os.Getpid() {
		t.Fatalf("held: pid=%d err=%v", pid, err)
	}
	l.Close()
	if pid, err := Holder(dir); err != nil || pid != 0 {
		t.Fatalf("released: pid=%d err=%v", pid, err)
	}
}
