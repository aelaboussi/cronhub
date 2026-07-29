// Package config loads a TOML file into in-memory structs and resolves the
// layered defaults (compiled-in -> [defaults] table -> per-job) into fully
// populated ports.Job values. The core sees only ports.Job — never the file
// format. Swapping to YAML later is a second loader producing the same structs.
package config

import (
	"fmt"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/aelaboussi/cronhub/internal/ports"
)

// SchemaVersion is the config format version this build understands.
const SchemaVersion = 1

// File mirrors the on-disk TOML. Optional fields are pointers/zero so we can
// tell "unset" from "explicitly set", which is what layering needs.
type File struct {
	Version   int           `toml:"version"`
	Defaults  Layer         `toml:"defaults"`
	Jobs      []JobEntry    `toml:"job"`
	Notifiers []NotifierDef `toml:"notifier"`
}

// NotifierDef declares a named notifier instance a job can reference by name in
// its `notify` list. The "log" notifier is always available implicitly and need
// not be declared. Type-specific fields (e.g. url) are validated by the wiring
// layer in main.go, not here, so new notifier types need no config-package change.
type NotifierDef struct {
	Name         string `toml:"name"`          // reference name, e.g. "alerts"
	Type         string `toml:"type"`          // "log" | "webhook" | ...
	URL          string `toml:"url"`           // webhook: POST target
	FailuresOnly *bool  `toml:"failures_only"` // webhook: only notify on failure
}

// Layer holds optional policy fields shared by [defaults] and each [[job]].
type Layer struct {
	OnOverlap *string  `toml:"on_overlap"`
	OnMissed  *string  `toml:"on_missed"`
	Timeout   *string  `toml:"timeout"` // duration string, e.g. "30m"
	Notify    []string `toml:"notify"`
	Timezone  *string  `toml:"timezone"`
}

type JobEntry struct {
	Name     string `toml:"name"`
	Schedule string `toml:"schedule"`
	Command  string `toml:"command"`
	Layer           // embedded optional overrides
}

// compiledDefaults are the promises documented in ARCHITECTURE.md §6.
// An empty config runs correctly on these.
func compiledDefaults() ports.Job {
	return ports.Job{
		OnOverlap: ports.OverlapSkip,
		OnMissed:  ports.MissedSkip,
		Timeout:   0,
		Notify:    []string{"log"},
		Timezone:  "UTC",
	}
}

// Load parses the TOML file, validates it, and returns fully-resolved jobs.
// Config is the fully-resolved configuration: jobs plus declared notifier
// instances. The core consumes Jobs; main.go builds notifiers from Notifiers.
type Config struct {
	Jobs      []ports.Job
	Notifiers []NotifierDef
}

// Load returns just the resolved jobs (used by `list`). Delegates to LoadConfig.
func Load(path string) ([]ports.Job, error) {
	cfg, err := LoadConfig(path)
	if err != nil {
		return nil, err
	}
	return cfg.Jobs, nil
}

// LoadConfig parses the TOML file, validates it, and returns the resolved jobs
// and notifier definitions. It fails loud: any malformed field is an error
// before the daemon starts.
func LoadConfig(path string) (*Config, error) {
	var f File
	meta, err := toml.DecodeFile(path, &f)
	if err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		return nil, fmt.Errorf("config: unknown keys: %v", undecoded)
	}
	if f.Version != SchemaVersion {
		return nil, fmt.Errorf("config: version %d not supported (this build expects %d)", f.Version, SchemaVersion)
	}
	if len(f.Jobs) == 0 {
		return nil, fmt.Errorf("config: no [[job]] entries defined")
	}

	// Validate notifier declarations and collect known names ("log" is implicit).
	known := map[string]bool{"log": true}
	seenNotifier := map[string]bool{}
	for i, nd := range f.Notifiers {
		if nd.Name == "" {
			return nil, fmt.Errorf("config: notifier #%d has no name", i+1)
		}
		if nd.Name == "log" {
			return nil, fmt.Errorf("config: notifier name %q is reserved", nd.Name)
		}
		if seenNotifier[nd.Name] {
			return nil, fmt.Errorf("config: duplicate notifier name %q", nd.Name)
		}
		seenNotifier[nd.Name] = true
		switch nd.Type {
		case "webhook":
			if nd.URL == "" {
				return nil, fmt.Errorf("config: notifier %q (webhook) missing url", nd.Name)
			}
		case "log":
			return nil, fmt.Errorf("config: notifier %q may not redeclare type \"log\"", nd.Name)
		case "":
			return nil, fmt.Errorf("config: notifier %q missing type", nd.Name)
		default:
			return nil, fmt.Errorf("config: notifier %q has unknown type %q", nd.Name, nd.Type)
		}
		known[nd.Name] = true
	}

	// Layer 1 -> 2: compiled defaults overlaid by the [defaults] table.
	base := compiledDefaults()
	if err := applyLayer(&base, f.Defaults); err != nil {
		return nil, fmt.Errorf("config: [defaults]: %w", err)
	}

	// Layer 2 -> 3: per-job overrides.
	jobs := make([]ports.Job, 0, len(f.Jobs))
	seen := map[string]bool{}
	for i, je := range f.Jobs {
		if je.Name == "" {
			return nil, fmt.Errorf("config: job #%d has no name", i+1)
		}
		if seen[je.Name] {
			return nil, fmt.Errorf("config: duplicate job name %q", je.Name)
		}
		seen[je.Name] = true
		if je.Schedule == "" {
			return nil, fmt.Errorf("config: job %q missing schedule", je.Name)
		}
		if je.Command == "" {
			return nil, fmt.Errorf("config: job %q missing command", je.Name)
		}

		job := base // copy resolved defaults
		job.Name = je.Name
		job.Schedule = je.Schedule
		job.Command = je.Command
		if err := applyLayer(&job, je.Layer); err != nil {
			return nil, fmt.Errorf("config: job %q: %w", je.Name, err)
		}
		// Every notifier a job references must be declared (or be "log").
		for _, n := range job.Notify {
			if !known[n] {
				return nil, fmt.Errorf("config: job %q references undeclared notifier %q", je.Name, n)
			}
		}
		jobs = append(jobs, job)
	}
	return &Config{Jobs: jobs, Notifiers: f.Notifiers}, nil
}

// applyLayer overlays any set fields of a Layer onto a resolved Job, validating
// enum and duration values as it goes.
func applyLayer(job *ports.Job, l Layer) error {
	if l.OnOverlap != nil {
		m := ports.OverlapMode(*l.OnOverlap)
		switch m {
		case ports.OverlapSkip, ports.OverlapQueue, ports.OverlapParallel, ports.OverlapKill:
			job.OnOverlap = m
		default:
			return fmt.Errorf("invalid on_overlap %q", *l.OnOverlap)
		}
	}
	if l.OnMissed != nil {
		m := ports.MissedMode(*l.OnMissed)
		switch m {
		case ports.MissedSkip, ports.MissedCatchUpOnce, ports.MissedCatchUpAll:
			job.OnMissed = m
		default:
			return fmt.Errorf("invalid on_missed %q", *l.OnMissed)
		}
	}
	if l.Timeout != nil {
		d, err := time.ParseDuration(*l.Timeout)
		if err != nil {
			return fmt.Errorf("invalid timeout %q: %w", *l.Timeout, err)
		}
		job.Timeout = d
	}
	if l.Notify != nil {
		job.Notify = l.Notify
	}
	if l.Timezone != nil {
		if _, err := time.LoadLocation(*l.Timezone); err != nil {
			return fmt.Errorf("invalid timezone %q: %w", *l.Timezone, err)
		}
		job.Timezone = *l.Timezone
	}
	return nil
}
