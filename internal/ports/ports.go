package ports

import (
	"context"
	"time"
)

// ScheduleParser turns a raw spec string into something that can compute the
// next fire time. The core never knows the syntax. The two implementations (cron + readable words) live in the schedule package.
type ScheduleParser interface {
	Parse(spec string) (Schedule, error)
}

// Schedule computes the next fire time strictly after `after`.
type Schedule interface {
	Next(after time.Time) time.Time
}

// TriggerPolicy decides, for a scheduled time that has arrived (or elapsed
// while the daemon was down), whether to run now, skip, or catch up.
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
// still active.
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

// Store persists job definitions and run history. Transactional. Backed by SQLite today.
type Store interface {
	SaveJob(job Job) error
	LoadJobs() ([]Job, error)
	RecordRun(rec RunRecord) error
	ReadHistory(jobName string, limit int) ([]RunRecord, error)

	// Live run state, used by `status`. The engine marks a job running when it
	// launches and clears it when the job finishes, so a separate `status`
	// process can see what is executing right now by reading the store.
	MarkRunning(jobName string, startedAt time.Time) error
	ClearRunning(jobName string) error
	ListRunning() ([]RunningJob, error)

	Close() error
}

// RunningJob is a job currently executing, as seen via the store.
type RunningJob struct {
	JobName   string
	StartedAt time.Time
}

// Notifier reports a run outcome. The log notifier is always available; others are declared in config.
type Notifier interface {
	Name() string
	Notify(ev NotifyEvent) error
}

// ServiceAdapter registers the daemon with the OS's existing service manager.
// cronhub is a client of systemd/launchd/SCM, never a replacement. Wraps
// kardianos/service and defaults to user-level registration.
type ServiceAdapter interface {
	Install() error
	Uninstall() error
	Start() error
	Stop() error
	Status() (string, error)
}
