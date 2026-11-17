package control

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// With -log-dir, logs are read there, not under the data dir.
func TestServerLogDir(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(t.TempDir(), "tmpfs-logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	line := `{"stream":"stdout","log":"hello from tmpfs\n","time":"2026-11-17T00:00:00Z"}` + "\n"
	if err := os.WriteFile(filepath.Join(logDir, "web.log"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "s", "nyxd.sock")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(log, nil, nil, nil, nil, nil, "v1", "c", "d", dir, sock, "", "host", "auto")
	if got := s.containerLogDir(); got != filepath.Join(dir, "logs") {
		t.Fatalf("default log dir: %s", got)
	}
	s.SetLogDir(logDir)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	c := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}}}
	resp, err := c.Get("http://unix/v1/containers/web/logs")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), "hello from tmpfs") {
		t.Fatalf("logs from -log-dir: %d %s", resp.StatusCode, b)
	}
}
