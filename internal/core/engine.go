// Package core is the engine: it owns the scheduling loop and depends ONLY on
// the ports interfaces. It never imports a concrete impl. Everything it needs is
// injected via Deps. This is the rule that makes the whole thing extensible.
package core

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/aelaboussi/cronhub/internal/ports"
)

// Deps are the injected implementations. The core is agnostic to which concretes
// these are.
type Deps struct {
	Parser    ports.ScheduleParser
	Trigger   ports.TriggerPolicy
	Overlap   ports.OverlapPolicy
	Executor  ports.Executor
	Store     ports.Store
	Notifiers map[string]ports.Notifier // keyed by Notifier.Name()
}

type Engine struct {
	deps Deps

	mu      sync.Mutex
	running map[string]bool // jobName -> currently executing (for overlap policy)

	stop chan struct{}
	wg   sync.WaitGroup
}

func New(deps Deps) *Engine {
	return &Engine{
		deps:    deps,
		running: map[string]bool{},
		stop:    make(chan struct{}),
	}
}

// scheduled pairs a job with its parsed schedule and next fire time.
type scheduled struct {
	job  ports.Job
	sch  ports.Schedule
	next time.Time
	last time.Time
}

// Run loads jobs from the store and drives the loop until Stop is called.
func (e *Engine) Run(ctx context.Context) error {
	jobs, err := e.deps.Store.LoadJobs()
	if err != nil {
		return err
	}

	now := time.Now()
	var scheds []*scheduled
	for _, j := range jobs {
		sch, err := e.deps.Parser.Parse(j.Schedule)
		if err != nil {
			log.Printf("job %q: bad schedule %q: %v (skipped)", j.Name, j.Schedule, err)
			continue
		}
		scheds = append(scheds, &scheduled{job: j, sch: sch, next: sch.Next(now)})
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-e.stop:
			e.wg.Wait()
			return nil
		case <-ctx.Done():
			e.wg.Wait()
			return ctx.Err()
		case now := <-ticker.C:
			for _, s := range scheds {
				if now.Before(s.next) {
					continue
				}
				scheduledFor := s.next
				s.next = s.sch.Next(now) // advance regardless of decision

				switch e.deps.Trigger.Decide(scheduledFor, now, s.last) {
				case ports.TriggerSkip:
					continue
				case ports.TriggerRun, ports.TriggerCatchUp:
				}

				if e.isRunning(s.job.Name) {
					if e.deps.Overlap.OnOverlap(s.job) == ports.OverlapDecisionSkip {
						log.Printf("job %q still running; skipping overlap", s.job.Name)
						continue
					}
					// Other overlap modes (queue/kill/parallel) are roadmap.
				}

				s.last = scheduledFor
				e.launch(ctx, s.job)
			}
		}
	}
}

func (e *Engine) launch(ctx context.Context, job ports.Job) {
	e.setRunning(job.Name, true)
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		defer e.setRunning(job.Name, false)

		res := e.deps.Executor.Run(ctx, job)

		rec := ports.RunRecord{
			JobName:  job.Name,
			Started:  res.Started,
			Duration: res.Duration,
			ExitCode: res.ExitCode,
			Success:  res.Err == nil && res.ExitCode == 0,
			Stdout:   res.Stdout,
			Stderr:   res.Stderr,
		}
		if err := e.deps.Store.RecordRun(rec); err != nil {
			log.Printf("job %q: failed to persist run: %v", job.Name, err)
		}

		for _, name := range job.Notify {
			if n, ok := e.deps.Notifiers[name]; ok {
				_ = n.Notify(ports.NotifyEvent{JobName: job.Name, Result: res})
			}
		}
	}()
}

func (e *Engine) Stop() { close(e.stop) }

func (e *Engine) isRunning(name string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.running[name]
}

func (e *Engine) setRunning(name string, v bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if v {
		e.running[name] = true
	} else {
		delete(e.running, name)
	}
}
