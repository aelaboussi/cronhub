// Package crontab parses a classic Unix crontab into cronhub jobs and renders
// them as a cronhub TOML config. This is the zero-friction adoption hook: an
// existing cron user points cronhub at their crontab and it runs unchanged.
//
// Supported per line:
//   - comments (# ...) and blank lines: ignored
//   - environment assignments (KEY=VALUE): captured, emitted into [defaults]
//     only for recognized keys (currently CRON_TZ -> timezone); others ignored
//     with a note, since arbitrary env handling is a later executor concern
//   - standard 5-field entries: "m h dom mon dow  command..."
//   - @-shortcuts: @yearly/@annually, @monthly, @weekly, @daily/@midnight,
//     @hourly  (@reboot is NOT a time schedule and is reported as unsupported)
//
// Job names are synthesized (job1, job2, ...) since crontab has no names; a
// trailing "# name: X" comment on the entry line overrides that.
package crontab

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// Entry is one parsed crontab job, pre-TOML.
type Entry struct {
	Name     string
	Schedule string // 5-field cron, already normalized from any @shortcut
	Command  string
}

// Result is the outcome of parsing a whole crontab.
type Result struct {
	Entries  []Entry
	Timezone string   // from CRON_TZ, if present
	Warnings []string // non-fatal: unsupported lines, ignored env vars
}

var shortcuts = map[string]string{
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
	"@monthly":  "0 0 1 * *",
	"@weekly":   "0 0 * * 0",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@hourly":   "0 * * * *",
}

// Parse reads a crontab and returns structured entries plus warnings.
func Parse(r io.Reader) (*Result, error) {
	res := &Result{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	lineNo := 0
	jobN := 0
	for sc.Scan() {
		lineNo++
		raw := sc.Text()
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Environment assignment: KEY=VALUE with no spaces around a leading token.
		if isEnvAssignment(line) {
			k, v := splitEnv(line)
			switch strings.ToUpper(k) {
			case "CRON_TZ", "TZ":
				res.Timezone = strings.Trim(v, `"'`)
			default:
				res.Warnings = append(res.Warnings,
					fmt.Sprintf("line %d: ignoring env var %q (arbitrary env is not yet applied to jobs)", lineNo, k))
			}
			continue
		}

		if strings.HasPrefix(line, "@reboot") {
			res.Warnings = append(res.Warnings,
				fmt.Sprintf("line %d: @reboot is not a time schedule and is unsupported; skipped", lineNo))
			continue
		}

		schedule, command, name, err := parseEntry(line)
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("line %d: %v; skipped", lineNo, err))
			continue
		}
		jobN++
		if name == "" {
			name = fmt.Sprintf("job%d", jobN)
		}
		res.Entries = append(res.Entries, Entry{Name: name, Schedule: schedule, Command: command})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(res.Entries) == 0 {
		return res, fmt.Errorf("no schedulable entries found")
	}
	return res, nil
}

// parseEntry handles both @shortcut and 5-field forms, plus an optional
// trailing "# name: X" naming comment.
func parseEntry(line string) (schedule, command, name string, err error) {
	// Extract optional naming comment: "... # name: backup"
	if i := strings.LastIndex(line, "#"); i >= 0 {
		afterHash := strings.TrimSpace(line[i+1:])
		if rest, ok := cutPrefixFold(afterHash, "name:"); ok {
			name = strings.TrimSpace(rest)
			line = strings.TrimSpace(line[:i])
		}
	}

	if strings.HasPrefix(line, "@") {
		fields := strings.Fields(line)
		sc, ok := shortcuts[strings.ToLower(fields[0])]
		if !ok {
			return "", "", "", fmt.Errorf("unknown shortcut %q", fields[0])
		}
		if len(fields) < 2 {
			return "", "", "", fmt.Errorf("shortcut %q has no command", fields[0])
		}
		return sc, strings.TrimSpace(strings.TrimPrefix(line, fields[0])), name, nil
	}

	// 5-field: split into exactly 5 schedule fields + the command remainder.
	fields := strings.Fields(line)
	if len(fields) < 6 {
		return "", "", "", fmt.Errorf("expected 5 schedule fields + command, got %d fields", len(fields))
	}
	schedule = strings.Join(fields[:5], " ")
	// Command is the remainder of the original line after the 5th field, to
	// preserve internal spacing/quotes.
	command = commandRemainder(line, fields[:5])
	if command == "" {
		return "", "", "", fmt.Errorf("empty command")
	}
	return schedule, command, name, nil
}

// commandRemainder returns everything after the 5 schedule fields, preserving
// the original spacing of the command.
func commandRemainder(line string, schedFields []string) string {
	idx := 0
	for _, f := range schedFields {
		j := strings.Index(line[idx:], f)
		if j < 0 {
			return ""
		}
		idx += j + len(f)
	}
	return strings.TrimSpace(line[idx:])
}

func isEnvAssignment(line string) bool {
	eq := strings.Index(line, "=")
	if eq <= 0 {
		return false
	}
	key := strings.TrimSpace(line[:eq])
	// A key is a single token with no spaces; distinguishes "FOO=bar" from a
	// cron field line that happens to contain '='.
	return !strings.ContainsAny(key, " \t")
}

func splitEnv(line string) (key, val string) {
	eq := strings.Index(line, "=")
	return strings.TrimSpace(line[:eq]), strings.TrimSpace(line[eq+1:])
}

// cutPrefixFold reports whether s starts with prefix (case-insensitive) and, if
// so, returns the remainder with its ORIGINAL case preserved.
func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) < len(prefix) {
		return "", false
	}
	if strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}
	return "", false
}

// RenderTOML turns a parsed Result into a cronhub config file body.
func RenderTOML(res *Result) string {
	var b strings.Builder
	b.WriteString("version = 1\n\n")
	b.WriteString("# Imported from an existing crontab by `cronhub import-crontab`.\n")
	b.WriteString("# Review job names and commands before running.\n\n")

	if res.Timezone != "" {
		b.WriteString("[defaults]\n")
		fmt.Fprintf(&b, "timezone = %q\n\n", res.Timezone)
	}

	for _, e := range res.Entries {
		b.WriteString("[[job]]\n")
		fmt.Fprintf(&b, "name     = %q\n", e.Name)
		fmt.Fprintf(&b, "schedule = %q\n", e.Schedule)
		fmt.Fprintf(&b, "command  = %q\n\n", e.Command)
	}
	return b.String()
}
