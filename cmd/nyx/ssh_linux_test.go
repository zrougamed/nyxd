//go:build linux

package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestSSHEndpointRealSSHD runs nyx against a stub daemon through a real,
// unprivileged sshd on a random local port with throwaway keys. Skipped when
// sshd or ssh-keygen is missing or sshd will not start.
func TestSSHEndpointRealSSHD(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	sshd, err := exec.LookPath("sshd")
	if err != nil {
		sshd = "/usr/sbin/sshd"
	}
	if _, err := os.Stat(sshd); err != nil {
		t.Skip("no sshd")
	}
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("no ssh-keygen")
	}
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("no ssh")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-o", filepath.Join(bin, "nyxd"), "../nyxd").CombinedOutput(); err != nil {
		t.Fatalf("build nyxd: %v\n%s", err, out)
	}
	for _, k := range []string{"host", "client"} {
		if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", filepath.Join(dir, k)).CombinedOutput(); err != nil {
			t.Fatalf("ssh-keygen: %v\n%s", err, out)
		}
	}
	pub, _ := os.ReadFile(filepath.Join(dir, "client.pub"))
	if err := os.WriteFile(filepath.Join(dir, "authorized_keys"), pub, 0o600); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	cfg := fmt.Sprintf(`Port %d
ListenAddress 127.0.0.1
HostKey %s
AuthorizedKeysFile %s
PidFile %s
StrictModes no
UsePAM no
PasswordAuthentication no
KbdInteractiveAuthentication no
SetEnv PATH=%s:/usr/bin:/bin
`, port, filepath.Join(dir, "host"), filepath.Join(dir, "authorized_keys"), filepath.Join(dir, "sshd.pid"), bin)
	if err := os.WriteFile(filepath.Join(dir, "sshd_config"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	// Its own process group, logging to a file: the per-connection
	// sshd-session children outlive a kill of sshd alone and would keep a
	// stderr pipe (and Wait) open.
	logPath := filepath.Join(dir, "sshd.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	srv := exec.Command(sshd, "-D", "-e", "-f", filepath.Join(dir, "sshd_config"))
	srv.Stderr = logFile
	srv.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := srv.Start(); err != nil {
		t.Skipf("start sshd: %v", err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-srv.Process.Pid, syscall.SIGKILL); _ = srv.Wait() })
	srvLog := func() string { b, _ := os.ReadFile(logPath); return string(b) }
	up := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
			c.Close()
			up = true
			break
		}
	}
	if !up {
		t.Skipf("sshd did not start: %s", srvLog())
	}

	// The client side: only the throwaway key and known_hosts, nothing from ~/.ssh.
	clientCfg := filepath.Join(dir, "ssh_config")
	if err := os.WriteFile(clientCfg, []byte(fmt.Sprintf(`Host *
  IdentityFile %s
  IdentitiesOnly yes
  UserKnownHostsFile %s
  StrictHostKeyChecking accept-new
  BatchMode yes
`, filepath.Join(dir, "client"), filepath.Join(dir, "known_hosts"))), 0o600); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(dir, "ssh")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexec ssh -F '"+clientCfg+"' \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NYX_SSH", wrapper)

	sock := stub(t, routes(map[string]http.HandlerFunc{
		"/v1/ping":    reply(200, `{"ok":true}`),
		"/v1/version": reply(200, `{"version":"v9.9.9","commit":"abc12345","daemon":"nyxd"}`),
	}))
	user := os.Getenv("USER")
	if user == "" {
		t.Skip("no USER")
	}
	endpoint := fmt.Sprintf("ssh://%s@127.0.0.1:%d%s", user, port, sock)
	out, _, err := capture(t, "", func() error { return run([]string{"--host", endpoint, "version"}) })
	if err != nil || !strings.Contains(out, `"version":"v9.9.9"`) {
		t.Fatalf("nyx version over ssh: %q %v\nsshd: %s", out, err, srvLog())
	}
}
