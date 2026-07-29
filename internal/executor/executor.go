// Package executor provides the v1 Executor: run a shell command locally,
// capture stdout/stderr and exit code, enforce timeout. The OS-agnostic logic
// lives here; process-group setup and kill-with-children live in build-tagged
// files (executor_unix.go / executor_windows.go) — this is the one seam with
// genuine per-OS work (see ARCHITECTURE.md §5).
package executor

import (
	"bytes"
	"context"
	"os/exec"
	"time"

	"github.com/aelaboussi/cronhub/internal/ports"
)

type Local struct{}

func NewLocal() *Local { return &Local{} }

func (l *Local) Run(ctx context.Context, job ports.Job) ports.RunResult {
	started := time.Now()

	if job.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, job.Timeout)
		defer cancel()
	}

	cmd := buildCommand(job.Command) // build-tagged: sh -c ... vs cmd /C ...
	configureProcAttr(cmd)           // build-tagged: new process group / job object

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	res := ports.RunResult{Started: started, ExitCode: -1}

	if err := cmd.Start(); err != nil {
		res.Err = err
		res.Duration = time.Since(started)
		return res
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-ctx.Done():
		// Timeout or cancellation: kill the whole process tree (build-tagged).
		_ = killProcessTree(cmd)
		<-done // reap
		res.Err = ctx.Err()
	case err := <-done:
		if exitErr, ok := err.(*exec.ExitError); ok {
			res.ExitCode = exitErr.ExitCode()
		} else if err != nil {
			res.Err = err
		} else {
			res.ExitCode = 0
		}
	}

	res.Stdout = stdout.String()
	res.Stderr = stderr.String()
	res.Duration = time.Since(started)
	return res
}
