package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// NewJobSpec describes a job to add or update. Empty optional fields are left at
// their defaults (for add) or left unchanged (for update).
type NewJobSpec struct {
	Name     string
	Schedule string
	Command  string
	// Optional overrides; empty string means "not set".
	OnOverlap string
	OnMissed  string
	Timeout   string
	Timezone  string
	Notify    []string
}

// JobExists reports whether a job with the given name is already in the file.
func JobExists(path, name string) (bool, error) {
	var f File
	if _, err := toml.DecodeFile(path, &f); err != nil {
		return false, err
	}
	for _, j := range f.Jobs {
		if j.Name == name {
			return true, nil
		}
	}
	return false, nil
}

// AddJob appends a new [[job]] block to the end of the config file as text,
// leaving everything already in the file untouched (comments, formatting, and
// ordering are all preserved). It refuses to add a duplicate name.
func AddJob(path string, spec NewJobSpec) error {
	exists, err := JobExists(path, spec.Name)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("a job named %q already exists (use `cronhub edit` to change it)", spec.Name)
	}

	existing, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	block := renderJobBlock(spec)

	// Ensure exactly one blank line between existing content and the new block.
	body := string(existing)
	body = strings.TrimRight(body, "\n") + "\n\n" + block

	return os.WriteFile(path, []byte(body), 0o644)
}

// RemoveJob rewrites the config without the named job. Because this requires
// re-serializing the file, a backup is written to <path>.bak first, and the
// caller is told if comments may have been lost.
func RemoveJob(path, name string) (hadComments bool, err error) {
	var f File
	meta, err := toml.DecodeFile(path, &f)
	if err != nil {
		return false, err
	}
	_ = meta

	idx := -1
	for i, j := range f.Jobs {
		if j.Name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false, fmt.Errorf("no job named %q", name)
	}

	hadComments = fileHasComments(path)

	if err := backup(path); err != nil {
		return hadComments, err
	}

	f.Jobs = append(f.Jobs[:idx], f.Jobs[idx+1:]...)
	return hadComments, writeFile(path, &f)
}

// UpdateJob changes fields of an existing job and rewrites the file (with a
// backup). Only non-empty fields in spec are applied.
func UpdateJob(path string, spec NewJobSpec) (hadComments bool, err error) {
	var f File
	if _, err := toml.DecodeFile(path, &f); err != nil {
		return false, err
	}

	idx := -1
	for i, j := range f.Jobs {
		if j.Name == spec.Name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false, fmt.Errorf("no job named %q", spec.Name)
	}

	hadComments = fileHasComments(path)
	if err := backup(path); err != nil {
		return hadComments, err
	}

	j := &f.Jobs[idx]
	if spec.Schedule != "" {
		j.Schedule = spec.Schedule
	}
	if spec.Command != "" {
		j.Command = spec.Command
	}
	if spec.OnOverlap != "" {
		j.OnOverlap = &spec.OnOverlap
	}
	if spec.OnMissed != "" {
		j.OnMissed = &spec.OnMissed
	}
	if spec.Timeout != "" {
		j.Timeout = &spec.Timeout
	}
	if spec.Timezone != "" {
		j.Timezone = &spec.Timezone
	}
	if spec.Notify != nil {
		j.Notify = spec.Notify
	}

	return hadComments, writeFile(path, &f)
}

// renderJobBlock produces the TOML text for one [[job]], including only the
// optional fields that were set.
func renderJobBlock(spec NewJobSpec) string {
	var b strings.Builder
	b.WriteString("[[job]]\n")
	fmt.Fprintf(&b, "name     = %q\n", spec.Name)
	fmt.Fprintf(&b, "schedule = %q\n", spec.Schedule)
	fmt.Fprintf(&b, "command  = %q\n", spec.Command)
	if spec.OnOverlap != "" {
		fmt.Fprintf(&b, "on_overlap = %q\n", spec.OnOverlap)
	}
	if spec.OnMissed != "" {
		fmt.Fprintf(&b, "on_missed  = %q\n", spec.OnMissed)
	}
	if spec.Timeout != "" {
		fmt.Fprintf(&b, "timeout    = %q\n", spec.Timeout)
	}
	if spec.Timezone != "" {
		fmt.Fprintf(&b, "timezone   = %q\n", spec.Timezone)
	}
	if len(spec.Notify) > 0 {
		quoted := make([]string, len(spec.Notify))
		for i, n := range spec.Notify {
			quoted[i] = fmt.Sprintf("%q", n)
		}
		fmt.Fprintf(&b, "notify     = [%s]\n", strings.Join(quoted, ", "))
	}
	return b.String()
}

// writeFile serializes the whole File back to TOML. It uses a write-only view
// with omitempty so an empty [defaults] or absent [[notifier]] section is not
// emitted (which would otherwise appear as a confusing empty block the user
// never wrote).
func writeFile(path string, f *File) error {
	type outLayer struct {
		OnOverlap *string  `toml:"on_overlap,omitempty"`
		OnMissed  *string  `toml:"on_missed,omitempty"`
		Timeout   *string  `toml:"timeout,omitempty"`
		Notify    []string `toml:"notify,omitempty"`
		Timezone  *string  `toml:"timezone,omitempty"`
	}
	type outJob struct {
		Name     string `toml:"name"`
		Schedule string `toml:"schedule"`
		Command  string `toml:"command"`
		outLayer
	}
	type outFile struct {
		Version   int           `toml:"version"`
		Defaults  *outLayer     `toml:"defaults,omitempty"`
		Notifiers []NotifierDef `toml:"notifier,omitempty"`
		Jobs      []outJob      `toml:"job"`
	}

	conv := func(l Layer) outLayer {
		return outLayer{OnOverlap: l.OnOverlap, OnMissed: l.OnMissed, Timeout: l.Timeout, Notify: l.Notify, Timezone: l.Timezone}
	}

	out := outFile{Version: f.Version, Notifiers: f.Notifiers}
	// include [defaults] only if it has any set field
	d := conv(f.Defaults)
	if d.OnOverlap != nil || d.OnMissed != nil || d.Timeout != nil || d.Timezone != nil || len(d.Notify) > 0 {
		out.Defaults = &d
	}
	for _, j := range f.Jobs {
		out.Jobs = append(out.Jobs, outJob{Name: j.Name, Schedule: j.Schedule, Command: j.Command, outLayer: conv(j.Layer)})
	}

	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return toml.NewEncoder(file).Encode(out)
}

// backup copies path to path+".bak".
func backup(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path+".bak", data, 0o644)
}

// fileHasComments reports whether the file contains any TOML comment lines, so
// the caller can warn that a rewrite will drop them.
func fileHasComments(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			return true
		}
	}
	return false
}
