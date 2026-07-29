//go:build !windows

package executor

import (
	"os/exec"
	"syscall"
)

func buildCommand(command string) *exec.Cmd {
	return exec.Command("/bin/sh", "-c", command)
}

// configureProcAttr puts the child in its own process group so we can signal
// the whole tree (the child and anything it spawns) at once.
func configureProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessTree signals the entire process group. Negative PID = the group
// whose ID equals the child's PID (established by Setpgid above).
func killProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
