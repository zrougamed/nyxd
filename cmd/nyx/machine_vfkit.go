package main

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// vfkitMachine runs nyxd in a small Linux VM on macOS, with vfkit (Apple
// Virtualization.framework; `brew install vfkit`). The VM image
// (scripts/build-vm-image.sh) boots from a system disk that upgrades replace
// and keeps nyxd's data on a second disk. The Mac's /Users is shared at the
// same path, nyxd's socket comes out of the VM over vsock as a Unix socket
// next to the disks, and `nyx machine run` forwards published container ports
// from localhost to the VM.
type vfkitMachine struct{ name string }

// vmConfig is config.json in the machine directory.
type vmConfig struct {
	CPUs      int    `json:"cpus"`
	MemoryMiB int    `json:"memory_mib"`
	MAC       string `json:"mac"`
	Version   string `json:"version"`
}

// useVfkit reports whether nyx machine drives vfkit: on macOS, or anywhere
// with NYX_MACHINE_DRIVER=vfkit (tests).
func useVfkit() bool {
	return runtime.GOOS == "darwin" || os.Getenv("NYX_MACHINE_DRIVER") == "vfkit"
}

// vfkitExe is vfkit; NYX_VFKIT overrides it (tests, custom installs).
func vfkitExe() string {
	if v := os.Getenv("NYX_VFKIT"); v != "" {
		return v
	}
	return "vfkit"
}

// launchctl is launchctl; NYX_LAUNCHCTL overrides it (tests).
func launchctl() string {
	if v := os.Getenv("NYX_LAUNCHCTL"); v != "" {
		return v
	}
	return "launchctl"
}

// machinesDir is ~/Library/Application Support/nyx/machines on macOS.
func machinesDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "nyx", "machines"), nil
}

// darwinMachineSocket is the default machine's nyxd socket, if it exists.
func darwinMachineSocket() string {
	d, err := machinesDir()
	if err != nil {
		return ""
	}
	p := filepath.Join(d, "nyx", "nyxd.sock")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

func (m vfkitMachine) dir() (string, error) {
	d, err := machinesDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, m.name), nil
}

func (m vfkitMachine) path(name string) string {
	d, _ := m.dir()
	return filepath.Join(d, name)
}

func (m vfkitMachine) socket() string { return m.path("nyxd.sock") }

func (m vfkitMachine) loadConfig() (vmConfig, error) {
	var c vmConfig
	b, err := os.ReadFile(m.path("config.json"))
	if errors.Is(err, os.ErrNotExist) {
		return c, fmt.Errorf("machine %q does not exist; create it with: nyx machine init%s", m.name, nameFlag(m.name))
	}
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(b, &c)
}

func (m vfkitMachine) saveConfig(c vmConfig) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.path("config.json"), append(b, '\n'), 0o600)
}

// machineOpts are the init flags.
type machineOpts struct {
	image    string
	cpus     int
	memory   int // MiB
	diskSize int // GiB
}

// init creates the machine, or upgrades its system disk (kernel, initramfs,
// root disk) when it exists: the data disk and settings are kept, and a
// running machine is restarted on the new system.
func (m vfkitMachine) init(o machineOpts) error {
	if _, err := exec.LookPath(vfkitExe()); err != nil {
		return errors.New("nyx machine needs vfkit on macOS: brew install vfkit")
	}
	dir, err := m.dir()
	if err != nil {
		return err
	}
	// macOS limits Unix socket paths to 104 bytes.
	if len(m.socket()) > 100 {
		return fmt.Errorf("machine directory %s is too deep for a Unix socket (macOS: 104 bytes)", dir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	image := o.image
	if image == "" {
		if version == "dev" {
			return errors.New("this nyx is a development build: pass --image with a VM image (scripts/build-vm-image.sh)")
		}
		image = fmt.Sprintf("%s/%s/nyx-vm-%s.tar.gz", releaseBase, version, runtime.GOARCH)
	}

	c, cfgErr := m.loadConfig()
	fresh := cfgErr != nil
	wasRunning := !fresh && m.ping()

	tarPath := filepath.Join(dir, "image.tar")
	defer os.Remove(tarPath)
	if err := fetchRootfs(image, tarPath); err != nil {
		return err
	}
	if wasRunning {
		fmt.Println("stopping the machine to upgrade it")
		if err := m.stop(false); err != nil {
			return err
		}
	}
	if err := extractVMImage(tarPath, dir); err != nil {
		return err
	}

	if fresh {
		c = vmConfig{CPUs: 2, MemoryMiB: 2048, MAC: randomMAC()}
		if n := runtime.NumCPU() / 2; n > c.CPUs {
			c.CPUs = n
		}
	}
	if o.cpus > 0 {
		c.CPUs = o.cpus
	}
	if o.memory > 0 {
		c.MemoryMiB = o.memory
	}
	c.Version = version
	data := filepath.Join(dir, "data.img")
	size := int64(o.diskSize) << 30
	st, statErr := os.Stat(data)
	if size <= 0 {
		size = 64 << 30 // a new disk; an upgrade keeps the current size
		if statErr == nil {
			size = st.Size()
		}
	}
	if statErr != nil || st.Size() < size {
		// Sparse: only what nyxd writes takes space. A new disk is formatted
		// on first boot, a grown one resized (nyx-datadisk in the VM).
		if err := growSparse(data, size); err != nil {
			return err
		}
	}
	if err := m.saveConfig(c); err != nil {
		return err
	}
	if fresh {
		fmt.Printf("created machine %q in %s (%d CPUs, %d MiB, %d GiB data disk)\n", m.name, dir, c.CPUs, c.MemoryMiB, size>>30)
		fmt.Printf("start it with: nyx machine start%s\n", nameFlag(m.name))
		return nil
	}
	fmt.Printf("upgraded machine %q to %s; images and containers are kept\n", m.name, version)
	if wasRunning {
		return m.start()
	}
	return nil
}

func growSparse(path string, size int64) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := f.Truncate(size); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// vmFiles are the files of a VM image tarball.
var vmFiles = map[string]bool{"vmlinuz": true, "initramfs": true, "rootfs.img": true, "cmdline": true}

// extractVMImage unpacks the image tarball into dir, keeping zero blocks as
// holes (the root disk is mostly empty).
func extractVMImage(tarPath, dir string) error {
	f, err := os.Open(tarPath)
	if err != nil {
		return err
	}
	defer f.Close()
	tr := tar.NewReader(f)
	seen := map[string]bool{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("VM image: %w", err)
		}
		name := filepath.Base(filepath.Clean(h.Name))
		if !vmFiles[name] || h.Typeflag != tar.TypeReg {
			continue
		}
		tmp := filepath.Join(dir, name+".new")
		if err := writeSparse(tmp, tr); err != nil {
			os.Remove(tmp)
			return err
		}
		if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
			return err
		}
		seen[name] = true
	}
	for n := range vmFiles {
		if !seen[n] {
			return fmt.Errorf("VM image: %s missing", n)
		}
	}
	return nil
}

func writeSparse(path string, r io.Reader) error {
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	buf := make([]byte, 64<<10)
	var off int64
	for {
		n, rerr := io.ReadFull(r, buf)
		if n > 0 {
			if isZero(buf[:n]) {
				if _, err := out.Seek(int64(n), io.SeekCurrent); err != nil {
					out.Close()
					return err
				}
			} else if _, err := out.Write(buf[:n]); err != nil {
				out.Close()
				return err
			}
			off += int64(n)
		}
		if rerr == io.EOF || rerr == io.ErrUnexpectedEOF {
			break
		}
		if rerr != nil {
			out.Close()
			return rerr
		}
	}
	if err := out.Truncate(off); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func isZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

// randomMAC is a locally administered unicast address.
func randomMAC() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	b[0] = (b[0] | 0x02) & 0xfe
	return net.HardwareAddr(b).String()
}

// vfkitArgs is the vfkit command line for the machine.
func (m vfkitMachine) vfkitArgs(c vmConfig) ([]string, error) {
	cmdline, err := os.ReadFile(m.path("cmdline"))
	if err != nil {
		return nil, err
	}
	cl := strings.TrimSpace(string(cmdline))
	if strings.Contains(cl, ",") {
		return nil, errors.New("VM image: the kernel command line must not contain commas")
	}
	return []string{
		"--cpus", strconv.Itoa(c.CPUs),
		"--memory", strconv.Itoa(c.MemoryMiB),
		"--bootloader", fmt.Sprintf("linux,kernel=%s,initrd=%s,cmdline=%q", m.path("vmlinuz"), m.path("initramfs"), cl),
		"--device", "virtio-blk,path=" + m.path("rootfs.img"),
		"--device", "virtio-blk,path=" + m.path("data.img"),
		"--device", "virtio-net,nat,mac=" + c.MAC,
		"--device", "virtio-rng",
		"--device", "virtio-serial,logFilePath=" + m.path("console.log"),
		"--device", "virtio-vsock,port=1024,socketURL=" + m.socket() + ",listen",
		"--device", "virtio-vsock,port=1025,socketURL=" + m.path("shutdown.sock") + ",listen",
		"--device", "virtio-fs,sharedDir=/Users,mountTag=users",
		"--restful-uri", "unix://" + m.path("vfkit.sock"),
	}, nil
}

func (m vfkitMachine) ping() bool {
	if _, err := os.Stat(m.socket()); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix/v1/ping", nil)
	resp, err := httpClient(m.socket()).Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// runPID is the pid of the `nyx machine run` supervising the VM, or 0.
func (m vfkitMachine) runPID() int {
	b, err := os.ReadFile(m.path("run.pid"))
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return 0
	}
	if p, err := os.FindProcess(pid); err != nil || p.Signal(syscall.Signal(0)) != nil {
		return 0
	}
	return pid
}

// run boots the VM and supervises it in the foreground: launchd runs this
// (nyx machine autostart), `nyx machine start` runs it in the background. It
// forwards published container ports to localhost, and on SIGTERM / SIGINT
// asks the VM to shut down cleanly before it exits.
func (m vfkitMachine) run() error {
	c, err := m.loadConfig()
	if err != nil {
		return err
	}
	if pid := m.runPID(); pid != 0 && pid != os.Getpid() {
		fmt.Printf("machine %q is already running (pid %d)\n", m.name, pid)
		return nil // not an error: launchd must not restart us
	}
	if err := os.WriteFile(m.path("run.pid"), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		return err
	}
	defer os.Remove(m.path("run.pid"))
	for _, s := range []string{"nyxd.sock", "shutdown.sock", "vfkit.sock"} {
		os.Remove(m.path(s))
	}
	// Keep one previous console log.
	_ = os.Rename(m.path("console.log"), m.path("console.log.1"))

	args, err := m.vfkitArgs(c)
	if err != nil {
		return err
	}
	logf, err := os.OpenFile(m.path("vfkit.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	vm := exec.Command(vfkitExe(), args...)
	vm.Stdout, vm.Stderr = logf, logf
	if err := vm.Start(); err != nil {
		return fmt.Errorf("start vfkit: %w (brew install vfkit)", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- vm.Wait() }()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigs)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	proxy := newPortProxy(m.socket(), func() string { return vmIP(c.MAC) })
	go proxy.loop(ctx)
	defer proxy.closeAll()

	select {
	case err := <-exited:
		return fmt.Errorf("the VM stopped on its own (%v); see: nyx machine logs%s", err, nameFlag(m.name))
	case <-sigs:
		cancel()
		return m.shutdownVM(vm, exited)
	}
}

// shutdownVM asks the guest to shut down cleanly, over vsock (works on
// arm64, whose kernel has no power-button driver) and then with the power
// button, and finally forces it: within the 80s nyx machine stop waits.
func (m vfkitMachine) shutdownVM(vm *exec.Cmd, exited chan error) error {
	steps := []struct {
		ask  func() error
		wait time.Duration
	}{
		{m.requestPoweroff, 45 * time.Second},
		{func() error { return m.vfkitState("Stop") }, 15 * time.Second},
		{func() error { return m.vfkitState("HardStop") }, 10 * time.Second},
	}
	for _, s := range steps {
		if err := s.ask(); err != nil {
			continue
		}
		select {
		case <-exited:
			return nil
		case <-time.After(s.wait):
		}
	}
	_ = vm.Process.Kill()
	<-exited
	return nil
}

// requestPoweroff connects to the guest's shutdown port (nyx-power in the VM
// image), which powers the VM off.
func (m vfkitMachine) requestPoweroff() error {
	c, err := net.DialTimeout("unix", m.path("shutdown.sock"), 5*time.Second)
	if err != nil {
		return err
	}
	return c.Close()
}

func (m vfkitMachine) vfkitState(state string) error {
	c := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", m.path("vfkit.sock"))
		},
	}}
	body, _ := json.Marshal(map[string]string{"state": state})
	resp, err := c.Post("http://vfkit/vm/state", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (m vfkitMachine) start() error {
	if m.ping() {
		fmt.Printf("nyxd is already running in machine %q\n", m.name)
		return nil
	}
	if _, err := m.loadConfig(); err != nil {
		return err
	}
	if m.runPID() == 0 {
		if m.autostartEnabled() {
			// launchd owns the process: let it start it.
			_ = exec.Command(launchctl(), "kickstart", m.launchdTarget()).Run()
		}
		if m.runPID() == 0 {
			if err := m.spawnRun(); err != nil {
				return err
			}
		}
	}
	for i := 0; i < 240; i++ {
		if m.ping() {
			fmt.Printf("nyxd is running in machine %q (socket %s)\n", m.name, m.socket())
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("nyxd did not answer within 120s; see: nyx machine logs%s", nameFlag(m.name))
}

// spawnRun starts `nyx machine run` detached, logging to run.log.
func (m vfkitMachine) spawnRun() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	logf, err := os.OpenFile(m.path("run.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(exe, "machine", "run", "--name", m.name)
	cmd.Stdout, cmd.Stderr = logf, logf
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func (m vfkitMachine) stop(bool) error {
	pid := m.runPID()
	if pid == 0 {
		fmt.Printf("machine %q is not running\n", m.name)
		return nil
	}
	p, _ := os.FindProcess(pid)
	if err := p.Signal(syscall.SIGTERM); err != nil {
		return err
	}
	for i := 0; i < 160; i++ {
		if m.runPID() == 0 {
			fmt.Printf("stopped machine %q\n", m.name)
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("machine %q did not stop within 80s (pid %d)", m.name, pid)
}

func (m vfkitMachine) remove() error {
	if err := m.stop(false); err != nil {
		return err
	}
	_ = m.autostart(false)
	dir, err := m.dir()
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	fmt.Printf("removed machine %q and its data\n", m.name)
	return nil
}

func (m vfkitMachine) status() error {
	c, err := m.loadConfig()
	if err != nil {
		fmt.Printf("%s: not created (nyx machine init%s)\n", m.name, nameFlag(m.name))
		return nil
	}
	if !m.ping() {
		fmt.Printf("%s: not running (start it with: nyx machine start%s)\n", m.name, nameFlag(m.name))
		return nil
	}
	fmt.Printf("%s: running, %d CPUs, %d MiB, image %s\n", m.name, c.CPUs, c.MemoryMiB, c.Version)
	fmt.Printf("  socket:    %s\n", m.socket())
	if ip := vmIP(c.MAC); ip != "" {
		fmt.Printf("  address:   %s\n", ip)
	}
	if m.autostartEnabled() {
		fmt.Println("  autostart: at login (launchd)")
	}
	return nil
}

func (m vfkitMachine) logs() error {
	for _, f := range []string{"console.log", "run.log"} {
		b, err := os.ReadFile(m.path(f))
		if err != nil {
			continue
		}
		fmt.Printf("==> %s <==\n", m.path(f))
		os.Stdout.Write(b) //nolint:errcheck
	}
	return nil
}

// ─── launchd ──────────────────────────────────────────────────────────────────

func (m vfkitMachine) launchdLabel() string {
	if m.name == "nyx" {
		return "io.github.zrougamed.nyx.machine"
	}
	return "io.github.zrougamed.nyx.machine." + m.name
}

func (m vfkitMachine) launchdTarget() string {
	return fmt.Sprintf("gui/%d/%s", os.Getuid(), m.launchdLabel())
}

func (m vfkitMachine) plistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", m.launchdLabel()+".plist"), nil
}

func (m vfkitMachine) autostartEnabled() bool {
	p, err := m.plistPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// autostart installs (or removes) a launchd agent that runs
// `nyx machine run` at login and restarts it if it fails.
func (m vfkitMachine) autostart(on bool) error {
	p, err := m.plistPath()
	if err != nil {
		return err
	}
	if !on {
		_ = exec.Command(launchctl(), "bootout", m.launchdTarget()).Run()
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		fmt.Printf("machine %q no longer starts at login\n", m.name)
		return nil
	}
	if _, err := m.loadConfig(); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(p, []byte(m.plist(exe)), 0o644); err != nil {
		return err
	}
	_ = exec.Command(launchctl(), "bootout", m.launchdTarget()).Run()
	if out, err := exec.Command(launchctl(), "bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), p).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap: %v: %s", err, strings.TrimSpace(string(out)))
	}
	fmt.Printf("machine %q starts at login (%s)\n", m.name, p)
	return nil
}

func (m vfkitMachine) plist(exe string) string {
	esc := func(s string) string {
		var b strings.Builder
		_ = xmlEscape(&b, s)
		return b.String()
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + esc(m.launchdLabel()) + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + esc(exe) + `</string>
		<string>machine</string>
		<string>run</string>
		<string>--name</string>
		<string>` + esc(m.name) + `</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ProcessType</key>
	<string>Background</string>
	<key>StandardOutPath</key>
	<string>` + esc(m.path("run.log")) + `</string>
	<key>StandardErrorPath</key>
	<string>` + esc(m.path("run.log")) + `</string>
</dict>
</plist>
`
}

func xmlEscape(w io.Writer, s string) error {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	_, err := io.WriteString(w, r.Replace(s))
	return err
}

// ─── VM address and port forwarding ───────────────────────────────────────────

// dhcpLeases is the macOS NAT's lease file (NYX_DHCP_LEASES for tests).
func dhcpLeases() string {
	if v := os.Getenv("NYX_DHCP_LEASES"); v != "" {
		return v
	}
	return "/var/db/dhcpd_leases"
}

// vmIP finds the VM's address by its MAC in the DHCP leases. The file writes
// MACs without leading zeros ("2:a:..."), so both sides are normalized.
func vmIP(mac string) string {
	want := normMAC(mac)
	b, err := os.ReadFile(dhcpLeases())
	if err != nil || want == "" {
		return ""
	}
	var ip string
	for _, block := range strings.Split(string(b), "}") {
		var addr, hw string
		for _, line := range strings.Split(block, "\n") {
			k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
			if !ok {
				continue
			}
			switch k {
			case "ip_address":
				addr = v
			case "hw_address":
				_, hw, _ = strings.Cut(v, ",")
			}
		}
		if addr != "" && normMAC(hw) == want {
			ip = addr // the last lease for the MAC is the newest
		}
	}
	return ip
}

func normMAC(s string) string {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(s)), ":")
	if len(parts) != 6 {
		return ""
	}
	for i, p := range parts {
		n, err := strconv.ParseUint(p, 16, 8)
		if err != nil {
			return ""
		}
		parts[i] = fmt.Sprintf("%02x", n)
	}
	return strings.Join(parts, ":")
}

var publishedTCP = regexp.MustCompile(`:(\d+)->\d+/tcp`)

// portProxy mirrors the VM's published TCP ports on 127.0.0.1, so a
// container published with -p 8080:80 answers on localhost:8080 on the Mac.
type portProxy struct {
	socket string
	vmIP   func() string
	mu     sync.Mutex
	ln     map[int]net.Listener
}

func newPortProxy(socket string, vmIP func() string) *portProxy {
	return &portProxy{socket: socket, vmIP: vmIP, ln: map[int]net.Listener{}}
}

func (p *portProxy) loop(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		p.sync()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// publishedPorts asks nyxd which host ports containers publish.
func (p *portProxy) publishedPorts() (map[int]bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix/v1/containers?detail=1", nil)
	resp, err := httpClient(p.socket).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Items []struct {
			Ports string `json:"ports"`
		} `json:"items"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out); err != nil {
		return nil, err
	}
	ports := map[int]bool{}
	for _, it := range out.Items {
		for _, m := range publishedTCP.FindAllStringSubmatch(it.Ports, -1) {
			if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
				ports[n] = true
			}
		}
	}
	return ports, nil
}

func (p *portProxy) sync() {
	want, err := p.publishedPorts()
	if err != nil {
		return // nyxd not up yet, or restarting: try again on the next tick
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for port, l := range p.ln {
		if !want[port] {
			l.Close()
			delete(p.ln, port)
		}
	}
	for port := range want {
		if _, ok := p.ln[port]; ok {
			continue
		}
		l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			fmt.Fprintf(os.Stderr, "nyx machine: cannot forward localhost:%d: %v\n", port, err)
			continue
		}
		p.ln[port] = l
		go p.serve(l, port)
	}
}

func (p *portProxy) serve(l net.Listener, port int) {
	for {
		in, err := l.Accept()
		if err != nil {
			return
		}
		go func() {
			defer in.Close()
			ip := p.vmIP()
			if ip == "" {
				return
			}
			out, err := net.DialTimeout("tcp", net.JoinHostPort(ip, strconv.Itoa(port)), 5*time.Second)
			if err != nil {
				return
			}
			defer out.Close()
			relay(in, out)
		}()
	}
}

// relay copies both ways, passing half-closes on.
func relay(a, b net.Conn) {
	var wg sync.WaitGroup
	cp := func(dst, src net.Conn) {
		defer wg.Done()
		io.Copy(dst, src) //nolint:errcheck
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite() //nolint:errcheck
		} else {
			dst.Close()
		}
	}
	wg.Add(2)
	go cp(a, b)
	go cp(b, a)
	wg.Wait()
}

// forwarded lists the forwarded ports (tests, status).
func (p *portProxy) forwarded() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []int
	for port := range p.ln {
		out = append(out, port)
	}
	sort.Ints(out)
	return out
}

func (p *portProxy) closeAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for port, l := range p.ln {
		l.Close()
		delete(p.ln, port)
	}
}
