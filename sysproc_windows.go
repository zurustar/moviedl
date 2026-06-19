//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

var (
	ntdll            = syscall.NewLazyDLL("ntdll.dll")
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	ntSuspendProcess = ntdll.NewProc("NtSuspendProcess")
	ntResumeProcess  = ntdll.NewProc("NtResumeProcess")
	openProcess      = kernel32.NewProc("OpenProcess")
	closeHandle      = kernel32.NewProc("CloseHandle")
)

const processSuspendResume = 0x0800

func applyOSProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

func suspendProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	h, _, _ := openProcess.Call(processSuspendResume, 0, uintptr(cmd.Process.Pid))
	if h == 0 {
		return syscall.EINVAL
	}
	defer closeHandle.Call(h) //nolint:errcheck
	ntSuspendProcess.Call(h)  //nolint:errcheck
	return nil
}

func resumeProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	h, _, _ := openProcess.Call(processSuspendResume, 0, uintptr(cmd.Process.Pid))
	if h == 0 {
		return syscall.EINVAL
	}
	defer closeHandle.Call(h) //nolint:errcheck
	ntResumeProcess.Call(h)   //nolint:errcheck
	return nil
}
