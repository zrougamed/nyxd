package supervisor

import (
	"bytes"
	"io"
	"log/slog"
	"runtime"
	"testing"

	"github.com/zrougamed/nyxd/internal/apparmor"
)

func TestResolveApparmorProfile(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	if resolveApparmorProfile("", quiet) != "" {
		t.Fatal("empty")
	}
	if got := resolveApparmorProfile("  ", nil); got != "" {
		t.Fatalf("got %q", got)
	}

	// Force AppArmor off: profile must be dropped (SELinux/Alpine/non-Linux path).
	restore := apparmor.StubForTest(t, false, "")
	defer restore()
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	if got := resolveApparmorProfile("gh-buildkit", log); got != "" {
		t.Fatalf("unavailable: got %q", got)
	}
	if !bytes.Contains(buf.Bytes(), []byte("AppArmor not available")) {
		t.Fatalf("log=%q", buf.String())
	}

	if runtime.GOOS != "linux" {
		// Off Linux, Available is always false; loaded-profile branches are Linux-only.
		return
	}

	// AppArmor on but profile missing.
	restore2 := apparmor.StubForTest(t, true, "other (enforce)\n")
	defer restore2()
	buf.Reset()
	if got := resolveApparmorProfile("gh-buildkit", log); got != "" {
		t.Fatalf("missing: got %q", got)
	}
	if !bytes.Contains(buf.Bytes(), []byte("profile not loaded")) {
		t.Fatalf("log=%q", buf.String())
	}

	// AppArmor on and profile loaded.
	restore3 := apparmor.StubForTest(t, true, "gh-buildkit (enforce)\n")
	defer restore3()
	if got := resolveApparmorProfile(" gh-buildkit ", quiet); got != "gh-buildkit" {
		t.Fatalf("loaded: got %q", got)
	}
}
