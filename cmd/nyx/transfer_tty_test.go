package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/creack/pty"
)

// save and load refuse to write an archive to, or read one from, a terminal.
func TestTransferRefusesTerminal(t *testing.T) {
	ptm, tty, err := pty.Open()
	if err != nil {
		t.Skip(err)
	}
	defer ptm.Close()
	defer tty.Close()
	sock := stub(t, reply(200, "archive"))
	stdioMu.Lock()
	oldIn, oldOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = tty, tty
	errSave := run([]string{"-socket", sock, "save", "a:1"})
	errLoad := run([]string{"-socket", sock, "load"})
	os.Stdin, os.Stdout = oldIn, oldOut
	stdioMu.Unlock()
	if errSave == nil || !strings.Contains(errSave.Error(), "refusing to write an archive to a terminal") {
		t.Errorf("save to a terminal: %v", errSave)
	}
	if errLoad == nil || !strings.Contains(errLoad.Error(), "no archive on stdin") {
		t.Errorf("load from a terminal: %v", errLoad)
	}
}

func TestSaveToFileErrors(t *testing.T) {
	dir := t.TempDir()
	// The archive stream breaks off: no file is left behind.
	short := stub(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.Write([]byte("part")) //nolint:errcheck
	})
	out := filepath.Join(dir, "a.tar")
	if _, _, err := nyx(t, "-socket", short, "save", "-o", out, "a:1"); err == nil {
		t.Error("truncated archive saved")
	}
	if _, err := os.Stat(out + ".partial"); !os.IsNotExist(err) {
		t.Error("partial file left behind")
	}
	// The destination is a directory: the rename fails.
	full := stub(t, reply(200, "archive"))
	os.Mkdir(filepath.Join(dir, "d.tar"), 0o755)               //nolint:errcheck
	os.WriteFile(filepath.Join(dir, "d.tar", "x"), nil, 0o644) //nolint:errcheck
	if _, _, err := nyx(t, "-socket", full, "save", "--output="+filepath.Join(dir, "d.tar"), "a:1"); err == nil {
		t.Error("save over a directory")
	}
	if _, _, err := nyx(t, "-socket", stub(t, reply(200, "{\"phase\":\"done\",\"digest\":\"sha256:1\"}\n\n")), "push", "--password", "pw", "-u", "me", "a:1"); err != nil {
		t.Errorf("push with --password: %v", err)
	}
}
