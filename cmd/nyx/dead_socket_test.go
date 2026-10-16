package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every command reports a daemon that is not there instead of crashing.
func TestCommandsWithoutDaemon(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "none.sock")
	ctx := t.TempDir()
	os.WriteFile(filepath.Join(ctx, "Dockerfile"), []byte("FROM scratch\n"), 0o644) //nolint:errcheck
	archive := filepath.Join(ctx, "a.tar")
	os.WriteFile(archive, []byte("x"), 0o644) //nolint:errcheck
	compose := writeCompose(t)
	for _, args := range [][]string{
		{"ping"}, {"version"}, {"ps"}, {"ps", "-q"}, {"run", "-d", "x"}, {"run", "x"}, {"pull", "x"}, {"pull", "--json", "x"},
		{"stop", "c"}, {"rm", "c"}, {"rm", "-f", "c"}, {"kill", "c"}, {"logs", "c"}, {"logs", "-f", "c"}, {"exec", "c", "true"},
		{"image", "ls"}, {"image", "rm", "x"}, {"image", "prune"}, {"tag", "a", "b"}, {"push", "a:1"},
		{"save", "-o", filepath.Join(ctx, "out.tar"), "a:1"}, {"load", "-i", archive}, {"build", ctx},
		{"compose", "up", "-f", compose}, {"compose", "stop", "-f", compose}, {"compose", "down", "-f", compose},
	} {
		if _, _, err := nyx(t, append([]string{"-socket", sock}, args...)...); err == nil {
			t.Errorf("nyx %v without a daemon succeeded", args)
		}
	}
}

func TestPsEmpty(t *testing.T) {
	out, _, err := nyx(t, "-socket", stub(t, reply(200, `{"items":[]}`)), "ps")
	if err != nil || !strings.Contains(out, "CONTAINER ID") {
		t.Errorf("empty ps: %v %q", err, out)
	}
}

func TestDialErrorHints(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "none.sock")
	_, _, err := nyx(t, "-socket", missing, "ps")
	if msg := explainDialError(err).Error(); !strings.Contains(msg, "cannot reach nyxd at "+missing) {
		t.Errorf("missing socket: %q", msg)
	}

	if os.Geteuid() != 0 {
		denied := stub(t, reply(200, `{"items":[]}`))
		os.Chmod(denied, 0) //nolint:errcheck
		_, _, err = nyx(t, "-socket", denied, "ps")
		if msg := explainDialError(err).Error(); !strings.Contains(msg, "join the nyx group") {
			t.Errorf("unreadable socket: %q", msg)
		}
	}

	other := errors.New("boom")
	if explainDialError(other) != other {
		t.Error("unrelated errors must pass through")
	}
}

func TestVersionFlag(t *testing.T) {
	for _, f := range []string{"--version", "-v"} {
		out, _, err := nyx(t, "-socket", filepath.Join(t.TempDir(), "none.sock"), f)
		if err != nil || !strings.HasPrefix(out, "nyx ") {
			t.Errorf("nyx %s: %v %q", f, err, out)
		}
	}
}
