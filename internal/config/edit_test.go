package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tmpCfg(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cronhub.toml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAddPreservesComments(t *testing.T) {
	p := tmpCfg(t, "version = 1\n\n# keep me\n[[job]]\nname=\"a\"\nschedule=\"daily\"\ncommand=\"x\"\n")
	if err := AddJob(p, NewJobSpec{Name: "b", Schedule: "hourly", Command: "y"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	s := string(data)
	if !strings.Contains(s, "# keep me") {
		t.Error("comment was not preserved by add")
	}
	if !strings.Contains(s, `name     = "b"`) {
		t.Errorf("new job not appended cleanly:\n%s", s)
	}
}

func TestAddRejectsDuplicate(t *testing.T) {
	p := tmpCfg(t, "version = 1\n[[job]]\nname=\"a\"\nschedule=\"daily\"\ncommand=\"x\"\n")
	err := AddJob(p, NewJobSpec{Name: "a", Schedule: "hourly", Command: "y"})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected duplicate error, got %v", err)
	}
}

func TestRemoveWritesBackupAndDropsJob(t *testing.T) {
	p := tmpCfg(t, "version = 1\n[[job]]\nname=\"a\"\nschedule=\"daily\"\ncommand=\"x\"\n[[job]]\nname=\"b\"\nschedule=\"hourly\"\ncommand=\"y\"\n")
	if _, err := RemoveJob(p, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p + ".bak"); err != nil {
		t.Error("backup not written")
	}
	jobs, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Name != "b" {
		t.Errorf("remove left wrong jobs: %+v", jobs)
	}
}

func TestUpdateChangesFields(t *testing.T) {
	p := tmpCfg(t, "version = 1\n[[job]]\nname=\"a\"\nschedule=\"daily\"\ncommand=\"x\"\n")
	if _, err := UpdateJob(p, NewJobSpec{Name: "a", Schedule: "hourly", Timeout: "5m"}); err != nil {
		t.Fatal(err)
	}
	jobs, _ := Load(p)
	if jobs[0].Schedule != "hourly" {
		t.Errorf("schedule not updated: %q", jobs[0].Schedule)
	}
	if jobs[0].Timeout.String() != "5m0s" {
		t.Errorf("timeout not updated: %v", jobs[0].Timeout)
	}
	if jobs[0].Command != "x" {
		t.Errorf("command should be unchanged: %q", jobs[0].Command)
	}
}
