//go:build !linux

package layer

import (
	"errors"
	"os"
)

var errUnsupported = errors.New("layer: operation supported only on linux")

func mkWhiteoutAt(*os.Root, string) error               { return errUnsupported }
func mkfifoAt(*os.Root, string, uint32) error           { return errUnsupported }
func setXattrAt(*os.Root, string, string, []byte) error { return errUnsupported }
