// Package schedule provides the v1 ScheduleParser: standard cron syntax.
// Later impls (human syntax, intervals) satisfy the same ports.ScheduleParser.
package schedule

import (
	"time"

	"github.com/robfig/cron/v3"

	"github.com/aelaboussi/cronhub/internal/ports"
)

type CronParser struct {
	parser cron.Parser
}

func NewCronParser() *CronParser {
	// Standard 5-field cron (minute hour dom month dow), matching crontab import.
	return &CronParser{
		parser: cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow),
	}
}

func (p *CronParser) Parse(spec string) (ports.Schedule, error) {
	sched, err := p.parser.Parse(spec)
	if err != nil {
		return nil, err
	}
	return cronSchedule{sched}, nil
}

type cronSchedule struct{ s cron.Schedule }

func (c cronSchedule) Next(after time.Time) time.Time { return c.s.Next(after) }
