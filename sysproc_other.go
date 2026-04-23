//go:build !windows

package main

import "os/exec"

func applyOSProcAttr(cmd *exec.Cmd) {}
