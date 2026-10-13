//go:build linux

package main

import (
	"bytes"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zrougamed/nyxd/internal/image"
	"github.com/zrougamed/nyxd/internal/testutil/daemon"
	"github.com/zrougamed/nyxd/internal/testutil/nsrun"
	"github.com/zrougamed/nyxd/internal/testutil/registry"
	"github.com/zrougamed/nyxd/pkg/oci"
)

var stdioMu sync.Mutex

// capture runs fn with os.Stdin fed from stdin and os.Stdout/os.Stderr
// captured, as the nyx commands write to them directly.
func capture(t *testing.T, stdin string, fn func() error) (string, string, error) {
	t.Helper()
	stdioMu.Lock()
	defer stdioMu.Unlock()
	oldIn, oldOut, oldErr := os.Stdin, os.Stdout, os.Stderr
	inR, inW, _ := os.Pipe()
	outR, outW, _ := os.Pipe()
	errR, errW, _ := os.Pipe()
	go func() { _, _ = io.WriteString(inW, stdin); inW.Close() }()
	os.Stdin, os.Stdout, os.Stderr = inR, outW, errW
	var ob, eb bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = io.Copy(&ob, outR) }()
	go func() { defer wg.Done(); _, _ = io.Copy(&eb, errR) }()
	err := fn()
	outW.Close()
	errW.Close()
	wg.Wait()
	os.Stdin, os.Stdout, os.Stderr = oldIn, oldOut, oldErr
	inR.Close()
	return ob.String(), eb.String(), err
}

func nyx(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	return capture(t, "", func() error { return run(args) })
}

func nyxIn(t *testing.T, stdin string, args ...string) (string, string, error) {
	t.Helper()
	return capture(t, stdin, func() error { return run(args) })
}

// ok runs nyx and fails the test on error; it returns stdout.
func ok(t *testing.T, args ...string) string {
	t.Helper()
	out, errOut, err := nyx(t, args...)
	if err != nil {
		t.Fatalf("nyx %s: %v\nstdout: %s\nstderr: %s", strings.Join(args, " "), err, out, errOut)
	}
	return out
}

// fails runs nyx and expects an error mentioning want.
func fails(t *testing.T, want string, args ...string) {
	t.Helper()
	out, errOut, err := nyx(t, args...)
	if err == nil || !strings.Contains(err.Error()+errOut, want) {
		t.Errorf("nyx %s: err=%v, want %q\nstdout: %s\nstderr: %s", strings.Join(args, " "), err, want, out, errOut)
	}
}

func TestCLIAgainstDaemon(t *testing.T) {
	if !nsrun.Run(t) {
		return
	}
	d := daemon.Start(t)
	d.Image(t, "test/app:1", nil)
	t.Setenv("NYXD_SOCKET", d.Socket)

	// Global flags and dispatch.
	if out := ok(t, "ping"); !strings.Contains(out, "true") {
		t.Errorf("ping: %q", out)
	}
	ok(t, "-socket", d.Socket, "ping")
	ok(t, "-socket="+d.Socket, "ping")
	if out := ok(t, "version"); !strings.Contains(out, "abc1234") {
		t.Errorf("version: %q", out)
	}
	if _, errOut, err := nyx(t, "--help"); err != nil || !strings.Contains(errOut, "usage:") {
		t.Errorf("--help: %v %q", err, errOut)
	}
	fails(t, "no command")
	fails(t, "unknown flag", "--bogus", "ping")
	fails(t, "unknown command", "frobnicate")
	fails(t, "usage: nyx stop", "stop")

	// run: detached, JSON, attached.
	if out := ok(t, "run", "-d", "--name", "a1", "-e", "K=V", "--env=L=W", "--hostname", "h1", "--restart", "no",
		"-p", "18080:18081", "--publish=19090:19091", "test/app:1", "sleep", "300"); strings.TrimSpace(out) != "a1" {
		t.Errorf("run -d: %q", out)
	}
	if out := ok(t, "run", "-d", "--json", "--name=a2", "--hostname=h2", "--restart=no", "-e=X=1", "-p=18001:18002", "test/app:1", "--", "sleep", "300"); !strings.Contains(out, `"id":"a2"`) {
		t.Errorf("run -d --json: %q", out)
	}
	out, errOut, err := nyx(t, "run", "--name", "fg", "--print-id", "-h", "fgh", "test/app:1", "sh", "-c", "echo hi; hostname")
	if err != nil || !strings.Contains(out, "hi") || !strings.Contains(out, "fgh") || !strings.Contains(errOut, "fg") {
		t.Errorf("attached run: %v %q %q", err, out, errOut)
	}
	if out := ok(t, "run", "--json", "--name", "fgj", "test/app:1", "echo", "json-mode"); !strings.Contains(out, "json-mode") {
		t.Errorf("attached --json run: %q", out)
	}
	fails(t, "could not pull", "run", "-d", "registry.invalid/x:1")
	fails(t, "registry.invalid", "run", "registry.invalid/x:1")
	fails(t, "already running", "run", "-d", "--name", "a1", "test/app:1", "sleep", "1")

	// ps.
	if out := ok(t, "ps"); !strings.Contains(out, "test/app:1") || !strings.Contains(out, "18080->18081") {
		t.Errorf("ps: %q", out)
	}
	if out := ok(t, "ps", "--no-trunc"); !strings.Contains(out, "a1") {
		t.Errorf("ps --no-trunc: %q", out)
	}
	if out := ok(t, "ps", "-q"); !strings.Contains(out, "\n") || strings.Contains(out, "IMAGE") {
		t.Errorf("ps -q: %q", out)
	}
	ok(t, "ps", "--no-trunc", "-a")
	ok(t, "container", "ls")
	ok(t, "container", "list", "-q")
	fails(t, "unknown ps flag", "ps", "--bogus")
	fails(t, "unexpected argument", "ps", "extra")

	// exec.
	if out := ok(t, "exec", "a1", "echo", "exec-ok"); !strings.Contains(out, "exec-ok") {
		t.Errorf("exec: %q", out)
	}
	if out, _, err := nyxIn(t, "piped input\n", "exec", "-i", "a1", "cat"); err != nil || !strings.Contains(out, "piped input") {
		t.Errorf("exec -i: %v %q", err, out)
	}
	if _, errOut, _ := nyxIn(t, "echo from-sh\n", "exec", "-i", "a1", "sh"); !strings.Contains(errOut, "no PTY") {
		t.Errorf("exec -i sh hint: %q", errOut)
	}
	// nyx exec -i with a one-shot command while the terminal's stdin stays
	// open: returns when the command ends.
	inR, inW, _ := os.Pipe()
	execDone := make(chan string, 1)
	go func() {
		out, _, _ := capture(t, "", func() error {
			os.Stdin = inR
			return run([]string{"exec", "-i", "a1", "echo", "open-stdin"})
		})
		execDone <- out
	}()
	select {
	case out := <-execDone:
		if !strings.Contains(out, "open-stdin") {
			t.Errorf("exec -i with open stdin: %q", out)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("nyx exec -i did not return while stdin stayed open")
	}
	inW.Close()
	inR.Close()
	ok(t, "exec", "-t", "a1", "--", "echo", "tty")
	ok(t, "exec", "-it", "a1", "echo", "tty")
	ok(t, "exec", "-w", "/", "a1", "true")
	fails(t, "exit status", "exec", "a1", "false")
	fails(t, "", "exec", "a1", "bash") // missing shell: reported with a hint
	fails(t, "usage: nyx exec", "exec")
	fails(t, "unknown exec flag", "exec", "--bogus", "a1", "true")
	fails(t, "missing command", "exec", "a1")
	fails(t, "no matching container", "exec", "nosuch", "true")

	// logs.
	if out := ok(t, "logs", "fg"); !strings.Contains(out, "hi") {
		t.Errorf("logs: %q", out)
	}
	ok(t, "logs", "-f", "fg")
	ok(t, "logs", "--tail", "1", "fg")
	ok(t, "logs", "-n", "1", "fg")
	ok(t, "logs", "--tail=1", "fg")
	ok(t, "container", "logs", "fg")
	fails(t, "usage", "logs")
	fails(t, "no matching container", "logs", "never-ran")
	fails(t, "", "logs", "--tail", "x", "fg")

	// stop / rm.
	ok(t, "stop", "a1", "a2")
	fails(t, "no matching container", "stop", "nosuch")
	ok(t, "run", "-d", "--name", "r1", "test/app:1", "sleep", "300")
	ok(t, "run", "-d", "--name", "r2", "test/app:1", "sleep", "300")
	ok(t, "rm", "r1")
	ok(t, "container", "rm", "r2")
	fails(t, "no matching container", "rm", "nosuch")
	fails(t, "usage", "rm")
	fails(t, "", "container")
	fails(t, "", "container", "bogus")

	// Images, pull from a registry, tag, save, load, push.
	src, err := image.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := &oci.ImageConfig{OS: "linux", Architecture: runtime.GOARCH, Config: oci.ContainerConfig{Env: []string{"PATH=/bin"}, Cmd: []string{"echo", "from-registry"}}}
	for _, tag := range []string{"1", "2", "3", "4"} {
		c := *cfg
		if err := daemon.StoreImage(src, "lib/app:"+tag, &c, map[string]string{"/tag": tag}); err != nil {
			t.Fatal(err)
		}
	}
	reg := registry.New(src)
	defer reg.Close()
	if out, errOut, err := nyx(t, "pull", reg.Host+"/lib/app:1"); err != nil || !strings.Contains(out+errOut, "lib/app:1") {
		t.Errorf("pull: %v %q %q", err, out, errOut)
	}
	if out := ok(t, "pull", "--json", "-u", "user", "--password", "pw", reg.Host+"/lib/app:2"); !strings.Contains(out, `"ok":true`) {
		t.Errorf("pull --json: %q", out)
	}
	ok(t, "pull", "--username", "u", reg.Host+"/lib/app:1")
	fails(t, "", "pull", reg.Host+"/lib/nope:1")
	fails(t, "usage", "pull")
	fails(t, "usage", "pull", "-u")
	fails(t, "usage", "pull", "--password")
	fails(t, "usage", "pull", "-h")
	fails(t, "unknown flag", "pull", "--bogus", "x")
	fails(t, "unexpected extra", "pull", "a", "b")
	if out := ok(t, "run", "--name", "pr", reg.Host+"/lib/app:3"); !strings.Contains(out, "from-registry") {
		t.Errorf("run pulling: %q", out)
	}

	if out := ok(t, "image", "ls"); !strings.Contains(out, "test/app:1") {
		t.Errorf("image ls: %q", out)
	}
	ok(t, "tag", "test/app:1", "test/app:copy")
	fails(t, "usage", "tag", "only-one")
	fails(t, "", "tag", "nope:1", "x:1")
	archive := filepath.Join(t.TempDir(), "images.tar")
	ok(t, "save", "-o", archive, "test/app:copy")
	ok(t, "save", "--output="+filepath.Join(t.TempDir(), "x.tar"), "test/app:1")
	fails(t, "", "save")
	fails(t, "", "save", "-o", archive, "nope:1")
	ok(t, "image", "rm", "test/app:copy")
	if out := ok(t, "load", "-i", archive); !strings.Contains(out, "test/app:copy") {
		t.Errorf("load: %q", out)
	}
	b, _ := os.ReadFile(archive)
	if out, _, err := nyxIn(t, string(b), "load"); err != nil || !strings.Contains(out, "copy") {
		t.Errorf("load from stdin: %v %q", err, out)
	}
	fails(t, "", "load", "-i", "/nonexistent.tar")
	fails(t, "", "push", "test/app:1")                // docker.io: no network here
	fails(t, "", "push", "--username", "u", "nope:1") // unknown image
	fails(t, "usage", "push")
	ok(t, "image", "rm", reg.Host+"/lib/app:2")
	fails(t, "", "image", "rm", "nope:9")
	fails(t, "usage", "image", "rm")
	if out := ok(t, "image", "prune", "--dry-run"); !strings.Contains(out, "lib/app") {
		t.Errorf("prune dry run: %q", out)
	}
	ok(t, "image", "prune", "-n")
	ok(t, "image", "prune")
	d.Image(t, "test/app:1", nil) // pruned: nothing used it
	fails(t, "", "image", "prune", "--bogus")
	fails(t, "", "image")
	fails(t, "", "image", "bogus")

	// build.
	ctxDir := t.TempDir()
	write := func(name, data string) {
		if err := os.WriteFile(filepath.Join(ctxDir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Dockerfile", "FROM test/app:1\nARG MSG=none\nCOPY msg /msg\nRUN echo $MSG > /arg\nCMD [\"cat\", \"/msg\", \"/arg\"]\n")
	write("alt.Dockerfile", "FROM test/app:1 AS base\nRUN echo base > /b\nFROM base AS final\nRUN echo final > /f\n")
	write("msg", "built-msg\n")
	write("ignored.txt", "x")
	write(".dockerignore", "ignored.txt\n")
	if err := os.MkdirAll(filepath.Join(ctxDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	write("sub/file", "nested")
	if out, errOut, err := nyx(t, "build", "-t", "test/built:1", "--build-arg", "MSG=arg-ok", "--no-cache", ctxDir); err != nil {
		t.Fatalf("build: %v\n%s\n%s", err, out, errOut)
	}
	if out := ok(t, "build", "-q", "-f", filepath.Join(ctxDir, "alt.Dockerfile"), "--target", "base", "-t", "test/base:1", ctxDir); !strings.HasPrefix(strings.TrimSpace(out), "sha256:") {
		t.Errorf("build -q: %q", out)
	}
	if out := ok(t, "run", "--name", "bt", "test/built:1"); !strings.Contains(out, "built-msg") || !strings.Contains(out, "arg-ok") {
		t.Errorf("built image: %q", out)
	}
	write("bad.Dockerfile", "FROM test/app:1\nRUN false\n")
	fails(t, "", "build", "-f", "bad.Dockerfile", ctxDir)
	fails(t, "", "build")
	fails(t, "", "build", "/nonexistent-context")
	// --build-arg NAME takes the value from the environment (or is dropped).
	t.Setenv("MSG", "from-env")
	ok(t, "build", "-t", "test/env:1", "--build-arg", "MSG", "--build-arg", "UNSET_ARG", "--build-arg=X=1", ctxDir)
	if out := ok(t, "run", "--name", "envb", "test/env:1"); !strings.Contains(out, "from-env") {
		t.Errorf("build arg from env: %q", out)
	}
	fails(t, "requires a value", "build", "-t")
	fails(t, "only one build context", "build", ctxDir, ctxDir)
	fails(t, "", "build", "--bogus", ctxDir)

	// compose.
	cdir := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(cdir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfile := filepath.Join(cdir, "compose.yaml")
	if err := os.WriteFile(cfile, []byte("services:\n  s:\n    image: test/app:1\n    command: [sleep, '300']\n    volumes: [data:/data]\nvolumes:\n  data: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ok(t, "compose", "up", "-f", cfile)
	ok(t, "compose", "stop", "--file="+cfile)
	ok(t, "compose", "down", "-f="+cfile, "-v")
	ok(t, "compose", "up", "--file", cfile, "--project", "other")
	ok(t, "compose", "down", "-f", cfile, "--project=other", "--volumes")
	fails(t, "", "compose")
	fails(t, "", "compose", "bogus")
	fails(t, "requires a path", "compose", "up", "-f")
	fails(t, "--project requires", "compose", "up", "--project")
	fails(t, "unknown flag", "compose", "up", "--bogus")
	fails(t, "compose up does not take -v", "compose", "up", "-v")
	fails(t, "compose stop does not take -v", "compose", "stop", "-v")
	fails(t, "", "compose", "up", "-f", filepath.Join(cdir, "missing.yaml"))
	wd, _ := os.Getwd()
	if err := os.Chdir(cdir); err != nil {
		t.Fatal(err)
	}
	ok(t, "compose", "up")
	ok(t, "compose", "down")
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	fails(t, "", "compose", "up") // no compose file in cwd
	_ = os.Chdir(wd)

	// Attached run interrupted: the container is killed.
	done := make(chan error, 1)
	go func() {
		_, _, err := nyx(t, "run", "--name", "sig", "test/app:1", "sleep", "300")
		done <- err
	}()
	// (Not through nyx: the attached run holds the captured stdio.)
	daemon.WaitFor(t, 10*time.Second, "sig to run", func() bool {
		_, out := d.Do(t, "GET", "/v1/containers", nil)
		return strings.Contains(out, `"sig"`)
	})
	// A handler of our own: SIGINT must never take the default action here.
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt)
	defer signal.Stop(sigc)
	// nyx run catches SIGINT from the start of a foreground run, so one
	// signal is enough however far it has got.
	p, _ := os.FindProcess(os.Getpid())
	_ = p.Signal(os.Interrupt)
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("attached run did not end after SIGINT")
	}
	daemon.WaitFor(t, 10*time.Second, "sig to be killed", func() bool {
		_, out := d.Do(t, "GET", "/v1/containers", nil)
		return !strings.Contains(out, `"sig"`)
	})
}

// Every command against a daemon that is not there fails cleanly.
func TestCLIWithoutDaemon(t *testing.T) {
	t.Setenv("NYXD_SOCKET", filepath.Join(t.TempDir(), "nope.sock"))
	ctxDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(ctxDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644)
	cfile := filepath.Join(t.TempDir(), "compose.yaml")
	_ = os.WriteFile(cfile, []byte("services:\n  s:\n    image: x\n"), 0o644)
	archive := filepath.Join(t.TempDir(), "a.tar")
	_ = os.WriteFile(archive, []byte("x"), 0o644)
	for _, args := range [][]string{
		{"ping"}, {"version"}, {"pull", "x:1"}, {"pull", "--json", "x:1"}, {"run", "x:1"}, {"run", "-d", "x:1"},
		{"ps"}, {"rm", "x"}, {"logs", "x"}, {"logs", "-f", "x"}, {"stop", "x"}, {"exec", "x", "true"}, {"exec", "-i", "x", "cat"},
		{"image", "ls"}, {"image", "rm", "x"}, {"image", "prune"}, {"container", "ls"},
		{"compose", "up", "-f", cfile}, {"compose", "stop", "-f", cfile}, {"compose", "down", "-f", cfile},
		{"build", ctxDir}, {"tag", "a", "b"}, {"push", "a:1"}, {"save", "a:1"}, {"load", "-i", archive},
	} {
		if _, _, err := nyx(t, args...); err == nil {
			t.Errorf("nyx %s: no error without a daemon", strings.Join(args, " "))
		}
	}
}
