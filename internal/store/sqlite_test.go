package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/aelaboussi/cronhub/internal/ports"
)

func TestSaveLoadAndHistory(t *testing.T) {
	dbFile := filepath.Join(t.TempDir(), "test.db")
	st, err := Open(dbFile)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	// Save a job with a multi-notifier list and confirm it round-trips.
	job := ports.Job{
		Name:      "backup",
		Schedule:  "0 3 * * *",
		Command:   "/opt/backup.sh",
		OnOverlap: ports.OverlapSkip,
		OnMissed:  ports.MissedSkip,
		Timeout:   30 * time.Minute,
		Timezone:  "UTC",
		Notify:    []string{"log", "alerts"},
	}
	if err := st.SaveJob(job); err != nil {
		t.Fatalf("save: %v", err)
	}
	jobs, err := st.LoadJobs()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	if got := jobs[0].Notify; len(got) != 2 || got[0] != "log" || got[1] != "alerts" {
		t.Errorf("notify list did not round-trip: %v", got)
	}

	// Record three runs at increasing times; ReadHistory must return newest-first.
	base := time.Date(2026, 1, 1, 3, 0, 0, 0, time.UTC)
	for i, ok := range []bool{true, false, true} {
		rec := ports.RunRecord{
			JobName:  "backup",
			Started:  base.Add(time.Duration(i) * time.Hour),
			Duration: time.Second,
			ExitCode: map[bool]int{true: 0, false: 1}[ok],
			Success:  ok,
			Stderr:   map[bool]string{true: "", false: "boom"}[ok],
		}
		if err := st.RecordRun(rec); err != nil {
			t.Fatalf("record run %d: %v", i, err)
		}
	}

	hist, err := st.ReadHistory("backup", 10)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(hist) != 3 {
		t.Fatalf("expected 3 runs, got %d", len(hist))
	}
	// newest first => the last recorded (i=2, success) is first
	if !hist[0].Success || !hist[0].Started.Equal(base.Add(2*time.Hour)) {
		t.Errorf("history not newest-first: %+v", hist[0])
	}
	// the middle failure should have captured stderr
	if hist[1].Success || hist[1].Stderr != "boom" {
		t.Errorf("failure record wrong: %+v", hist[1])
	}

	// limit is respected
	hist2, _ := st.ReadHistory("backup", 2)
	if len(hist2) != 2 {
		t.Errorf("limit not respected: got %d", len(hist2))
	}
}
