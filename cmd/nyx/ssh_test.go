package main

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestSSHCommand(t *testing.T) {
	for endpoint, want := range map[string][]string{
		"ssh://box":                    {"-T", "--", "box", "nyxd", "stdio-proxy"},
		"ssh://pi@raspberrypi.local":   {"-T", "--", "pi@raspberrypi.local", "nyxd", "stdio-proxy"},
		"ssh://pi@nas:2222/":           {"-T", "-p", "2222", "--", "pi@nas", "nyxd", "stdio-proxy"},
		"ssh://pi@[fe80::1]:22/run/x":  {"-T", "-p", "22", "--", "pi@fe80::1", "nyxd", "stdio-proxy", "-socket", "'/run/x'"},
		"ssh://box/run/user/1000/it's": {"-T", "--", "box", "nyxd", "stdio-proxy", "-socket", `'/run/user/1000/it'\''s'`},
	} {
		got, err := sshCommand(endpoint)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s:\n got %q %v\nwant %q", endpoint, got, err, want)
		}
	}
	for _, bad := range []string{
		"ssh://", "ssh:///run/nyxd.sock", "ssh://-oProxyCommand=x", "ssh://-l@box",
		"ssh://pi:secret@box", "ssh://box?x=1", "ssh://box#f", "ssh://%zz",
	} {
		if _, err := sshCommand(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestEndpointKinds(t *testing.T) {
	if !isSSHEndpoint("ssh://box") || isSSHEndpoint("/run/nyxd/nyxd.sock") || !isRemoteEndpoint("ssh://box") || isRemoteEndpoint("wsl://nyx") {
		t.Fatal("endpoint kinds")
	}
	t.Setenv("NYXD_SOCKET", "/tmp/a.sock")
	t.Setenv("NYX_HOST", "ssh://box")
	if got := defaultSocket(); got != "ssh://box" {
		t.Fatalf("NYX_HOST should win over NYXD_SOCKET: %s", got)
	}
	t.Setenv("NYX_HOST", "")
	if got := defaultSocket(); got != "/tmp/a.sock" {
		t.Fatalf("NYXD_SOCKET: %s", got)
	}
}

func TestComposeFileBodyRemote(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(f, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	body, err := composeFileBody("ssh://box", f)
	if err != nil || body["yaml"] != "services: {}\n" || body["context_dir"] != dir || body["file"] != nil {
		t.Fatalf("remote: %v %v", body, err)
	}
	body, err = composeFileBody("/run/nyxd/nyxd.sock", f)
	if err != nil || body["file"] != f || body["yaml"] != nil {
		t.Fatalf("local: %v %v", body, err)
	}
	if _, err := composeFileBody("ssh://box", filepath.Join(dir, "missing.yaml")); err == nil {
		t.Fatal("missing file over ssh")
	}
}

// TestSSHEndpointEndToEnd drives a stub daemon through a fake ssh that runs
// the command the way sshd does (through a shell) with the real nyxd
// stdio-proxy.
func TestSSHEndpointEndToEnd(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("needs a Unix socket and /bin/sh")
	}
	bin := t.TempDir()
	build := exec.Command("go", "build", "-o", filepath.Join(bin, "nyxd"), "../nyxd")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build nyxd: %v\n%s", err, out)
	}
	argsLog := filepath.Join(bin, "args")
	fake := filepath.Join(bin, "fake-ssh")
	script := `#!/bin/sh
printf '%s\n' "$*" > "` + argsLog + `"
while [ "$#" -gt 0 ] && [ "$1" != "--" ]; do shift; done
shift 2 # "--" and the destination
eval "$*"
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NYX_SSH", fake)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	sockDir := filepath.Join(t.TempDir(), "it's x")
	if err := os.Mkdir(sockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	var paths []string
	sock := stubAt(t, filepath.Join(sockDir, "d.sock"), func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	endpoint := "ssh://pi@box:2222" + sock
	if err := run([]string{"--host", endpoint, "ping"}); err != nil {
		t.Fatalf("ping over ssh: %v", err)
	}
	if len(paths) != 1 || paths[0] != "/v1/ping" {
		t.Fatalf("daemon saw %v", paths)
	}
	logged, _ := os.ReadFile(argsLog)
	if !strings.HasPrefix(string(logged), "-T -p 2222 -- pi@box nyxd stdio-proxy -socket '") {
		t.Fatalf("ssh args: %s", logged)
	}

	// The remote side failing comes back with its message.
	err := run([]string{"--host=ssh://box/nonexistent.sock", "ping"})
	if err == nil || !strings.Contains(err.Error(), "nyxd (ssh): nyxd stdio-proxy:") {
		t.Fatalf("remote failure: %v", err)
	}
	t.Setenv("NYX_SSH", filepath.Join(bin, "no-such-ssh"))
	if err := run([]string{"-H", "ssh://box", "ping"}); err == nil || !strings.Contains(err.Error(), "OpenSSH client") {
		t.Fatalf("missing ssh: %v", err)
	}
}
