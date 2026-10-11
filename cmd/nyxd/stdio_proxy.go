package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"os"
)

// stdioProxy connects stdin/stdout to the daemon's control socket. nyx on
// Windows runs it through `wsl.exe --exec`, so the API is reachable without
// any TCP listener: every connection is one short-lived process.
func stdioProxy(args []string) int {
	fs := flag.NewFlagSet("stdio-proxy", flag.ContinueOnError)
	socket := fs.String("socket", "/run/nyxd/nyxd.sock", "control socket to connect to")
	if err := fs.Parse(args); err != nil {
		return 2
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
