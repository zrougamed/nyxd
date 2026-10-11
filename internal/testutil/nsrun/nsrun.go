//go:build linux

// Package nsrun runs a test inside new user, mount and network namespaces
// where the test process is root, so code that creates links, nftables rules,
// mounts or overlayfs is tested against the real kernel without privileges.
//
//	func TestBridge(t *testing.T) {
//		if !nsrun.Run(t, "/run", "/var/lib/nyxd") {
//			return // ran in the child; its result is already reported
//		}
//		// root in a private network stack; /run and /var/lib/nyxd are tmpfs
//	}
package nsrun

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

const envTest = "NYXD_NSRUN_TEST"

// Inside reports whether the process is the namespaced child of t.
func Inside(t *testing.T) bool { return os.Getenv(envTest) == t.Name() }

// Run re-executes the test binary for t alone in fresh namespaces and returns
// false; the caller must return. In that child it mounts a tmpfs on each of
// tmpfs (created if missing) and returns true. The test is skipped where
// unprivileged user namespaces are unavailable.
func Run(t *testing.T, tmpfs ...string) bool {
	t.Helper()
	if Inside(t) {
		if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
			t.Fatalf("nsrun: make / private: %v", err)
		}
		if err := loopbackUp(); err != nil {
			t.Fatalf("nsrun: lo up: %v", err)
		}
		for _, p := range tmpfs {
			if err := os.MkdirAll(p, 0o755); err != nil && !os.IsExist(err) {
				t.Fatalf("nsrun: mkdir %s: %v", p, err)
			}
			if err := unix.Mount("tmpfs", p, "tmpfs", 0, "mode=0755"); err != nil {
				t.Fatalf("nsrun: tmpfs on %s: %v", p, err)
			}
		}
		return true
	}

	cmd := exec.Command(os.Args[0], childArgs(t.Name())...)
	cmd.Env = append(os.Environ(), envTest+"="+t.Name())
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags:  syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS | syscall.CLONE_NEWNET | syscall.CLONE_NEWUTS | syscall.CLONE_NEWIPC,
		UidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getuid(), Size: 1}},
		GidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getgid(), Size: 1}},
	}
	out, err := cmd.CombinedOutput()
	if err != nil && cmd.Process == nil {
		t.Skipf("nsrun: user namespaces unavailable: %v", err)
	}
	if err != nil {
		t.Fatalf("namespaced run failed: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "--- SKIP") {
		t.Skipf("skipped in namespace:\n%s", out)
	}
	if testing.Verbose() {
		t.Logf("namespaced run:\n%s", out)
	}
	return false
}

// loopbackUp brings lo up in the new network namespace (127.0.0.1 works).
func loopbackUp() error {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	ifr, err := unix.NewIfreq("lo")
	if err != nil {
		return err
	}
	ifr.SetUint16(unix.IFF_UP | unix.IFF_LOOPBACK | unix.IFF_RUNNING)
	return unix.IoctlIfreq(fd, unix.SIOCSIFFLAGS, ifr)
}

// childArgs runs only test name, keeping coverage output (-test.gocoverdir
// files are per process; a shared -test.coverprofile would be overwritten).
func childArgs(name string) []string {
	parts := strings.Split(name, "/")
	for i, p := range parts {
		parts[i] = "^" + regexp.QuoteMeta(p) + "$"
	}
	args := []string{"-test.run=" + strings.Join(parts, "/"), "-test.v", "-test.count=1"}
	for _, a := range os.Args[1:] {
		if strings.HasPrefix(a, "-test.gocoverdir=") || strings.HasPrefix(a, "-test.timeout=") {
			args = append(args, a)
		}
	}
	return args
}
