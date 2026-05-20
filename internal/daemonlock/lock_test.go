//go:build unix

package daemonlock

import (
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
