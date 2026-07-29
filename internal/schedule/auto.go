// Package schedule: AutoParser is the ScheduleParser the daemon actually uses.
// It routes each spec to either the human parser or the classic cron parser, so
// users can write "every monday at 9am" OR "0 9 * * 1" and both just work. The
// core depends only on ports.ScheduleParser and is unaware of the split.
package schedule

import (
	"strings"

	"github.com/aelaboussi/cronhub/internal/ports"
)

type AutoParser struct {
	human *HumanParser
	cron  *CronParser
}

func NewAutoParser() *AutoParser {
	return &AutoParser{human: NewHumanParser(), cron: NewCronParser()}
}

// Parse chooses a parser by the first character of the spec:
//   - starts with a letter  -> human syntax ("every ...", "daily", ...)
//   - otherwise             -> classic cron ("*", digits, "@", "?")
//
// This is unambiguous because cron fields never begin with a letter and human
// phrases always do.
func (a *AutoParser) Parse(spec string) (ports.Schedule, error) {
	trimmed := strings.TrimSpace(spec)
	if trimmed == "" {
		return a.cron.Parse(spec) // let cron produce the canonical empty-spec error
	}
	c := trimmed[0]
	isLetter := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
	if isLetter {
		return a.human.Parse(trimmed)
	}
	return a.cron.Parse(trimmed)
}

var _ ports.ScheduleParser = (*AutoParser)(nil)
