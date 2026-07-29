package store

import (
	"github.com/aelaboussi/cronhub/internal/ports"
	"strings"
	"sync"
)

type SQLite struct {
	mu   sync.Mutex
	jobs map[string]ports.Job
	runs map[string][]ports.RunRecord
}

func Open(_ string) (*SQLite, error) {
	return &SQLite{jobs: map[string]ports.Job{}, runs: map[string][]ports.RunRecord{}}, nil
}
func (m *SQLite) SaveJob(j ports.Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(j.Notify) == 0 {
		j.Notify = strings.Split("log", ",")
	}
	m.jobs[j.Name] = j
	return nil
}
func (m *SQLite) LoadJobs() ([]ports.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o := []ports.Job{}
	for _, j := range m.jobs {
		o = append(o, j)
	}
	return o, nil
}
func (m *SQLite) RecordRun(r ports.RunRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runs[r.JobName] = append(m.runs[r.JobName], r)
	return nil
}
func (m *SQLite) ReadHistory(n string, l int) ([]ports.RunRecord, error) { return m.runs[n], nil }
func (m *SQLite) Close() error                                           { return nil }

var _ ports.Store = (*SQLite)(nil)
