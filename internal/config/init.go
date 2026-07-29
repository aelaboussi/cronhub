package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// StarterTOML is the commented config `cronhub init` writes for a new user.
const StarterTOML = `version = 1

# cronhub configuration.
# A job needs only name, schedule, and command. Everything else is optional
# and falls back to documented defaults.

[defaults]
# timezone = "UTC"          # IANA name, e.g. "Africa/Casablanca"
# notify   = ["log"]

[[job]]
name     = "example"
schedule = "*/5 * * * *"    # standard cron: every 5 minutes
command  = "echo hello from cronhub"
# on_overlap = "skip"       # skip | queue | parallel | kill
# on_missed  = "skip"       # skip | catch_up_once | catch_up_all
# timeout    = "30m"
`

// DefaultDir returns the OS-native cronhub config directory
// (~/Library/Application Support/cronhub on macOS, ~/.config/cronhub on Linux,
// %AppData%\cronhub on Windows). The user never needs to know which.
func DefaultDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "cronhub"), nil
}

// DefaultPath returns the default config file path inside DefaultDir.
func DefaultPath() (string, error) {
	dir, err := DefaultDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cronhub.toml"), nil
}

// Init writes a starter config to the default location, creating the directory.
// It refuses to overwrite an existing config unless force is true. Returns the
// path written.
func Init(force bool) (string, error) {
	dir, err := DefaultDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "cronhub.toml")
	if !force {
		if _, err := os.Stat(path); err == nil {
			return path, fmt.Errorf("config already exists at %s (use --force to overwrite)", path)
		}
	}
	if err := os.WriteFile(path, []byte(StarterTOML), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// WriteConfig writes arbitrary config body to the default path, creating the
// directory. Refuses to overwrite unless force is true. Used by import-crontab.
func WriteConfig(body string, force bool) (string, error) {
	dir, err := DefaultDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "cronhub.toml")
	if !force {
		if _, err := os.Stat(path); err == nil {
			return path, fmt.Errorf("config already exists at %s (use --force to overwrite)", path)
		}
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return "", err
	}
	return path, nil
}
