// Package schedule: HumanParser understands a small, fixed set of readable
// schedule phrases and translates them into cron expressions, which the proven
// cron engine then evaluates. It is deliberately NOT open-ended natural language:
// every supported form is listed in supportedForms and anything else is rejected
// with a helpful error. For anything the grammar can't express, users fall back
// to raw cron syntax.
//
// Supported forms (case-insensitive):
//
//	every N seconds|minutes|hours          e.g. "every 30 seconds", "every 5 minutes", "every 2 hours"
//	every second|minute|hour               (N = 1)
//	hourly | daily | weekly | monthly | yearly
//	every day at TIME                      e.g. "every day at 9am", "every day at 21:00"
//	every DAY at TIME                      e.g. "every monday at 9am"
//	every weekday at TIME                  Mon–Fri
//	every weekend at TIME                  Sat–Sun
//	every DAY,DAY,... at TIME              e.g. "every mon,wed,fri at 6pm"
//	every month on the Nth at TIME         e.g. "every month on the 1st at midnight"
//
// TIME accepts: "9am", "9:30am", "21:00", "9pm", "midnight", "noon".
// DAY accepts full or 3-letter names: monday/mon ... sunday/sun.
package schedule

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/robfig/cron/v3"

	"github.com/aelaboussi/cronhub/internal/ports"
)

type HumanParser struct {
	// cronParser evaluates the 5-field expressions we translate to.
	cronParser cron.Parser
	// secondsParser evaluates 6-field expressions for sub-minute intervals.
	secondsParser cron.Parser
}

func NewHumanParser() *HumanParser {
	return &HumanParser{
		cronParser:    cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow),
		secondsParser: cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow),
	}
}

// supportedForms is shown to the user whenever parsing fails, so the grammar is
// always discoverable from an error message.
const supportedForms = `supported readable forms:
  every N seconds|minutes|hours       (e.g. "every 5 minutes", "every 2 hours")
  hourly | daily | weekly | monthly | yearly
  every day at TIME                   (e.g. "every day at 9am")
  every WEEKDAY at TIME               (e.g. "every monday at 9am")
  every weekday|weekend at TIME
  every mon,wed,fri at TIME
  every month on the Nth at TIME      (e.g. "every month on the 1st at midnight")
TIME: 9am, 9:30am, 21:00, noon, midnight
or use classic cron syntax: * * * * *`

func (p *HumanParser) Parse(spec string) (ports.Schedule, error) {
	s := strings.ToLower(strings.TrimSpace(spec))

	cronExpr, seconds, err := p.translate(s)
	if err != nil {
		return nil, err
	}

	if seconds {
		sched, err := p.secondsParser.Parse(cronExpr)
		if err != nil {
			return nil, fmt.Errorf("internal: translated %q -> %q: %w", spec, cronExpr, err)
		}
		return cronSchedule{sched}, nil
	}
	sched, err := p.cronParser.Parse(cronExpr)
	if err != nil {
		return nil, fmt.Errorf("internal: translated %q -> %q: %w", spec, cronExpr, err)
	}
	return cronSchedule{sched}, nil
}

// translate returns a cron expression, whether it is 6-field (seconds), or an
// error listing supported forms. It never guesses — an unrecognized phrase fails.
func (p *HumanParser) translate(s string) (expr string, seconds bool, err error) {
	// Word shortcuts.
	switch s {
	case "hourly":
		return "0 * * * *", false, nil
	case "daily":
		return "0 0 * * *", false, nil
	case "weekly":
		return "0 0 * * 0", false, nil
	case "monthly":
		return "0 0 1 * *", false, nil
	case "yearly", "annually":
		return "0 0 1 1 *", false, nil
	}

	if !strings.HasPrefix(s, "every ") {
		return "", false, unsupported(s)
	}
	rest := strings.TrimSpace(strings.TrimPrefix(s, "every "))

	// Interval forms: "every N unit" or "every unit".
	if expr, seconds, ok, err := parseInterval(rest); ok {
		return expr, seconds, err
	}

	// Time-of-day forms all contain " at ".
	if !strings.Contains(rest, " at ") {
		return "", false, unsupported(s)
	}
	parts := strings.SplitN(rest, " at ", 2)
	dayPart := strings.TrimSpace(parts[0])
	timePart := strings.TrimSpace(parts[1])

	min, hour, err := parseTime(timePart)
	if err != nil {
		return "", false, err
	}

	// "day" -> every day
	if dayPart == "day" {
		return fmt.Sprintf("%d %d * * *", min, hour), false, nil
	}
	// "weekday" / "weekend"
	if dayPart == "weekday" || dayPart == "weekdays" {
		return fmt.Sprintf("%d %d * * 1-5", min, hour), false, nil
	}
	if dayPart == "weekend" || dayPart == "weekends" {
		return fmt.Sprintf("%d %d * * 0,6", min, hour), false, nil
	}
	// "month on the Nth"
	if strings.HasPrefix(dayPart, "month on the ") {
		nth := strings.TrimSpace(strings.TrimPrefix(dayPart, "month on the "))
		dom, err := parseOrdinal(nth)
		if err != nil {
			return "", false, err
		}
		return fmt.Sprintf("%d %d %d * *", min, hour, dom), false, nil
	}
	// Weekday list: "mon,wed,fri" or a single "monday".
	dows, err := parseDays(dayPart)
	if err != nil {
		return "", false, err
	}
	return fmt.Sprintf("%d %d * * %s", min, hour, dows), false, nil
}

// parseInterval handles "every N seconds|minutes|hours" and "every second|minute|hour".
// ok=false means "not an interval form" (caller should try other forms).
func parseInterval(rest string) (expr string, seconds, ok bool, err error) {
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return "", false, false, nil
	}

	// singular: "minute" / "hour" / "second"
	if len(fields) == 1 {
		switch fields[0] {
		case "second":
			return "* * * * * *", true, true, nil
		case "minute":
			return "* * * * *", false, true, nil
		case "hour":
			return "0 * * * *", false, true, nil
		}
		return "", false, false, nil
	}

	// "N unit"
	if len(fields) == 2 {
		n, convErr := strconv.Atoi(fields[0])
		if convErr != nil {
			return "", false, false, nil // not an interval; let caller try time forms
		}
		if n < 1 {
			return "", false, true, fmt.Errorf("interval must be at least 1")
		}
		unit := strings.TrimSuffix(fields[1], "s") // seconds->second
		switch unit {
		case "second":
			if n > 59 {
				return "", false, true, fmt.Errorf("second interval must be 1-59; for larger use minutes")
			}
			return fmt.Sprintf("*/%d * * * * *", n), true, true, nil
		case "minute":
			if n > 59 {
				return "", false, true, fmt.Errorf("minute interval must be 1-59; for larger use hours")
			}
			return fmt.Sprintf("*/%d * * * *", n), false, true, nil
		case "hour":
			if n > 23 {
				return "", false, true, fmt.Errorf("hour interval must be 1-23; for larger use a daily time")
			}
			return fmt.Sprintf("0 */%d * * *", n), false, true, nil
		}
		return "", false, false, nil
	}
	return "", false, false, nil
}

// parseTime accepts "9am", "9:30am", "21:00", "9pm", "noon", "midnight".
func parseTime(t string) (min, hour int, err error) {
	t = strings.TrimSpace(t)
	switch t {
	case "midnight":
		return 0, 0, nil
	case "noon":
		return 0, 12, nil
	}

	ampm := ""
	if strings.HasSuffix(t, "am") {
		ampm, t = "am", strings.TrimSpace(strings.TrimSuffix(t, "am"))
	} else if strings.HasSuffix(t, "pm") {
		ampm, t = "pm", strings.TrimSpace(strings.TrimSuffix(t, "pm"))
	}

	hh, mm := t, "0"
	if strings.Contains(t, ":") {
		bits := strings.SplitN(t, ":", 2)
		hh, mm = bits[0], bits[1]
	}
	hour, err = strconv.Atoi(strings.TrimSpace(hh))
	if err != nil {
		return 0, 0, fmt.Errorf("could not read time %q; %s", t, timeHint)
	}
	min, err = strconv.Atoi(strings.TrimSpace(mm))
	if err != nil {
		return 0, 0, fmt.Errorf("could not read minutes in %q; %s", t, timeHint)
	}

	switch ampm {
	case "am":
		if hour == 12 {
			hour = 0
		}
	case "pm":
		if hour != 12 {
			hour += 12
		}
	}
	if hour < 0 || hour > 23 || min < 0 || min > 59 {
		return 0, 0, fmt.Errorf("time %02d:%02d out of range", hour, min)
	}
	return min, hour, nil
}

const timeHint = `use forms like "9am", "9:30am", "21:00", "noon", "midnight"`

var dayNames = map[string]int{
	"sunday": 0, "sun": 0,
	"monday": 1, "mon": 1,
	"tuesday": 2, "tue": 2, "tues": 2,
	"wednesday": 3, "wed": 3,
	"thursday": 4, "thu": 4, "thur": 4, "thurs": 4,
	"friday": 5, "fri": 5,
	"saturday": 6, "sat": 6,
}

// parseDays turns "mon,wed,fri" or "monday" into a cron day-of-week field "1,3,5".
func parseDays(s string) (string, error) {
	raw := strings.Split(s, ",")
	nums := make([]string, 0, len(raw))
	for _, d := range raw {
		d = strings.TrimSpace(d)
		n, ok := dayNames[d]
		if !ok {
			return "", fmt.Errorf("unknown day %q; use monday..sunday or mon..sun", d)
		}
		nums = append(nums, strconv.Itoa(n))
	}
	return strings.Join(nums, ","), nil
}

// parseOrdinal turns "1st", "2nd", "15th", or "1" into a day-of-month number.
func parseOrdinal(s string) (int, error) {
	s = strings.TrimSpace(s)
	for _, suf := range []string{"st", "nd", "rd", "th"} {
		s = strings.TrimSuffix(s, suf)
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("could not read day-of-month %q; use 1st..31st", s)
	}
	if n < 1 || n > 31 {
		return 0, fmt.Errorf("day-of-month %d out of range (1-31)", n)
	}
	return n, nil
}

func unsupported(s string) error {
	return fmt.Errorf("could not understand schedule %q\n%s", s, supportedForms)
}

// compile-time reminder that HumanParser satisfies the port.
var _ ports.ScheduleParser = (*HumanParser)(nil)

// (cronSchedule is defined in cron.go and reused here.)
