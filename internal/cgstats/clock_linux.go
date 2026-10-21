//go:build linux

package cgstats

// USER_HZ is 100 on every common Linux configuration; reading it via
// sysconf is not exposed by golang.org/x/sys/unix on linux.
func clockTicks() int64 { return 100 }
