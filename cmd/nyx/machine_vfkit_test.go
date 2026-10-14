package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestFakeVfkitProcess is not a test: run as a child (NYX_FAKE_VFKIT=1) it
// stands in for vfkit. It serves nyxd's API on the vsock socket and vfkit's
// REST API, and plays a container listening on the VM address 127.0.0.2. It
// shuts down on a connection to the guest's shutdown port (unless
// NYX_FAKE_NO_POWEROFF=1) or on the REST Stop, and writes which one to
// NYX_FAKE_VFKIT_STOPPED.
func TestFakeVfkitProcess(t *testing.T) {
	if os.Getenv("NYX_FAKE_VFKIT") != "1" {
		t.Skip("helper process")
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	var sock, shutdownSock, rest string
	for _, a := range args {
		for _, f := range strings.Split(a, ",") {
			if v, ok := strings.CutPrefix(f, "socketURL="); ok {
				if strings.Contains(a, "port=1025,") {
					shutdownSock = v
				} else {
					sock = v
				}
			}
		}
		if v, ok := strings.CutPrefix(a, "unix://"); ok {
			rest = v
		}
	}
	_ = os.WriteFile(os.Getenv("NYX_FAKE_VFKIT_ARGS"), []byte(strings.Join(args, "\n")), 0o600)
	port := os.Getenv("NYX_FAKE_VM_PORT")

	app, err := net.Listen("tcp", "127.0.0.2:"+port)
	if err != nil {
		os.Exit(3)
	}
	go func() {
		for {
			c, err := app.Accept()
			if err != nil {
				return
			}
			c.Write([]byte("hello from the VM")) //nolint:errcheck
			c.Close()
		}
	}()
	api, err := net.Listen("unix", sock)
	if err != nil {
		os.Exit(4)
	}
	go http.Serve(api, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { //nolint:errcheck
		switch r.URL.Path {
		case "/v1/ping":
			io.WriteString(w, `{"ok":true}`) //nolint:errcheck
		case "/v1/containers":
			fmt.Fprintf(w, `{"items":[{"id":"web","ports":"0.0.0.0:%s->80/tcp"}]}`, port)
		default:
			http.NotFound(w, r)
		}
	}))
	ctl, err := net.Listen("unix", rest)
	if err != nil {
		os.Exit(5)
	}
	stop := make(chan string, 2)
	go http.Serve(ctl, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { //nolint:errcheck
		var body struct{ State string }
		json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
		if r.URL.Path == "/vm/state" && body.State == "Stop" {
			stop <- "Stop"
		}
	}))
	if os.Getenv("NYX_FAKE_NO_POWEROFF") != "1" {
		off, err := net.Listen("unix", shutdownSock)
		if err != nil {
			os.Exit(6)
		}
		go func() {
			if c, err := off.Accept(); err == nil {
				c.Close()
				stop <- "vsock"
			}
		}()
	}
	how := <-stop
	_ = os.WriteFile(os.Getenv("NYX_FAKE_VFKIT_STOPPED"), []byte(how), 0o600)
	os.Remove(sock)
	os.Remove(shutdownSock)
	os.Exit(0)
}

// vmTarball builds a VM image tarball: a mostly-zero root disk keeps the
// sparse extraction honest.
func vmTarball(t *testing.T, path, cmdline string) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	root := make([]byte, 1<<20)
	copy(root[512<<10:], "superblock")
	for name, data := range map[string][]byte{
		"vmlinuz": []byte("kernel"), "initramfs": []byte("initrd"), "cmdline": []byte(cmdline + "\n"), "rootfs.img": root,
	} {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg}) //nolint:errcheck
		tw.Write(data)                                                                                      //nolint:errcheck
	}
	tw.Close()
	gz.Close()
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestVfkitMachineLifecycle(t *testing.T) {
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "cfg")
	t.Setenv("NYX_MACHINE_DRIVER", "vfkit")
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("HOME", tmp)

	// Fake vfkit (this test binary), launchctl and DHCP leases.
	exe, _ := os.Executable()
	fake := filepath.Join(tmp, "vfkit")
	os.WriteFile(fake, []byte("#!/bin/sh\nexec "+exe+" -test.run=^TestFakeVfkitProcess$ -- \"$@\"\n"), 0o755) //nolint:errcheck
	t.Setenv("NYX_VFKIT", fake)
	t.Setenv("NYX_FAKE_VFKIT", "1")
	argsFile := filepath.Join(tmp, "vfkit.args")
	t.Setenv("NYX_FAKE_VFKIT_ARGS", argsFile)
	stoppedFile := filepath.Join(tmp, "vfkit.stopped")
	t.Setenv("NYX_FAKE_VFKIT_STOPPED", stoppedFile)
	port := freeTCPPort(t)
	t.Setenv("NYX_FAKE_VM_PORT", strconv.Itoa(port))
	lctl := filepath.Join(tmp, "launchctl")
	lctlLog := filepath.Join(tmp, "launchctl.log")
	os.WriteFile(lctl, []byte("#!/bin/sh\necho \"$@\" >>"+lctlLog+"\n"), 0o755) //nolint:errcheck
	t.Setenv("NYX_LAUNCHCTL", lctl)

	// Errors before anything exists.
	if err := doMachine([]string{"init", "--image", filepath.Join(tmp, "x.tgz")}); err == nil {
		t.Error("init from a missing image")
	}
	old := version
	version = "dev"
	if err := doMachine([]string{"init"}); err == nil || !strings.Contains(err.Error(), "development build") {
		t.Errorf("dev init: %v", err)
	}
	version = "v9.9.9"
	t.Cleanup(func() { version = old })
	if err := doMachine([]string{"start"}); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("start before init: %v", err)
	}
	if err := doMachine([]string{"autostart", "maybe"}); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("autostart arg: %v", err)
	}
	commaImg := filepath.Join(tmp, "comma.tgz")
	vmTarball(t, commaImg, "a=1,2")

	img := filepath.Join(tmp, "vm.tgz")
	vmTarball(t, img, "console=hvc0 root=/dev/vda")
	if err := doMachine([]string{"init", "--image", img, "--cpus", "3", "--memory", "1024", "--disk-size", "2"}); err != nil {
		t.Fatal(err)
	}
	m := vfkitMachine{name: "nyx"}
	c, err := m.loadConfig()
	if err != nil || c.CPUs != 3 || c.MemoryMiB != 1024 || c.Version != "v9.9.9" || normMAC(c.MAC) == "" {
		t.Fatalf("config %+v %v", c, err)
	}
	if st, _ := os.Stat(m.path("data.img")); st.Size() != 2<<30 {
		t.Errorf("data disk size %d", st.Size())
	}
	var stat syscall.Stat_t
	if err := syscall.Stat(m.path("rootfs.img"), &stat); err != nil || stat.Blocks*512 >= 1<<20 {
		t.Errorf("root disk not sparse: %d bytes allocated (%v)", stat.Blocks*512, err)
	}
	if b, _ := os.ReadFile(m.path("rootfs.img")); len(b) != 1<<20 || !bytes.Contains(b, []byte("superblock")) {
		t.Errorf("root disk content (%d bytes)", len(b))
	}

	// The VM address comes from the leases, MAC written without leading zeros.
	hw, _ := net.ParseMAC(c.MAC)
	short := make([]string, len(hw))
	for i, b := range hw {
		short[i] = strconv.FormatUint(uint64(b), 16)
	}
	leases := filepath.Join(tmp, "leases")
	os.WriteFile(leases, []byte(fmt.Sprintf("{\n\tname=old\n\tip_address=192.168.64.9\n\thw_address=1,0:0:0:0:0:1\n}\n{\n\tname=nyx\n\tip_address=127.0.0.2\n\thw_address=1,%s\n}\n", strings.Join(short, ":"))), 0o600) //nolint:errcheck
	t.Setenv("NYX_DHCP_LEASES", leases)
	if ip := vmIP(c.MAC); ip != "127.0.0.2" {
		t.Fatalf("vmIP = %q", ip)
	}

	// run in the foreground (as launchd does), in this process.
	done := make(chan error, 1)
	go func() { done <- m.run() }()
	deadline := time.Now().Add(20 * time.Second)
	for !m.ping() {
		if time.Now().After(deadline) {
			t.Fatal("nyxd (fake) never answered on the machine socket")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if m.runPID() != os.Getpid() {
		t.Errorf("run.pid = %d", m.runPID())
	}
	args, _ := os.ReadFile(argsFile)
	for _, want := range []string{"--cpus\n3", "--memory\n1024", `cmdline="console=hvc0 root=/dev/vda"`, "virtio-blk,path=" + m.path("data.img"),
		"virtio-net,nat,mac=" + c.MAC, "socketURL=" + m.socket() + ",listen",
		"virtio-vsock,port=1025,socketURL=" + m.path("shutdown.sock") + ",listen", "virtio-fs,sharedDir=/Users,mountTag=users"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("vfkit args lack %q:\n%s", want, args)
		}
	}
	// The published port answers on localhost, forwarded to the VM.
	var got []byte
	for time.Now().Before(deadline) {
		if conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port))); err == nil {
			got, _ = io.ReadAll(conn)
			conn.Close()
			if len(got) > 0 {
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if string(got) != "hello from the VM" {
		t.Errorf("forwarded port: %q", got)
	}
	if err := doMachine([]string{"status"}); err != nil {
		t.Error(err)
	}
	if err := doMachine([]string{"start"}); err != nil { // already running
		t.Error(err)
	}
	// A second run does not start another VM. run() ignores its own pid in
	// run.pid (a stale file), so the first run poses as another process.
	ownPID, _ := os.ReadFile(m.path("run.pid"))
	os.WriteFile(m.path("run.pid"), []byte(strconv.Itoa(os.Getppid())), 0o600) //nolint:errcheck
	if err := m.run(); err != nil {
		t.Errorf("second run: %v", err)
	}
	os.WriteFile(m.path("run.pid"), ownPID, 0o600) //nolint:errcheck

	// Upgrade while running: stop, replace the system, keep data and settings.
	os.WriteFile(m.path("data.img"), []byte("precious"), 0o600) //nolint:errcheck
	growSparse(m.path("data.img"), 2<<30)                       //nolint:errcheck
	sigterm := make(chan struct{})
	go func() { // stop() signals this process: run() catches it
		<-sigterm
	}()
	if err := m.stop(false); err != nil {
		t.Fatal(err)
	}
	close(sigterm)
	if err := <-done; err != nil {
		t.Errorf("run after stop: %v", err)
	}
	if m.ping() || m.runPID() != 0 {
		t.Error("still running after stop")
	}
	if b, _ := os.ReadFile(stoppedFile); string(b) != "vsock" {
		t.Errorf("stopped by %q, want the vsock shutdown request", b)
	}
	if err := doMachine([]string{"stop"}); err != nil { // not running
		t.Error(err)
	}
	vmTarball(t, img, "console=hvc0 root=/dev/vda quiet")
	if err := doMachine([]string{"init", "--image", img}); err != nil {
		t.Fatal(err)
	}
	if f, err := os.Open(m.path("data.img")); err == nil {
		b := make([]byte, 8)
		io.ReadFull(f, b) //nolint:errcheck
		f.Close()
		if string(b) != "precious" {
			t.Error("upgrade lost the data disk")
		}
	}
	if st, _ := os.Stat(m.path("data.img")); st.Size() != 2<<30 {
		t.Errorf("upgrade resized the data disk to %d bytes", st.Size())
	}
	if c2, _ := m.loadConfig(); c2.MAC != c.MAC || c2.CPUs != 3 {
		t.Errorf("upgrade changed settings: %+v", c2)
	}
	if b, _ := os.ReadFile(m.path("cmdline")); !strings.Contains(string(b), "quiet") {
		t.Error("upgrade kept the old system")
	}

	// A guest that does not answer the shutdown request gets the power button.
	t.Setenv("NYX_FAKE_NO_POWEROFF", "1")
	go func() { done <- m.run() }()
	for deadline := time.Now().Add(20 * time.Second); !m.ping(); time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("second boot: nyxd (fake) never answered")
		}
	}
	if err := m.stop(false); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Errorf("run after stop: %v", err)
	}
	if b, _ := os.ReadFile(stoppedFile); string(b) != "Stop" {
		t.Errorf("stopped by %q, want the power button (vfkit Stop)", b)
	}
	if err := doMachine([]string{"init", "--image", commaImg}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.vfkitArgs(c); err == nil || !strings.Contains(err.Error(), "commas") {
		t.Errorf("comma in cmdline: %v", err)
	}

	// Autostart: a launchd agent running `nyx machine run`.
	if err := doMachine([]string{"autostart", "on"}); err != nil {
		t.Fatal(err)
	}
	pl, _ := m.plistPath()
	b, _ := os.ReadFile(pl)
	for _, want := range []string{"<string>io.github.zrougamed.nyx.machine</string>", "<string>machine</string>", "<string>run</string>", "<key>RunAtLoad</key>", "<key>SuccessfulExit</key>"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("plist lacks %s", want)
		}
	}
	if !m.autostartEnabled() {
		t.Error("autostart not enabled")
	}
	if err := doMachine([]string{"logs"}); err != nil {
		t.Error(err)
	}
	if err := doMachine([]string{"rm"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(pl); !os.IsNotExist(err) {
		t.Error("rm kept the launchd agent")
	}
	if d, _ := m.dir(); func() bool { _, err := os.Stat(d); return err == nil }() {
		t.Error("rm kept the machine directory")
	}
	lb, _ := os.ReadFile(lctlLog)
	if !strings.Contains(string(lb), "bootstrap gui/") || !strings.Contains(string(lb), "bootout gui/") {
		t.Errorf("launchctl calls:\n%s", lb)
	}
	if err := doMachine([]string{"status"}); err != nil {
		t.Error(err)
	}
}

func TestNormMACAndPlistEscape(t *testing.T) {
	if normMAC("2:a:0:ff:1:b") != "02:0a:00:ff:01:0b" || normMAC("zz:1") != "" || normMAC("1:2:3:4:5:zz") != "" {
		t.Error("normMAC")
	}
	if m := randomMAC(); normMAC(m) == "" || m[1]&0x2 == 0 && m[1] != '2' && m[1] != '6' && m[1] != 'a' && m[1] != 'e' {
		t.Errorf("randomMAC %s", m)
	}
	p := vfkitMachine{name: "dev"}.plist(`/Apps/a&b <x>/nyx`)
	if !strings.Contains(p, "a&amp;b &lt;x&gt;") || !strings.Contains(p, "nyx.machine.dev") {
		t.Errorf("plist escaping:\n%s", p)
	}
	t.Setenv("NYX_MACHINE_DRIVER", "")
	t.Setenv("NYX_WSL_EXE", "")
	if err := doMachine([]string{"run"}); err == nil {
		t.Error("run without the vfkit driver")
	}
}
