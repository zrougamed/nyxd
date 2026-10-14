package main

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// version is set at build time (-X main.version); it selects the WSL distro
// release that `nyx machine init` downloads.
var version = "dev"

const releaseBase = "https://github.com/zrougamed/nyxd/releases/download"

// nyx machine manages where nyxd runs when the host is not Linux: the WSL2
// distro on Windows (below), the vfkit VM on macOS (machine_vfkit.go). The
// distro holds only a static nyxd and crun (no shell, no package manager);
// nyxd runs with -userns-remap, so containers never run as root of the WSL VM.
func doMachine(args []string) error {
	const usage = `usage: nyx machine <init|start|stop|rm|status|logs> [--name NAME]
  init [--image FILE|URL]   Windows: import the nyx WSL2 distro; macOS: create the nyx VM, or
                            upgrade its system (data kept). Default: the release matching this nyx.
       [--cpus N] [--memory MiB] [--disk-size GiB]   macOS VM size
  start                     start nyxd (no admin rights needed)
  stop [--terminate]        stop nyxd gracefully (--terminate also shuts the WSL distro down)
  rm                        stop and delete the machine and its data
  status                    show whether nyxd answers
  logs                      print the daemon log
  autostart on|off          macOS: start the machine at login (launchd)
  run                       macOS: run the VM in the foreground (what start and launchd run)`
	if len(args) == 0 {
		return errors.New(usage)
	}
	sub := args[0]
	fs := flag.NewFlagSet("machine "+sub, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	name := fs.String("name", "nyx", "machine name")
	image := fs.String("image", "", "distro tarball or VM image (file or http(s) URL)")
	terminate := fs.Bool("terminate", false, "also terminate the WSL distro")
	cpus := fs.Int("cpus", 0, "VM CPUs (macOS)")
	memory := fs.Int("memory", 0, "VM memory in MiB (macOS)")
	disk := fs.Int("disk-size", 0, "VM data disk in GiB (macOS)")
	if err := fs.Parse(args[1:]); err != nil {
		return errors.New(usage)
	}
	pos := fs.Args()
	if (sub == "autostart" && (len(pos) != 1 || (pos[0] != "on" && pos[0] != "off"))) || (sub != "autostart" && len(pos) != 0) {
		return errors.New(usage)
	}
	if useVfkit() {
		vm := vfkitMachine{name: *name}
		switch sub {
		case "init":
			return vm.init(machineOpts{image: *image, cpus: *cpus, memory: *memory, diskSize: *disk})
		case "start":
			return vm.start()
		case "stop":
			return vm.stop(*terminate)
		case "rm":
			return vm.remove()
		case "status":
			return vm.status()
		case "logs":
			return vm.logs()
		case "run":
			return vm.run()
		case "autostart":
			return vm.autostart(pos[0] == "on")
		default:
			return errors.New(usage)
		}
	}
	if sub == "run" || sub == "autostart" {
		return errors.New("nyx machine " + sub + " is for the macOS VM; on Windows, install.ps1 registers the logon task")
	}
	if runtime.GOOS != "windows" && os.Getenv("NYX_WSL_EXE") == "" {
		return errors.New("nyx machine manages the WSL2 distro on Windows and the VM on macOS; on Linux run nyxd directly (see docs/rootless.md)")
	}
	m := machine{name: *name}
	switch sub {
	case "init":
		return m.init(*image)
	case "start":
		return m.start()
	case "stop":
		return m.stop(*terminate)
	case "rm":
		return m.remove()
	case "status":
		return m.status()
	case "logs":
		return m.logs()
	default:
		return errors.New(usage)
	}
}

type machine struct{ name string }

func (m machine) endpoint() string { return wslScheme + m.name }

// dir is %LOCALAPPDATA%\nyx\machines\<name> (disk image, log).
func (m machine) dir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "nyx", "machines", m.name), nil
}

func (m machine) wsl(args ...string) error {
	cmd := exec.Command(wslExe(), args...)
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// wsl.exe prints UTF-16 on some versions; drop NULs for readability.
		msg := strings.TrimSpace(strings.ReplaceAll(string(out), "\x00", ""))
		return fmt.Errorf("wsl %s: %v: %s", strings.Join(args, " "), err, msg)
	}
	return nil
}

func (m machine) inDistro(args ...string) error {
	return m.wsl(append([]string{"-d", m.name, "-u", "root", "--exec"}, args...)...)
}

func (m machine) init(image string) error {
	dir, err := m.dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if image == "" {
		if version == "dev" {
			return errors.New("this nyx is a development build: pass --image with a distro tarball (make wsl-rootfs)")
		}
		image = fmt.Sprintf("%s/%s/nyx-wsl-%s.tar.gz", releaseBase, version, runtime.GOARCH)
	}
	tarPath := filepath.Join(dir, "rootfs.tar")
	defer os.Remove(tarPath)
	if err := fetchRootfs(image, tarPath); err != nil {
		return err
	}
	fmt.Printf("importing WSL distro %q into %s\n", m.name, dir)
	if err := m.wsl("--import", m.name, filepath.Join(dir, "disk"), tarPath, "--version", "2"); err != nil {
		return err
	}
	fmt.Printf("done. Start it with: nyx machine start%s\n", nameFlag(m.name))
	return nil
}

func nameFlag(name string) string {
	if name == "nyx" {
		return ""
	}
	return " --name " + name
}

// fetchRootfs copies (or downloads and sha256-verifies) a distro tarball
// to dst as an uncompressed tar.
func fetchRootfs(src, dst string) error {
	var r io.Reader
	if strings.HasPrefix(src, "https://") || strings.HasPrefix(src, "http://") {
		want, err := fetchChecksum(src + ".sha256")
		if err != nil {
			return err
		}
		fmt.Printf("downloading %s\n", src)
		resp, err := http.Get(src)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("download %s: %s", src, resp.Status)
		}
		tmp := dst + ".download"
		f, err := os.Create(tmp)
		if err != nil {
			return err
		}
		h := sha256.New()
		_, cErr := io.Copy(io.MultiWriter(f, h), resp.Body)
		f.Close()
		defer os.Remove(tmp)
		if cErr != nil {
			return cErr
		}
		if got := hex.EncodeToString(h.Sum(nil)); got != want {
			return fmt.Errorf("download %s: sha256 %s does not match published %s", src, got, want)
		}
		src = tmp
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	r = in
	if gz, err := gzip.NewReader(in); err == nil {
		defer gz.Close()
		r = gz
	} else if _, err := in.Seek(0, io.SeekStart); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func fetchChecksum(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("checksum %s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 || len(fields[0]) != 64 {
		return "", fmt.Errorf("checksum %s: unexpected content", url)
	}
	return strings.ToLower(fields[0]), nil
}

func (m machine) ping() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix/v1/ping", nil)
	resp, err := httpClient(m.endpoint()).Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (m machine) start() error {
	if m.ping() {
		fmt.Printf("nyxd is already running in %q\n", m.name)
		return nil
	}
	dir, err := m.dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Join(dir, "nyxd.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	// Root in the distro only maps IDs; nyxd and containers run in a user
	// namespace mapped to 100000+ (see docs/windows.md).
	cmd := exec.Command(wslExe(), "-d", m.name, "-u", "root", "--exec",
		"/usr/local/bin/nyxd", "-userns-remap=default", "-log-level", "info")
	cmd.Stdout, cmd.Stderr = logf, logf
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", wslExe(), err)
	}
	_ = cmd.Process.Release()
	for i := 0; i < 120; i++ {
		if m.ping() {
			fmt.Printf("nyxd is running in WSL distro %q\n", m.name)
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("nyxd did not answer within 60s; see: nyx machine logs%s", nameFlag(m.name))
}

func (m machine) stop(terminate bool) error {
	if err := m.inDistro("/usr/local/bin/nyxd", "stop"); err != nil && !terminate {
		return err
	}
	if terminate {
		return m.wsl("--terminate", m.name)
	}
	return nil
}

func (m machine) remove() error {
	_ = m.inDistro("/usr/local/bin/nyxd", "stop")
	if err := m.wsl("--unregister", m.name); err != nil {
		return err
	}
	if dir, err := m.dir(); err == nil {
		_ = os.RemoveAll(dir)
	}
	fmt.Printf("removed WSL distro %q\n", m.name)
	return nil
}

func (m machine) status() error {
	if m.ping() {
		fmt.Printf("%s: running (endpoint %s)\n", m.name, m.endpoint())
		return nil
	}
	fmt.Printf("%s: not running (start it with: nyx machine start%s)\n", m.name, nameFlag(m.name))
	return nil
}

func (m machine) logs() error {
	dir, err := m.dir()
	if err != nil {
		return err
	}
	f, err := os.Open(filepath.Join(dir, "nyxd.log"))
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(os.Stdout, f)
	return err
}
