package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
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
//	ssh://[user@]host[:port][/path/to/nyxd.sock]       nyxd on another machine, over SSH
//
// wsl:// and ssh:// endpoints run `nyxd stdio-proxy` next to the daemon for
// each connection and speak the API over its stdin/stdout, so the daemon is
// reachable without any TCP listener. ssh:// uses the system ssh binary with
// the user's keys, agent, known_hosts and ~/.ssh/config.
const (
	wslScheme = "wsl://"
	sshScheme = "ssh://"
)

// wslSocket is the daemon socket inside the nyx WSL distro.
const wslSocket = "/run/nyxd/nyxd.sock"

func isWSLEndpoint(endpoint string) bool { return strings.HasPrefix(endpoint, wslScheme) }

func isSSHEndpoint(endpoint string) bool { return strings.HasPrefix(endpoint, sshScheme) }

// isRemoteEndpoint reports whether the daemon runs on another machine, so it
// cannot read files by their client-side path.
func isRemoteEndpoint(endpoint string) bool { return isSSHEndpoint(endpoint) }

// dialEndpoint connects to the daemon control API.
func dialEndpoint(ctx context.Context, endpoint string) (net.Conn, error) {
	if isWSLEndpoint(endpoint) {
		return dialWSL(strings.TrimPrefix(endpoint, wslScheme))
	}
	if isSSHEndpoint(endpoint) {
		return dialSSH(endpoint)
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
	return &pipeConn{cmd: cmd, r: stdout, w: stdin, stderr: &stderr, label: "wsl"}, nil
}

// sshExe is the ssh client; NYX_SSH overrides it (tests, custom installs).
func sshExe() string {
	if v := os.Getenv("NYX_SSH"); v != "" {
		return v
	}
	return "ssh"
}

// sshCommand returns the ssh arguments that run `nyxd stdio-proxy` on the
// host named by an ssh:// endpoint. The path, if any, is the remote socket;
// without one, stdio-proxy picks the daemon the remote user would reach.
func sshCommand(endpoint string) ([]string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "ssh" || u.Hostname() == "" {
		return nil, fmt.Errorf("invalid ssh endpoint %q: want ssh://[user@]host[:port][/path/to/nyxd.sock]", endpoint)
	}
	if strings.HasPrefix(u.Hostname(), "-") || strings.HasPrefix(u.User.Username(), "-") {
		return nil, fmt.Errorf("invalid ssh endpoint %q: host and user cannot start with '-'", endpoint)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("invalid ssh endpoint %q: no query or fragment allowed", endpoint)
	}
	if _, set := u.User.Password(); set {
		return nil, fmt.Errorf("invalid ssh endpoint %q: passwords are not accepted in the URL (use keys or ssh-agent)", endpoint)
	}
	args := []string{"-T"}
	if p := u.Port(); p != "" {
		args = append(args, "-p", p)
	}
	dest := u.Hostname() // ssh takes IPv6 literals without brackets
	if name := u.User.Username(); name != "" {
		dest = name + "@" + dest
	}
	// "--" ends ssh's options: the destination cannot be read as one.
	args = append(args, "--", dest, "nyxd", "stdio-proxy")
	if u.Path != "" && u.Path != "/" {
		// The remote shell parses the command: quote the path.
		args = append(args, "-socket", shellQuote(u.Path))
	}
	return args, nil
}

// shellQuote quotes s for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func dialSSH(endpoint string) (net.Conn, error) {
	args, err := sshCommand(endpoint)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(sshExe(), args...)
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
		return nil, fmt.Errorf("start %s: %w (is an OpenSSH client installed?)", sshExe(), err)
	}
	return &pipeConn{cmd: cmd, r: stdout, w: stdin, stderr: &stderr, label: "ssh"}, nil
}

// pipeConn is a net.Conn over a child process's stdin/stdout.
type pipeConn struct {
	cmd    *exec.Cmd
	r      io.ReadCloser
	w      io.WriteCloser
	stderr *strings.Builder
	label  string // transport, for messages: wsl, ssh
	once   sync.Once
}

func (c *pipeConn) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if err == io.EOF && n == 0 && c.stderr.Len() > 0 {
		return 0, fmt.Errorf("nyxd (%s): %s", c.label, strings.TrimSpace(c.stderr.String()))
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

type pipeAddr string

func (pipeAddr) Network() string  { return "pipe" }
func (a pipeAddr) String() string { return string(a) }

func (c *pipeConn) LocalAddr() net.Addr              { return pipeAddr(c.label) }
func (c *pipeConn) RemoteAddr() net.Addr             { return pipeAddr(c.label) }
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
