// Package store keeps jobs and run history in a single SQLite file. It uses
// modernc.org/sqlite, a pure-Go driver, so there's no cgo and cross-compiling
// stays simple. A networked backend (e.g. Postgres) for running cronhub across
// several machines would be a separate implementation of the same interface.
package store

import (
	"database/sql"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/aelaboussi/cronhub/internal/ports"
)

type SQLite struct {
	db *sql.DB
}

func Open(path string) (*SQLite, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// Serialize access through a single connection. For a scheduler this is
	// plenty of throughput, and it makes the per-connection busy_timeout below
	// deterministic (Go's sql pool would otherwise apply PRAGMAs to whichever
	// pooled connection served the call).
	db.SetMaxOpenConns(1)
	// WAL mode lets readers (e.g. `cronhub status`) and the writer (the running
	// daemon) work at the same time without blocking each other. busy_timeout
	// makes a connection wait up to 5s for a lock instead of failing instantly
	// with SQLITE_BUSY. Applied as explicit PRAGMAs so it works regardless of
	// driver DSN quirks.
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA busy_timeout=5000;",
		"PRAGMA synchronous=NORMAL;",
	} {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	s := &SQLite{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLite) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS jobs (
	name       TEXT PRIMARY KEY,
	schedule   TEXT NOT NULL,
	command    TEXT NOT NULL,
	on_overlap TEXT NOT NULL,
	on_missed  TEXT NOT NULL,
	timeout_ns INTEGER NOT NULL,
	timezone   TEXT NOT NULL,
	notify     TEXT NOT NULL DEFAULT 'log'
);
CREATE TABLE IF NOT EXISTS runs (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	job_name    TEXT NOT NULL,
	started     INTEGER NOT NULL,   -- unix nanoseconds
	duration_ns INTEGER NOT NULL,
	exit_code   INTEGER NOT NULL,
	success     INTEGER NOT NULL,
	stdout      TEXT,
	stderr      TEXT
);
CREATE INDEX IF NOT EXISTS idx_runs_job ON runs(job_name, started DESC);
CREATE TABLE IF NOT EXISTS running (
	job_name   TEXT PRIMARY KEY,
	started_at INTEGER NOT NULL   -- unix nanoseconds
);`)
	if err != nil {
		return err
	}
	// Add columns introduced after a database may have first been created, so
	// an older cronhub's database picks up new fields without a manual step.
	return s.ensureColumn("jobs", "notify", "TEXT NOT NULL DEFAULT 'log'")
}

// ensureColumn adds a column to a table if it isn't already there.
func (s *SQLite) ensureColumn(table, column, decl string) error {
	rows, err := s.db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid       int
			name, typ string
			notnull   int
			dflt      any
			pk        int
		)
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = s.db.Exec("ALTER TABLE " + table + " ADD COLUMN " + column + " " + decl)
	return err
}

func (s *SQLite) SaveJob(j ports.Job) error {
	notify := strings.Join(j.Notify, ",")
	if notify == "" {
		notify = "log"
	}
	_, err := s.db.Exec(`
INSERT INTO jobs (name, schedule, command, on_overlap, on_missed, timeout_ns, timezone, notify)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(name) DO UPDATE SET
	schedule=excluded.schedule, command=excluded.command,
	on_overlap=excluded.on_overlap, on_missed=excluded.on_missed,
	timeout_ns=excluded.timeout_ns, timezone=excluded.timezone,
	notify=excluded.notify`,
		j.Name, j.Schedule, j.Command, string(j.OnOverlap), string(j.OnMissed),
		int64(j.Timeout), j.Timezone, notify)
	return err
}

func (s *SQLite) LoadJobs() ([]ports.Job, error) {
	rows, err := s.db.Query(`SELECT name, schedule, command, on_overlap, on_missed, timeout_ns, timezone, notify FROM jobs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []ports.Job
	for rows.Next() {
		var j ports.Job
		var overlap, missed, notify string
		var timeoutNs int64
		if err := rows.Scan(&j.Name, &j.Schedule, &j.Command, &overlap, &missed, &timeoutNs, &j.Timezone, &notify); err != nil {
			return nil, err
		}
		j.OnOverlap = ports.OverlapMode(overlap)
		j.OnMissed = ports.MissedMode(missed)
		j.Timeout = time.Duration(timeoutNs)
		if notify == "" {
			j.Notify = []string{"log"}
		} else {
			j.Notify = strings.Split(notify, ",")
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

func (s *SQLite) RecordRun(r ports.RunRecord) error {
	success := 0
	if r.Success {
		success = 1
	}
	_, err := s.db.Exec(`
INSERT INTO runs (job_name, started, duration_ns, exit_code, success, stdout, stderr)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.JobName, r.Started.UnixNano(), int64(r.Duration), r.ExitCode, success, r.Stdout, r.Stderr)
	return err
}

func (s *SQLite) ReadHistory(jobName string, limit int) ([]ports.RunRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
SELECT job_name, started, duration_ns, exit_code, success, stdout, stderr
FROM runs WHERE job_name = ? ORDER BY started DESC LIMIT ?`, jobName, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var recs []ports.RunRecord
	for rows.Next() {
		var r ports.RunRecord
		var startedNs, durNs int64
		var success int
		if err := rows.Scan(&r.JobName, &startedNs, &durNs, &r.ExitCode, &success, &r.Stdout, &r.Stderr); err != nil {
			return nil, err
		}
		r.Started = time.Unix(0, startedNs).UTC()
		r.Duration = time.Duration(durNs)
		r.Success = success == 1
		recs = append(recs, r)
	}
	return recs, rows.Err()
}

func (s *SQLite) MarkRunning(jobName string, startedAt time.Time) error {
	_, err := s.db.Exec(`
INSERT INTO running (job_name, started_at) VALUES (?, ?)
ON CONFLICT(job_name) DO UPDATE SET started_at=excluded.started_at`,
		jobName, startedAt.UnixNano())
	return err
}

func (s *SQLite) ClearRunning(jobName string) error {
	_, err := s.db.Exec(`DELETE FROM running WHERE job_name = ?`, jobName)
	return err
}

func (s *SQLite) ListRunning() ([]ports.RunningJob, error) {
	rows, err := s.db.Query(`SELECT job_name, started_at FROM running`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ports.RunningJob
	for rows.Next() {
		var r ports.RunningJob
		var ns int64
		if err := rows.Scan(&r.JobName, &ns); err != nil {
			return nil, err
		}
		r.StartedAt = time.Unix(0, ns).UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *SQLite) Close() error { return s.db.Close() }

var _ ports.Store = (*SQLite)(nil)
