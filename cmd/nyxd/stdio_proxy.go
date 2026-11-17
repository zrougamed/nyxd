package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
)

// stdioProxy connects stdin/stdout to the daemon's control socket. nyx runs
// it through `wsl.exe --exec` on Windows and through ssh for ssh://
// endpoints, so the API is reachable without any TCP listener: every
// connection is one short-lived process.
func stdioProxy(args []string) int {
	fs := flag.NewFlagSet("stdio-proxy", flag.ContinueOnError)
	socket := fs.String("socket", "", "control socket to connect to (default: the user's rootless daemon if running, else "+systemSocket+")")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *socket == "" {
		*socket = proxyDefaultSocket(os.Geteuid(), os.Getenv("XDG_RUNTIME_DIR"))
	}
	conn, err := net.Dial("unix", *socket)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nyxd stdio-proxy:", err)
		return 1
	}
	uc := conn.(*net.UnixConn)
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(os.Stdout, uc)
		close(done)
	}()
	_, _ = io.Copy(uc, os.Stdin)
	_ = uc.CloseWrite() // request body finished; keep reading the response
	<-done
	return 0
}

// systemSocket is the system daemon's control socket.
const systemSocket = "/run/nyxd/nyxd.sock"

// proxyDefaultSocket mirrors nyx's default: a non-root user's own rootless
// daemon at $XDG_RUNTIME_DIR/nyxd/nyxd.sock when it exists, else the system
// daemon. An ssh:// endpoint without a path lands here.
func proxyDefaultSocket(euid int, runtimeDir string) string {
	if euid != 0 && runtimeDir != "" {
		p := filepath.Join(runtimeDir, "nyxd", "nyxd.sock")
		if fi, err := os.Stat(p); err == nil && fi.Mode()&os.ModeSocket != 0 {
			return p
		}
	}
	return systemSocket
}
