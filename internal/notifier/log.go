// Package notifier provides the v1 Notifier: write outcomes to the log.
// Email/Slack/webhook notifiers are the classic community contribution — each is
// a small impl of ports.Notifier.
package notifier

import (
	"log"

	"github.com/aelaboussi/cronhub/internal/ports"
)

type Log struct{}

func NewLog() *Log { return &Log{} }

func (Log) Name() string { return "log" }

func (Log) Notify(ev ports.NotifyEvent) error {
	r := ev.Result
	if r.Err != nil {
		log.Printf("job %q FAILED to run: %v", ev.JobName, r.Err)
		return nil
	}
	if r.ExitCode != 0 {
		log.Printf("job %q exited %d in %s", ev.JobName, r.ExitCode, r.Duration)
		return nil
	}
	log.Printf("job %q ok in %s", ev.JobName, r.Duration)
	return nil
}

var _ ports.Notifier = (*Log)(nil)
