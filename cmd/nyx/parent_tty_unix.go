//go:build unix

package main

import (
	"os"
	"syscall"
)

// nudgeParentShellRedraw sends SIGWINCH to the parent process (usually your interactive
// zsh/bash). Many shells refresh the prompt on WINCH, which avoids the “press Enter to
// see the prompt” artifact after a raw-mode child like `nyx exec -it … sh` exits.
func nudgeParentShellRedraw() {
	ppid := os.Getppid()
	if ppid <= 1 {
		return
	}
	_ = syscall.Kill(ppid, syscall.SIGWINCH)
}
