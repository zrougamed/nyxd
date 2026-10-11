package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// isNologin reports whether nyxd was started as /usr/sbin/nologin, the login
// shell of the nyx WSL distro (a symlink to nyxd). A plain `wsl -d nyx` then
// prints a hint instead of starting the daemon as real root without
// -userns-remap. Login shells get a "-" before argv[0].
func isNologin(argv0 string) bool {
	return filepath.Base(strings.TrimPrefix(argv0, "-")) == "nologin"
}

func nologin() int {
	fmt.Fprintln(os.Stderr, `This distro runs nyxd only and has no shell.
Use nyx.exe from Windows (nyx machine start, nyx ps, ...),
or run a command directly: wsl -d nyx --exec /usr/local/bin/nyxd -version`)
	return 1
}
