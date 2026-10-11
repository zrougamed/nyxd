//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

func hideWindow(*exec.Cmd) {}

// detach lets the started process outlive nyx (own session).
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
