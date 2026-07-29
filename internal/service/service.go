// Package service provides the v1 ServiceAdapter. It wraps kardianos/service
// (which emits the correct systemd unit / launchd plist / Windows SCM entry per
// OS) behind cronhub's own interface, so the core is not coupled to that library.
// Defaults to user-level registration: no root, simplest install.
package service

import (
	"github.com/kardianos/service"

	"github.com/aelaboussi/cronhub/internal/ports"
)

// Runner is whatever the daemon needs to do while supervised. The service
// adapter calls Start (non-blocking: spin up and return) and Stop.
type Runner interface {
	Start() error
	Stop() error
}

type Adapter struct {
	svc service.Service
}

type program struct{ r Runner }

func (p *program) Start(service.Service) error { return p.r.Start() }
func (p *program) Stop(service.Service) error  { return p.r.Stop() }

// New builds an adapter. systemLevel=false => user-level (default, no root).
func New(r Runner, systemLevel bool) (*Adapter, error) {
	opts := service.KeyValue{}
	if !systemLevel {
		opts["UserService"] = true
	}
	cfg := &service.Config{
		Name:        "cronhub",
		DisplayName: "cronhub scheduler",
		Description: "Reliable cross-platform cron alternative.",
		Option:      opts,
	}
	svc, err := service.New(&program{r: r}, cfg)
	if err != nil {
		return nil, err
	}
	return &Adapter{svc: svc}, nil
}

func (a *Adapter) Install() error          { return a.svc.Install() }
func (a *Adapter) Uninstall() error        { return a.svc.Uninstall() }
func (a *Adapter) Start() error            { return a.svc.Start() }
func (a *Adapter) Stop() error             { return a.svc.Stop() }
func (a *Adapter) Status() (string, error) { s, err := a.svc.Status(); return statusString(s), err }

// Run hands control to kardianos/service (blocks; used when the OS launches us
// as the service).
func (a *Adapter) Run() error { return a.svc.Run() }

func statusString(s service.Status) string {
	switch s {
	case service.StatusRunning:
		return "running"
	case service.StatusStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

var _ ports.ServiceAdapter = (*Adapter)(nil)
