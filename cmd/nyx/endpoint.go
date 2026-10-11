package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/zrougamed/nyxd/internal/wslpath"
)

// Endpoints accepted by -socket / NYXD_SOCKET:
//
//	/run/nyxd/nyxd.sock, unix:///run/nyxd/nyxd.sock   Unix socket (Linux, macOS)
//	wsl://nyx                                         nyxd in the WSL distro "nyx" (Windows)
//
// A wsl:// endpoint runs `nyxd stdio-proxy` in the distro for each
// connection, so the daemon is reachable without any TCP listener.
const wslScheme = "wsl://"

// wslSocket is the daemon socket inside the nyx WSL distro.
const wslSocket = "/run/nyxd/nyxd.sock"

func isWSLEndpoint(endpoint string) bool { return strings.HasPrefix(endpoint, wslScheme) }

// dialEndpoint connects to the daemon control API.
func dialEndpoint(ctx context.Context, endpoint string) (net.Conn, error) {
	if isWSLEndpoint(endpoint) {
		return dialWSL(strings.TrimPrefix(endpoint, wslScheme))
	}
	var d net.Dialer
	return d.DialContext(ctx, "unix", strings.TrimPrefix(endpoint, "unix://"))
}

// wslExe is wsl.exe; NYX_WSL_EXE overrides it (tests, custom installs).
func wslExe() string {
	if v := os.Getenv("NYX_WSL_EXE"); v != "" {
		return v
	}
	return "wsl.exe"
}

func dialWSL(distro string) (net.Conn, error) {
	if distro == "" {
		return nil, errors.New("wsl:// endpoint needs a distro name, e.g. wsl://nyx")
	}
	cmd := exec.Command(wslExe(), "-d", distro, "-u", "root", "--exec",
		"/usr/local/bin/nyxd", "stdio-proxy", "-socket", wslSocket)
	hideWindow(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr strings.Builder
	cmd.Stderr = &limitedWriter{w: &stderr, n: 4096}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w (is WSL installed? see nyx machine init)", wslExe(), err)
	}
	return &pipeConn{cmd: cmd, r: stdout, w: stdin, stderr: &stderr}, nil
}

// pipeConn is a net.Conn over a child process's stdin/stdout.
type pipeConn struct {
	cmd    *exec.Cmd
	r      io.ReadCloser
	w      io.WriteCloser
	stderr *strings.Builder
	once   sync.Once
}

func (c *pipeConn) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if err == io.EOF && n == 0 && c.stderr.Len() > 0 {
		return 0, fmt.Errorf("nyxd (wsl): %s", strings.TrimSpace(c.stderr.String()))
	}
	return n, err
}

func (c *pipeConn) Write(p []byte) (int, error) { return c.w.Write(p) }

func (c *pipeConn) Close() error {
	c.once.Do(func() {
		_ = c.w.Close()
		done := make(chan struct{})
		go func() { _ = c.cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			_ = c.cmd.Process.Kill()
			<-done
		}
	})
	return nil
}

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "wsl" }

func (c *pipeConn) LocalAddr() net.Addr              { return pipeAddr{} }
func (c *pipeConn) RemoteAddr() net.Addr             { return pipeAddr{} }
func (c *pipeConn) SetDeadline(time.Time) error      { return nil }
func (c *pipeConn) SetReadDeadline(time.Time) error  { return nil }
func (c *pipeConn) SetWriteDeadline(time.Time) error { return nil }

type limitedWriter struct {
	w io.Writer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n <= 0 {
		return len(p), nil
	}
	q := p
	if len(q) > l.n {
		q = q[:l.n]
	}
	l.n -= len(q)
	_, _ = l.w.Write(q)
	return len(p), nil
}

// daemonPath converts a client-side path for the daemon. For a wsl://
// endpoint, Windows paths become their WSL automount form
// (C:\Users\me\app -> /mnt/c/Users/me/app); other paths are unchanged.
func daemonPath(endpoint, p string) (string, error) {
	if !isWSLEndpoint(endpoint) {
		return p, nil
	}
	return wslpath.ToWSL(p)
}
