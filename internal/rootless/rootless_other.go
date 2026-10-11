//go:build !linux

package rootless

import "errors"

// Info describes the namespace the daemon is running in.
type Info struct {
	Rootless bool
	SubIDs   bool
}

// EnvHostUID carries the invoking user's uid into the namespace.
const EnvHostUID = "_NYXD_HOST_UID"

// Wanted is always false off Linux.
func Wanted() bool { return false }

// Current reports whether this process is the rootless daemon stage.
func Current() Info { return Info{} }

// Enter is only supported on Linux.
func Enter() (Info, error) { return Info{}, errors.New("rootless mode is supported only on linux") }
