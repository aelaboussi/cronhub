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

func TestRunningState(t *testing.T) {
	dbFile := filepath.Join(t.TempDir(), "run.db")
	st, err := Open(dbFile)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	// nothing running initially
	r, err := st.ListRunning()
	if err != nil {
		t.Fatal(err)
	}
	if len(r) != 0 {
		t.Fatalf("expected 0 running, got %d", len(r))
	}

	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	if err := st.MarkRunning("job1", start); err != nil {
		t.Fatal(err)
	}
	r, _ = st.ListRunning()
	if len(r) != 1 || r[0].JobName != "job1" || !r[0].StartedAt.Equal(start) {
		t.Fatalf("running state wrong: %+v", r)
	}

	// marking again updates rather than duplicating
	if err := st.MarkRunning("job1", start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	r, _ = st.ListRunning()
	if len(r) != 1 {
		t.Fatalf("mark should upsert, got %d rows", len(r))
	}

	if err := st.ClearRunning("job1"); err != nil {
		t.Fatal(err)
	}
	r, _ = st.ListRunning()
	if len(r) != 0 {
		t.Fatalf("expected cleared, got %d", len(r))
	}
}

// TestConcurrentAccess simulates the daemon (writer) and `status` (reader)
// hitting the same database at once, which previously produced SQLITE_BUSY.
// With WAL + busy_timeout + a single connection, it should not error.
func TestConcurrentAccess(t *testing.T) {
	dbFile := filepath.Join(t.TempDir(), "concurrent.db")
	st, err := Open(dbFile)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	if err := st.SaveJob(ports.Job{Name: "j", Schedule: "* * * * *", Command: "x", OnOverlap: ports.OverlapSkip, OnMissed: ports.MissedSkip, Timezone: "UTC", Notify: []string{"log"}}); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 2)

	// writer: mark running, record runs, clear running, repeatedly
	go func() {
		for i := 0; i < 200; i++ {
			if err := st.MarkRunning("j", time.Now()); err != nil {
				done <- err
				return
			}
			if err := st.RecordRun(ports.RunRecord{JobName: "j", Started: time.Now(), Success: true}); err != nil {
				done <- err
				return
			}
			if err := st.ClearRunning("j"); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()

	// reader: list running and read history, repeatedly (like `status --watch`)
	go func() {
		for i := 0; i < 200; i++ {
			if _, err := st.ListRunning(); err != nil {
				done <- err
				return
			}
			if _, err := st.ReadHistory("j", 5); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()

	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatalf("concurrent access errored (SQLITE_BUSY not fixed?): %v", err)
		}
	}
}
