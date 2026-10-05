//go:build !linux

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// configureProc is a no-op on non-linux platforms (development only).
func configureProc(cmd *exec.Cmd) {}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

func signalProc(cmd *exec.Cmd, term bool) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if term {
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	return cmd.Process.Kill()
}
