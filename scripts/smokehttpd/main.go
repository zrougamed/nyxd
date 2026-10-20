// Command smokehttpd serves a directory over HTTP. scripts/repo-smoke.sh
// runs it inside each distribution's container, next to the install it
// tests: bare images have no web server, and a server on the host may not be
// reachable from a container (a firewalled CI runner, a remote nyxd).
//
//	smokehttpd DIR ADDR
package main

import (
	"fmt"
	"net/http"
	"os"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: smokehttpd DIR ADDR")
		os.Exit(2)
	}
	err := http.ListenAndServe(os.Args[2], http.FileServer(http.Dir(os.Args[1])))
	fmt.Fprintln(os.Stderr, "smokehttpd:", err)
	os.Exit(1)
}
