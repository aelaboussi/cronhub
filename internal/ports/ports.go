package ports

import (
	"context"
	"time"
)

// ScheduleParser turns a raw spec string into something that can compute the
// next fire time. The core never knows the syntax. v1 impl: cron syntax.
type ScheduleParser interface {
	Parse(spec string) (Schedule, error)
}

// Schedule computes the next fire time strictly after `after`.
type Schedule interface {
	Next(after time.Time) time.Time
}

// TriggerPolicy decides, for a scheduled time that has arrived (or elapsed
// while the daemon was down), whether to run now, skip, or catch up.
// v1 impl: skip missed.
type TriggerPolicy interface {
	Decide(scheduledFor, now, lastRun time.Time) TriggerDecision
}

type TriggerDecision int

const (
	TriggerRun TriggerDecision = iota
	TriggerSkip
	TriggerCatchUp // roadmap; v1 policy never returns this
)

// OverlapPolicy decides what to do when a job is due while its previous run is
// still active. v1 impl: skip.
type OverlapPolicy interface {
	OnOverlap(job Job) OverlapDecision
}

type OverlapDecision int

const (
	OverlapDecisionSkip OverlapDecision = iota
	OverlapDecisionQueue
	OverlapDecisionKillPrevious
	OverlapDecisionAllowParallel
)

// Executor runs a job's command and reports the result. It is the one port with
// genuine per-OS work (process kill / timeout with child processes), which lives
// behind build tags in the concrete impl — the interface stays OS-agnostic.
type Executor interface {
	// Run blocks until the command completes, the context is cancelled, or the
	// job's timeout elapses; it then reports the RunResult.
	Run(ctx context.Context, job Job) RunResult
}

// Store persists job definitions and run history. Transactional. v1 impl: SQLite.
type Store interface {
	SaveJob(job Job) error
	LoadJobs() ([]Job, error)
	RecordRun(rec RunRecord) error
	ReadHistory(jobName string, limit int) ([]RunRecord, error)
	Close() error
}

// Notifier reports a run outcome to the user. v1 impl: log.
type Notifier interface {
	Name() string
	Notify(ev NotifyEvent) error
}

// ServiceAdapter registers the daemon with the OS's existing service manager.
// cronhub is a client of systemd/launchd/SCM, never a replacement. v1 impl wraps
// kardianos/service and defaults to user-level registration.
type ServiceAdapter interface {
	Install() error
	Uninstall() error
	Start() error
	Stop() error
	Status() (string, error)
}
