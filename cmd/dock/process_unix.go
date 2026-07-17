//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

func sysProcAttr() *syscall.SysProcAttr { return nil }

func terminate(cmd *exec.Cmd) error {
	return cmd.Process.Signal(syscall.SIGTERM)
}
