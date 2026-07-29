package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTmp(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "c.toml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNotifierRouting(t *testing.T) {
	good := `version = 1
[[notifier]]
name = "alerts"
type = "webhook"
url  = "http://x/y"
[[job]]
name="a"
schedule="* * * * *"
command="echo hi"
notify=["log","alerts"]
`
	cfg, err := LoadConfig(writeTmp(t, good))
	if err != nil {
		t.Fatalf("good config errored: %v", err)
	}
	if len(cfg.Notifiers) != 1 || cfg.Notifiers[0].Name != "alerts" {
		t.Errorf("notifier not parsed: %+v", cfg.Notifiers)
	}
	if got := cfg.Jobs[0].Notify; strings.Join(got, ",") != "log,alerts" {
		t.Errorf("job notify = %v", got)
	}
}

func TestUndeclaredNotifierFails(t *testing.T) {
	bad := `version = 1
[[job]]
name="a"
schedule="* * * * *"
command="echo hi"
notify=["ghost"]
`
	_, err := LoadConfig(writeTmp(t, bad))
	if err == nil || !strings.Contains(err.Error(), "undeclared notifier") {
		t.Errorf("expected undeclared-notifier error, got: %v", err)
	}
}

func TestWebhookMissingURLFails(t *testing.T) {
	bad := `version = 1
[[notifier]]
name = "alerts"
type = "webhook"
[[job]]
name="a"
schedule="* * * * *"
command="echo hi"
`
	_, err := LoadConfig(writeTmp(t, bad))
	if err == nil || !strings.Contains(err.Error(), "missing url") {
		t.Errorf("expected missing-url error, got: %v", err)
	}
}
