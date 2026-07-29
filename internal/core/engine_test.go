package core

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/aelaboussi/cronhub/internal/ports"
)

// --- fakes -----------------------------------------------------------------

type fakeParser struct{}

func (fakeParser) Parse(string) (ports.Schedule, error) { return fakeSchedule{}, nil }

// fakeSchedule fires every hour on the hour relative to `after`.
type fakeSchedule struct{}

func (fakeSchedule) Next(after time.Time) time.Time {
	return after.Truncate(time.Hour).Add(time.Hour)
}

type fakeTrigger struct{}

func (fakeTrigger) Decide(_, _, _ time.Time) ports.TriggerDecision { return ports.TriggerSkip }

type fakeOverlap struct{}

func (fakeOverlap) OnOverlap(ports.Job) ports.OverlapDecision { return ports.OverlapDecisionSkip }

type fakeExecutor struct {
	mu       sync.Mutex
	launched []string
}

func (f *fakeExecutor) Run(_ context.Context, j ports.Job) ports.RunResult {
	f.mu.Lock()
	f.launched = append(f.launched, j.Name)
	f.mu.Unlock()
	return ports.RunResult{Started: time.Now()}
}
func (f *fakeExecutor) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.launched)
}

// fakeStore returns preset history and swallows everything else.
type fakeStore struct {
	history map[string][]ports.RunRecord
}

func (s *fakeStore) SaveJob(ports.Job) error         { return nil }
func (s *fakeStore) LoadJobs() ([]ports.Job, error)  { return nil, nil }
func (s *fakeStore) RecordRun(ports.RunRecord) error { return nil }
func (s *fakeStore) ReadHistory(job string, _ int) ([]ports.RunRecord, error) {
	return s.history[job], nil
}
func (s *fakeStore) MarkRunning(string, time.Time) error      { return nil }
func (s *fakeStore) ClearRunning(string) error                { return nil }
func (s *fakeStore) ListRunning() ([]ports.RunningJob, error) { return nil, nil }
func (s *fakeStore) Close() error                             { return nil }

// runCatchUpPass builds an engine with the given jobs+history, runs it briefly
// so the startup catch-up pass executes, then stops it, and reports how many
// jobs the executor launched.
func runCatchUpPass(t *testing.T, jobs []ports.Job, history map[string][]ports.RunRecord) int {
	t.Helper()
	exec := &fakeExecutor{}
	store := &fakeStore{history: history}
	// LoadJobs must return the jobs; wire that through a small shim store.
	store2 := &jobsStore{fakeStore: store, jobs: jobs}

	eng := New(Deps{
		Parser:    fakeParser{},
		Trigger:   fakeTrigger{},
		Overlap:   fakeOverlap{},
		Executor:  exec,
		Store:     store2,
		Notifiers: map[string]ports.Notifier{},
	})

	done := make(chan struct{})
	go func() {
		_ = eng.Run(context.Background())
		close(done)
	}()
	// Give the startup catch-up pass time to run and launch goroutines.
	time.Sleep(100 * time.Millisecond)
	eng.Stop()
	<-done
	return exec.count()
}

type jobsStore struct {
	*fakeStore
	jobs []ports.Job
}

func (s *jobsStore) LoadJobs() ([]ports.Job, error) { return s.jobs, nil }

// --- tests -----------------------------------------------------------------

func TestCatchUpOnce_RunsWhenMissed(t *testing.T) {
	job := ports.Job{Name: "j", Schedule: "0 * * * *", Command: "x", OnMissed: ports.MissedCatchUpOnce}
	// last ran 3 hours ago => scheduled times were missed
	history := map[string][]ports.RunRecord{
		"j": {{JobName: "j", Started: time.Now().Add(-3 * time.Hour), Success: true}},
	}
	if n := runCatchUpPass(t, []ports.Job{job}, history); n != 1 {
		t.Errorf("expected exactly 1 catch-up launch, got %d", n)
	}
}

func TestCatchUpOnce_SkipsWhenNoHistory(t *testing.T) {
	job := ports.Job{Name: "j", Schedule: "0 * * * *", Command: "x", OnMissed: ports.MissedCatchUpOnce}
	// no history => nothing to catch up to
	if n := runCatchUpPass(t, []ports.Job{job}, map[string][]ports.RunRecord{}); n != 0 {
		t.Errorf("expected 0 launches for never-run job, got %d", n)
	}
}

func TestCatchUpOnce_SkipModeDoesNothing(t *testing.T) {
	job := ports.Job{Name: "j", Schedule: "0 * * * *", Command: "x", OnMissed: ports.MissedSkip}
	history := map[string][]ports.RunRecord{
		"j": {{JobName: "j", Started: time.Now().Add(-3 * time.Hour), Success: true}},
	}
	if n := runCatchUpPass(t, []ports.Job{job}, history); n != 0 {
		t.Errorf("expected 0 launches for on_missed=skip, got %d", n)
	}
}
