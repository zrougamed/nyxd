//go:build linux

// Command fakewsl-init is pid 1 of a distro booted by scripts/fake-wsl.sh.
// Like WSL's own init, it makes the distro the root of its mount namespace
// (pivot_root, host tree detached; a chroot would not do: the kernel refuses
// user namespaces to chrooted processes, and nyxd -userns-remap needs one),
// then reaps orphaned processes until the distro is terminated.
//
//	fakewsl-init ROOTFS READYFILE
package main

import (
	"fmt"
	"os"
	"os/signal"

	"golang.org/x/sys/unix"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: fakewsl-init ROOTFS READYFILE")
		os.Exit(2)
	}
	// Opened before the pivot: the file lives outside the new root.
	ready, err := os.OpenFile(os.Args[2], os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	check(err)
	check(unix.Chdir(os.Args[1]))
	check(unix.PivotRoot(".", "."))
	check(unix.Unmount(".", unix.MNT_DETACH))
	check(unix.Chdir("/"))
	_, err = ready.WriteString("ok\n")
	check(err)
	check(ready.Close())

	sigs := make(chan os.Signal, 16)
	signal.Notify(sigs, unix.SIGCHLD)
	for range sigs {
		for {
			pid, err := unix.Wait4(-1, nil, unix.WNOHANG, nil)
			if pid <= 0 || err != nil {
				break
			}
		}
	}
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakewsl-init:", err)
		os.Exit(1)
	}
}
