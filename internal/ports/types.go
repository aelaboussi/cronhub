// Package ports defines the seams (interfaces) the core depends on, plus the
// domain types passed across them. The core imports ONLY this package's
// interfaces and types — never a concrete implementation. See ARCHITECTURE.md.
package ports

import "time"

// Job is a single scheduled unit of work. It is fully resolved: any optional
// config field has already had defaults applied by the config layer, so the
// core never sees "unset".
type Job struct {
	Name      string
	Schedule  string // raw schedule spec, interpreted by the ScheduleParser
	Command   string // raw command, interpreted by the Executor
	OnOverlap OverlapMode
	OnMissed  MissedMode
	Timeout   time.Duration // 0 = no timeout
	Notify    []string      // notifier names, e.g. ["log"]
	Timezone  string        // IANA name, e.g. "Africa/Casablanca"
}

// OverlapMode: what to do when a job is due but its previous run is still active.
type OverlapMode string

const (
	OverlapSkip     OverlapMode = "skip"     // v1 default
	OverlapQueue    OverlapMode = "queue"    // roadmap
	OverlapParallel OverlapMode = "parallel" // roadmap
	OverlapKill     OverlapMode = "kill"     // roadmap
)

// MissedMode: what to do for a scheduled time that elapsed while the daemon was
// down or the machine asleep.
type MissedMode string

const (
	MissedSkip        MissedMode = "skip"          // v1 default
	MissedCatchUpOnce MissedMode = "catch_up_once" // run once now if any were missed
	MissedCatchUpAll  MissedMode = "catch_up_all"  // roadmap: replay every missed run
)

// RunResult is the outcome the Executor reports for a single execution.
type RunResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Started  time.Time
	Duration time.Duration
	Err      error // non-nil if the process could not be started/killed cleanly
}

// RunRecord is a persisted history entry (Store.RecordRun / ReadHistory).
type RunRecord struct {
	JobName  string
	Started  time.Time
	Duration time.Duration
	ExitCode int
	Success  bool
	Stdout   string
	Stderr   string
}

// NotifyEvent describes something worth telling the user about (Notifier.Notify).
type NotifyEvent struct {
	JobName string
	Result  RunResult
}
