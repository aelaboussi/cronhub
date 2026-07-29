//go:build windows

package executor

import (
	"os/exec"
	"strconv"
)

func buildCommand(command string) *exec.Cmd {
	return exec.Command("cmd", "/C", command)
}

// configureProcAttr: on Windows the robust way to kill a whole tree is a Job
// Object. v1 uses the simpler taskkill /T approach in killProcessTree and leaves
// Job Object association as a documented refinement. No proc attr needed here.
func configureProcAttr(cmd *exec.Cmd) {}

// killProcessTree uses taskkill to terminate the process and its children (/T)
// forcefully (/F). This is the pragmatic v1 approach; a Job Object gives
// stronger guarantees and is a roadmap refinement of this same seam.
func killProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	pid := strconv.Itoa(cmd.Process.Pid)
	return exec.Command("taskkill", "/F", "/T", "/PID", pid).Run()
}
