//go:build linux

package main

import (
	"os/exec"
	"syscall"
)

// configureProc detaches the child into its own process group so that the whole
// tree can be signalled at once.
func configureProc(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

func signalProc(cmd *exec.Cmd, term bool) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	sig := syscall.SIGTERM
	if !term {
		sig = syscall.SIGKILL
	}
	// negative pid signals the whole process group
	if err := syscall.Kill(-pid, sig); err != nil {
		return cmd.Process.Signal(sig)
	}
	return nil
}
