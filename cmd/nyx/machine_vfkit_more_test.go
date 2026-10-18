package main

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestMain lets the test binary stand in for nyx: `nyx machine start` runs
// os.Executable() machine run, which is this binary under go test.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "machine" {
		if err := run(os.Args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "nyx: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// fakeVfkitEnv points nyx machine at a temporary home, the fake vfkit (this
// binary, TestFakeVfkitProcess) and a launchctl that logs its arguments and
// fails bootstrap when NYX_FAKE_LAUNCHCTL_FAIL is set. It returns the
// directory and the launchctl log.
func fakeVfkitEnv(t *testing.T) (string, string) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("NYX_MACHINE_DRIVER", "vfkit")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "cfg"))
	t.Setenv("HOME", tmp)
	exe, _ := os.Executable()
	fake := filepath.Join(tmp, "vfkit")
	os.WriteFile(fake, []byte("#!/bin/sh\nexec "+exe+" -test.run=^TestFakeVfkitProcess$ -- \"$@\"\n"), 0o755) //nolint:errcheck
	t.Setenv("NYX_VFKIT", fake)
	t.Setenv("NYX_FAKE_VFKIT", "1")
	t.Setenv("NYX_FAKE_VFKIT_ARGS", filepath.Join(tmp, "vfkit.args"))
	t.Setenv("NYX_FAKE_VFKIT_STOPPED", filepath.Join(tmp, "vfkit.stopped"))
	t.Setenv("NYX_FAKE_VM_PORT", strconv.Itoa(freeTCPPort(t)))
	t.Setenv("NYX_FAKE_NO_POWEROFF", "")
	t.Setenv("NYX_DHCP_LEASES", filepath.Join(tmp, "no-leases"))
	lctlLog := filepath.Join(tmp, "launchctl.log")
	lctl := filepath.Join(tmp, "launchctl")
	os.WriteFile(lctl, []byte("#!/bin/sh\necho \"$@\" >>"+lctlLog+"\n"+ //nolint:errcheck
		"[ \"$1\" = bootstrap ] && [ -n \"$NYX_FAKE_LAUNCHCTL_FAIL\" ] && { echo 'Bootstrap failed: 5' >&2; exit 5; }\nexit 0\n"), 0o755)
	t.Setenv("NYX_LAUNCHCTL", lctl)
	t.Setenv("NYX_FAKE_LAUNCHCTL_FAIL", "")
	return tmp, lctlLog
}

// TestVfkitStartSpawnsRun covers start in the background (a detached
// `nyx machine run`), an upgrade of a running machine and start under launchd.
func TestVfkitStartSpawnsRun(t *testing.T) {
	tmp, lctlLog := fakeVfkitEnv(t)
	old := version
	version = "v1.0.0"
	t.Cleanup(func() { version = old })
	img := filepath.Join(tmp, "vm.tgz")
	vmTarball(t, img, "console=hvc0")
	m := vfkitMachine{name: "bg"}
	if err := doMachine([]string{"init", "--name", "bg", "--image", img, "--disk-size", "1"}); err != nil {
		t.Fatal(err)
	}
	if err := doMachine([]string{"status", "--name", "bg"}); err != nil { // created, not running
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.stop(false) })
	if err := doMachine([]string{"start", "--name", "bg"}); err != nil {
		t.Fatal(err)
	}
	if pid := m.runPID(); pid == 0 || pid == os.Getpid() {
		t.Fatalf("run.pid = %d, want the detached nyx machine run", pid)
	}

	// Upgrade while running: stopped, upgraded, started again.
	vmTarball(t, img, "console=hvc0 quiet")
	if err := doMachine([]string{"init", "--name", "bg", "--image", img}); err != nil {
		t.Fatal(err)
	}
	if !m.ping() {
		t.Fatal("not running after the upgrade")
	}
	if b, _ := os.ReadFile(m.path("cmdline")); !strings.Contains(string(b), "quiet") {
		t.Error("upgrade kept the old system")
	}
	if err := doMachine([]string{"autostart", "--name", "bg", "on"}); err != nil {
		t.Fatal(err)
	}
	out, _, err := nyx(t, "machine", "status", "--name", "bg")
	if err != nil || !strings.Contains(out, "autostart: at login") || !strings.Contains(out, ": running, ") {
		t.Errorf("status: %v\n%s", err, out)
	}
	if err := m.stop(false); err != nil {
		t.Fatal(err)
	}

	// With autostart on, start asks launchd first; this launchctl does not
	// start anything, so nyx starts the machine itself.
	if err := m.start(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(lctlLog); !strings.Contains(string(b), "kickstart gui/") {
		t.Errorf("launchctl calls:\n%s", b)
	}
	out, _, _ = nyx(t, "machine", "logs", "--name", "bg")
	if !strings.Contains(out, "run.log") {
		t.Errorf("logs lack run.log:\n%s", out)
	}
	if err := doMachine([]string{"rm", "--name", "bg"}); err != nil {
		t.Fatal(err)
	}
}

func TestVfkitRunErrors(t *testing.T) {
	tmp, _ := fakeVfkitEnv(t)
	m := vfkitMachine{name: "r"}
	if err := m.run(); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("run before init: %v", err)
	}
	dir, _ := m.dir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := m.saveConfig(vmConfig{CPUs: 1, MemoryMiB: 64, MAC: "02:00:00:00:00:01"}); err != nil {
		t.Fatal(err)
	}
	if err := m.run(); err == nil { // no cmdline: not an initialized machine
		t.Error("run without a VM image")
	}
	os.WriteFile(m.path("cmdline"), []byte("console=hvc0\n"), 0o600) //nolint:errcheck

	os.Mkdir(m.path("vfkit.log"), 0o700) //nolint:errcheck
	if err := m.run(); err == nil {
		t.Error("run with an unwritable vfkit.log")
	}
	os.Remove(m.path("vfkit.log"))

	t.Setenv("NYX_VFKIT", filepath.Join(tmp, "no-such-vfkit"))
	if err := m.run(); err == nil || !strings.Contains(err.Error(), "start vfkit") {
		t.Errorf("missing vfkit: %v", err)
	}
	f := filepath.Join(tmp, "exits")
	os.WriteFile(f, []byte("#!/bin/sh\nexit 2\n"), 0o755) //nolint:errcheck
	t.Setenv("NYX_VFKIT", f)
	if err := m.run(); err == nil || !strings.Contains(err.Error(), "stopped on its own") {
		t.Errorf("VM that exits: %v", err)
	}

	os.Mkdir(m.path("run.pid"), 0o700) //nolint:errcheck
	if err := m.run(); err == nil {
		t.Error("run with an unwritable run.pid")
	}
	os.Remove(m.path("run.pid"))

	// run.pid that names no live process is stale.
	for _, s := range []string{"junk", "-4", "999999999"} {
		os.WriteFile(m.path("run.pid"), []byte(s), 0o600) //nolint:errcheck
		if pid := m.runPID(); pid != 0 {
			t.Errorf("runPID(%q) = %d", s, pid)
		}
	}
	// A socket file nobody serves.
	os.WriteFile(m.socket(), nil, 0o600) //nolint:errcheck
	if m.ping() {
		t.Error("ping on a dead socket")
	}
	if err := doMachine([]string{"status", "--name", "r"}); err != nil {
		t.Error(err)
	}
}

// A guest that ignores the shutdown request and the power button is killed.
func TestVfkitShutdownForcesKill(t *testing.T) {
	m := vfkitMachine{name: "gone"}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // no shutdown.sock, no vfkit.sock
	vm := exec.Command("sleep", "60")
	if err := vm.Start(); err != nil {
		t.Skip(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- vm.Wait() }()
	start := time.Now()
	if err := m.shutdownVM(vm, exited); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 30*time.Second {
		t.Errorf("shutdown waited %s for a guest it could not reach", time.Since(start))
	}
	if vm.ProcessState == nil {
		t.Error("vfkit not reaped")
	}
}

func TestVfkitInitErrors(t *testing.T) {
	tmp, _ := fakeVfkitEnv(t)
	img := filepath.Join(tmp, "vm.tgz")
	vmTarball(t, img, "console=hvc0")
	m := vfkitMachine{name: "nyx"}
	initErr := func() error { return m.init(machineOpts{image: img, diskSize: 1}) }

	t.Setenv("NYX_VFKIT", "nyx-no-such-vfkit")
	if err := initErr(); err == nil || !strings.Contains(err.Error(), "brew install vfkit") {
		t.Errorf("no vfkit: %v", err)
	}
	t.Setenv("NYX_VFKIT", "/bin/true")

	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	if err := initErr(); err == nil {
		t.Error("init without a config directory")
	}
	if darwinMachineSocket() != "" || m.autostartEnabled() {
		t.Error("no home: no socket, no autostart")
	}
	if err := m.remove(); err == nil {
		t.Error("remove without a config directory")
	}
	t.Setenv("HOME", tmp)

	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, strings.Repeat("d", 120)))
	if err := initErr(); err == nil || !strings.Contains(err.Error(), "too deep") {
		t.Errorf("deep directory: %v", err)
	}
	file := filepath.Join(tmp, "file")
	os.WriteFile(file, nil, 0o600) //nolint:errcheck
	t.Setenv("XDG_CONFIG_HOME", file)
	if err := initErr(); err == nil {
		t.Error("init under a file")
	}

	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "cfg"))
	dir, _ := m.dir()
	os.MkdirAll(filepath.Join(dir, "data.img"), 0o700) //nolint:errcheck
	if err := initErr(); err == nil {
		t.Error("init with a directory as data disk")
	}
	os.Remove(filepath.Join(dir, "data.img"))
	os.Mkdir(m.path("config.json"), 0o700) //nolint:errcheck
	if _, err := m.loadConfig(); err == nil || strings.Contains(err.Error(), "does not exist") {
		t.Errorf("unreadable config: %v", err)
	}
	if err := initErr(); err == nil {
		t.Error("init that cannot save its config")
	}
	os.Remove(m.path("config.json"))

	if _, err := (vfkitMachine{name: "none"}).vfkitArgs(vmConfig{}); err == nil {
		t.Error("vfkitArgs without cmdline")
	}
	if err := writeSparse(filepath.Join(tmp, "no", "dir"), strings.NewReader("x")); err == nil {
		t.Error("writeSparse into a missing directory")
	}
	if err := growSparse(filepath.Join(tmp, "no", "dir"), 1); err == nil {
		t.Error("growSparse into a missing directory")
	}
	if err := writeSparse(filepath.Join(tmp, "w"), iotestErrReader{}); err == nil {
		t.Error("writeSparse read error")
	}
}

type iotestErrReader struct{}

func (iotestErrReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestExtractVMImageErrors(t *testing.T) {
	tmp := t.TempDir()
	tarOf := func(files map[string]string, extra ...*tar.Header) string {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		for _, h := range extra {
			tw.WriteHeader(h) //nolint:errcheck
		}
		for name, data := range files {
			tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg}) //nolint:errcheck
			tw.Write([]byte(data))                                                                              //nolint:errcheck
		}
		tw.Close()
		p := filepath.Join(tmp, fmt.Sprintf("img%d.tar", len(files)+len(extra)))
		os.WriteFile(p, buf.Bytes(), 0o600) //nolint:errcheck
		return p
	}
	out := filepath.Join(tmp, "out")
	os.Mkdir(out, 0o700) //nolint:errcheck

	if err := extractVMImage(filepath.Join(tmp, "missing.tar"), out); err == nil {
		t.Error("missing tarball")
	}
	junk := filepath.Join(tmp, "junk.tar")
	os.WriteFile(junk, bytes.Repeat([]byte("x"), 1024), 0o600) //nolint:errcheck
	if err := extractVMImage(junk, out); err == nil || !strings.Contains(err.Error(), "VM image") {
		t.Errorf("junk tarball: %v", err)
	}
	// Unknown files and directories are skipped; a missing file is an error.
	p := tarOf(map[string]string{"README": "hi", "vmlinuz": "k"}, &tar.Header{Name: "boot/", Typeflag: tar.TypeDir, Mode: 0o755})
	if err := extractVMImage(p, out); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Errorf("incomplete image: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "README")); err == nil {
		t.Error("extracted a file that is not part of the image")
	}
	if err := extractVMImage(p, filepath.Join(tmp, "no-dir")); err == nil {
		t.Error("extract into a missing directory")
	}
	// A directory in the way of a file.
	os.Remove(filepath.Join(out, "vmlinuz"))
	os.MkdirAll(filepath.Join(out, "vmlinuz", "sub"), 0o700) //nolint:errcheck
	if err := extractVMImage(p, out); err == nil {
		t.Error("extract over a directory")
	}
}

func TestExtractVMImageGNUSparse(t *testing.T) {
	if _, err := exec.LookPath("tar"); err != nil {
		t.Skip("tar not available")
	}
	// GNU tar --sparse emits TypeGNUSparse for mostly-zero files; that is how
	// release nyx-vm-*.tar.gz packages rootfs.img.
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src")
	os.Mkdir(src, 0o700) //nolint:errcheck
	for name, data := range map[string][]byte{
		"vmlinuz": []byte("kernel"), "initramfs": []byte("initrd"), "cmdline": []byte("console=hvc0\n"),
	} {
		if err := os.WriteFile(filepath.Join(src, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	root := filepath.Join(src, "rootfs.img")
	f, err := os.Create(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(1 << 20); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte("superblock"), 512<<10); err != nil {
		t.Fatal(err)
	}
	f.Close()
	archive := filepath.Join(tmp, "vm.tar")
	cmd := exec.Command("tar", "--sparse", "-C", src, "-cf", archive, "vmlinuz", "initramfs", "rootfs.img", "cmdline")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("tar --sparse: %v (%s)", err, out)
	}
	// Confirm the archive really used the GNU sparse type (skip if this tar cannot).
	af, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(af)
	var sawSparse bool
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if filepath.Base(h.Name) == "rootfs.img" && h.Typeflag == tar.TypeGNUSparse {
			sawSparse = true
		}
	}
	af.Close()
	if !sawSparse {
		t.Skip("tar did not emit GNU sparse rootfs.img")
	}
	out := filepath.Join(tmp, "out")
	os.Mkdir(out, 0o700) //nolint:errcheck
	if err := extractVMImage(archive, out); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(out, "rootfs.img"))
	if err != nil || len(b) != 1<<20 || !bytes.Contains(b, []byte("superblock")) {
		t.Fatalf("rootfs content: len=%d err=%v", len(b), err)
	}
}

func TestVfkitDefaults(t *testing.T) {
	t.Setenv("NYX_VFKIT", "")
	t.Setenv("NYX_LAUNCHCTL", "")
	t.Setenv("NYX_DHCP_LEASES", "")
	if vfkitExe() != "vfkit" || launchctl() != "launchctl" || dhcpLeases() != "/var/db/dhcpd_leases" {
		t.Error("defaults")
	}
}

func TestVfkitAutostartErrors(t *testing.T) {
	tmp, _ := fakeVfkitEnv(t)
	m := vfkitMachine{name: "nyx"}
	if darwinMachineSocket() != "" {
		t.Error("socket before init")
	}
	dir, _ := m.dir()
	os.MkdirAll(dir, 0o700)              //nolint:errcheck
	os.WriteFile(m.socket(), nil, 0o600) //nolint:errcheck
	if got := darwinMachineSocket(); got != m.socket() {
		t.Errorf("darwinMachineSocket = %q", got)
	}
	if vmIP("02:00:00:00:00:01") != "" {
		t.Error("vmIP without leases")
	}

	if err := m.autostart(true); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("autostart before init: %v", err)
	}
	m.saveConfig(vmConfig{CPUs: 1}) //nolint:errcheck
	pl, _ := m.plistPath()
	t.Setenv("NYX_FAKE_LAUNCHCTL_FAIL", "1")
	if err := m.autostart(true); err == nil || !strings.Contains(err.Error(), "Bootstrap failed") {
		t.Errorf("bootstrap failure: %v", err)
	}
	t.Setenv("NYX_FAKE_LAUNCHCTL_FAIL", "")
	os.Remove(pl)
	os.MkdirAll(filepath.Join(pl, "x"), 0o700) //nolint:errcheck
	if err := m.autostart(true); err == nil {
		t.Error("autostart with a directory as plist")
	}
	if err := m.autostart(false); err == nil {
		t.Error("autostart off that cannot remove the plist")
	}
	os.RemoveAll(filepath.Join(tmp, "Library"))
	os.WriteFile(filepath.Join(tmp, "Library"), nil, 0o600) //nolint:errcheck
	if err := m.autostart(true); err == nil {
		t.Error("autostart without a LaunchAgents directory")
	}
	os.Remove(filepath.Join(tmp, "Library"))
	if err := m.autostart(false); err != nil { // nothing to remove
		t.Errorf("autostart off: %v", err)
	}
}

func TestPortProxySync(t *testing.T) {
	var body atomic.Value
	body.Store("{")
	sock := stub(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body.Load().(string)) }) //nolint:errcheck
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	busyPort := busy.Addr().(*net.TCPAddr).Port
	// Two distinct free ports: hold both before releasing them.
	la, _ := net.Listen("tcp", "127.0.0.1:0")
	lb, _ := net.Listen("tcp", "127.0.0.1:0")
	a, b := la.Addr().(*net.TCPAddr).Port, lb.Addr().(*net.TCPAddr).Port
	la.Close()
	lb.Close()
	ip := atomic.Value{}
	ip.Store("")
	p := newPortProxy(sock, func() string { return ip.Load().(string) })
	defer p.closeAll()

	if _, err := p.publishedPorts(); err == nil {
		t.Error("bad JSON from nyxd")
	}
	p.sync() // nyxd not answering properly: nothing forwarded
	if len(p.forwarded()) != 0 {
		t.Error("forwarded on a bad answer")
	}
	items := func(ports ...int) string {
		var s []string
		for _, n := range ports {
			s = append(s, fmt.Sprintf(`{"ports":"0.0.0.0:%d->80/tcp, 0.0.0.0:%d->53/udp"}`, n, n+1))
		}
		return `{"items":[` + strings.Join(s, ",") + `]}`
	}
	body.Store(items(a, b, busyPort))
	_, errOut, _ := capture(t, "", func() error { p.sync(); p.sync(); return nil })
	if got := p.forwarded(); len(got) != 2 {
		t.Errorf("forwarded %v, want %d and %d", got, a, b)
	}
	if !strings.Contains(errOut, "cannot forward localhost:"+strconv.Itoa(busyPort)) {
		t.Errorf("busy port not reported: %q", errOut)
	}
	body.Store(items(a))
	p.sync()
	if got := p.forwarded(); len(got) != 1 || got[0] != a {
		t.Errorf("after unpublish: %v", got)
	}

	// No VM address yet, then an address where nothing listens: the
	// connection is closed.
	for _, addr := range []string{"", "127.0.0.3"} {
		ip.Store(addr)
		c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(a)))
		if err != nil {
			t.Fatal(err)
		}
		c.SetReadDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck
		if n, _ := c.Read(make([]byte, 1)); n != 0 {
			t.Errorf("%q: read data from nowhere", addr)
		}
		c.Close()
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.loop(ctx); close(done) }()
	cancel()
	<-done
}

// relay closes a connection that cannot half-close once its side is done.
func TestRelayPipe(t *testing.T) {
	a1, a2 := net.Pipe()
	b1, b2 := net.Pipe()
	done := make(chan struct{})
	go func() { relay(a2, b1); close(done) }()
	go func() { a1.Write([]byte("ping")); a1.Close() }() //nolint:errcheck
	got, _ := io.ReadAll(b2)
	b2.Close()
	<-done
	if string(got) != "ping" {
		t.Errorf("relayed %q", got)
	}
}
