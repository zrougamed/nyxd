package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/zrougamed/nyxd/internal/daemonlock"
)

// stopCmd gracefully stops the nyxd that holds the daemon lock under
// -base-dir: SIGTERM, then wait for the lock to be released. It needs no
// shell or kill binary (the nyx WSL distro has neither).
func stopCmd(args []string) int {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	baseDir := fs.String("base-dir", "/var/lib/nyxd", "base directory of the daemon to stop")
	timeout := fs.Duration("timeout", 60*time.Second, "how long to wait for a clean shutdown")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	pid, err := daemonlock.Holder(*baseDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nyxd stop:", err)
		return 1
	}
	if pid == 0 {
		fmt.Println("nyxd is not running")
		return 0
	}
	if err := daemonlock.Signal(pid); err != nil {
		fmt.Fprintln(os.Stderr, "nyxd stop:", err)
		return 1
	}
	deadline := time.Now().Add(*timeout)
	for time.Now().Before(deadline) {
		if p, err := daemonlock.Holder(*baseDir); err == nil && p == 0 {
			fmt.Println("nyxd stopped")
			return 0
		}
		time.Sleep(200 * time.Millisecond)
	}
	fmt.Fprintf(os.Stderr, "nyxd stop: daemon (pid %d) still running after %s\n", pid, *timeout)
	return 1
}
