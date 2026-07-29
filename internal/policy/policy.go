// Package policy provides the v1 TriggerPolicy and OverlapPolicy: the documented
// default behaviors from ARCHITECTURE.md §6. Both are trivially swappable.
package policy

import (
	"time"

	"github.com/aelaboussi/cronhub/internal/ports"
)

// SkipMissed is the v1 TriggerPolicy: a scheduled time that has already elapsed
// (daemon was down / machine asleep) is skipped, not replayed.
type SkipMissed struct {
	// Grace is how late a fire may be and still count as "on time" rather than
	// "missed". Small value absorbs normal tick jitter.
	Grace time.Duration
}

func NewSkipMissed() *SkipMissed { return &SkipMissed{Grace: 5 * time.Second} }

func (s *SkipMissed) Decide(scheduledFor, now, _ time.Time) ports.TriggerDecision {
	if now.Sub(scheduledFor) > s.Grace {
		return ports.TriggerSkip
	}
	return ports.TriggerRun
}

// NoOverlap is the v1 OverlapPolicy: if the previous run is still active, skip.
type NoOverlap struct{}

func NewNoOverlap() *NoOverlap { return &NoOverlap{} }

func (NoOverlap) OnOverlap(_ ports.Job) ports.OverlapDecision {
	return ports.OverlapDecisionSkip
}
