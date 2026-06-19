//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

func applyOSProcAttr(cmd *exec.Cmd) {}

func suspendProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Signal(syscall.SIGSTOP)
}

func resumeProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Signal(syscall.SIGCONT)
}
