// Command cronhub is the entrypoint. This is the ONLY place where concrete
// implementations are constructed and injected into the core. Subcommands:
//
//	cronhub init                 create a starter config in the OS-native location
//	cronhub import-crontab FILE  import a classic crontab into a cronhub config
//	cronhub list                 list configured jobs
//	cronhub run                  run the scheduler in the foreground
//	cronhub install [--system]   register with the OS service manager
//	cronhub uninstall
//	cronhub start | stop | status
//
// Config is loaded from --config or the OS-native default path.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/aelaboussi/cronhub/internal/config"
	"github.com/aelaboussi/cronhub/internal/core"
	"github.com/aelaboussi/cronhub/internal/crontab"
	"github.com/aelaboussi/cronhub/internal/executor"
	"github.com/aelaboussi/cronhub/internal/notifier"
	"github.com/aelaboussi/cronhub/internal/policy"
	"github.com/aelaboussi/cronhub/internal/ports"
	"github.com/aelaboussi/cronhub/internal/schedule"
	svc "github.com/aelaboussi/cronhub/internal/service"
	"github.com/aelaboussi/cronhub/internal/store"
)

// version is set at build time with -ldflags "-X main.version=vX.Y.Z".
// It defaults to "dev" for local builds.
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	cfgPath := fs.String("config", defaultConfigPath(), "path to config file")
	systemLevel := fs.Bool("system", false, "register as a system service (needs root)")
	force := fs.Bool("force", false, "overwrite an existing config")
	limit := fs.Int("limit", 10, "number of runs to show (history)")
	failed := fs.Bool("failed", false, "show only failed runs (history)")
	runN := fs.Int("run", 0, "show full details of run number N (history)")
	fSchedule := fs.String("schedule", "", "job schedule (add/edit)")
	fCommand := fs.String("command", "", "job command (add/edit)")
	fName := fs.String("name", "", "job name (add/edit; or pass as positional)")
	fOverlap := fs.String("on-overlap", "", "overlap policy (add/edit)")
	fMissed := fs.String("on-missed", "", "missed-run policy (add/edit)")
	fTimeout := fs.String("timeout", "", "job timeout, e.g. 30m (add/edit)")
	fTimezone := fs.String("timezone", "", "job timezone (add/edit)")
	var fNotify multiFlag
	fs.Var(&fNotify, "notify", "notifier name (add/edit; repeatable)")
	watch := fs.Bool("watch", false, "refresh the status view every 2s")
	// Reorder so flags may appear before OR after positional args (Go's flag
	// package otherwise stops at the first positional).
	_ = fs.Parse(reorderArgs(os.Args[2:]))

	switch cmd {
	case "init":
		mustInit(*force)
	case "import-crontab":
		mustImport(fs.Arg(0), *force)
	case "run":
		mustRun(*cfgPath)
	case "list":
		mustList(*cfgPath)
	case "add":
		nameArg := firstNonEmpty(*fName, fs.Arg(0))
		mustAdd(*cfgPath, config.NewJobSpec{
			Name: nameArg, Schedule: *fSchedule, Command: *fCommand,
			OnOverlap: *fOverlap, OnMissed: *fMissed, Timeout: *fTimeout,
			Timezone: *fTimezone, Notify: fNotify,
		})
	case "remove", "rm":
		mustRemove(*cfgPath, firstNonEmpty(*fName, fs.Arg(0)))
	case "edit":
		mustEdit(*cfgPath, config.NewJobSpec{
			Name: firstNonEmpty(*fName, fs.Arg(0)), Schedule: *fSchedule, Command: *fCommand,
			OnOverlap: *fOverlap, OnMissed: *fMissed, Timeout: *fTimeout,
			Timezone: *fTimezone, Notify: fNotify,
		})
	case "history":
		mustHistory(fs.Arg(0), *limit, *failed, *runN)
	case "install":
		mustService(*systemLevel, func(a *svc.Adapter) error { return a.Install() }, "installed")
	case "uninstall":
		mustService(*systemLevel, func(a *svc.Adapter) error { return a.Uninstall() }, "uninstalled")
	case "start":
		mustService(*systemLevel, func(a *svc.Adapter) error { return a.Start() }, "started")
	case "stop":
		mustService(*systemLevel, func(a *svc.Adapter) error { return a.Stop() }, "stopped")
	case "status":
		mustJobStatus(*cfgPath, *watch)
	case "service-status":
		mustStatus(*systemLevel)
	case "version", "--version", "-v":
		fmt.Printf("cronhub %s\n", version)
	default:
		usage()
		os.Exit(2)
	}
}

func mustInit(force bool) {
	path, err := config.Init(force)
	if err != nil {
		die(err)
	}
	fmt.Printf("cronhub: wrote starter config to %s\n", path)
	fmt.Println("Edit it, then run `cronhub run` (foreground) or `cronhub install` (as a service).")
}

func mustImport(src string, force bool) {
	if src == "" {
		die(fmt.Errorf("usage: cronhub import-crontab <crontab-file> (use '-' for stdin)"))
	}
	var r *os.File
	var err error
	if src == "-" {
		r = os.Stdin
	} else {
		r, err = os.Open(src)
		if err != nil {
			die(err)
		}
		defer r.Close()
	}
	res, err := crontab.Parse(r)
	if err != nil {
		die(fmt.Errorf("parsing crontab: %w", err))
	}
	for _, w := range res.Warnings {
		fmt.Fprintf(os.Stderr, "cronhub: warning: %s\n", w)
	}
	path, err := config.WriteConfig(crontab.RenderTOML(res), force)
	if err != nil {
		die(err)
	}
	fmt.Printf("cronhub: imported %d job(s) to %s\n", len(res.Entries), path)
	fmt.Println("Review it, then run `cronhub list` to confirm, then `cronhub run`.")
}

// buildEngine is the single wiring point: concretes -> core.Deps -> Engine.
func buildEngine(cfgPath string) (*core.Engine, ports.Store, error) {
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		return nil, nil, err
	}

	st, err := store.Open(dbPath())
	if err != nil {
		return nil, nil, err
	}
	// Validate every schedule up front so a bad spec fails loud before the
	// daemon starts, not silently at first tick.
	parser := schedule.NewAutoParser()
	for _, j := range cfg.Jobs {
		if _, perr := parser.Parse(j.Schedule); perr != nil {
			_ = st.Close()
			return nil, nil, fmt.Errorf("job %q: %w", j.Name, perr)
		}
	}
	for _, j := range cfg.Jobs {
		if err := st.SaveJob(j); err != nil {
			_ = st.Close()
			return nil, nil, err
		}
	}

	// Build the notifier map. "log" is always available; other notifiers are
	// constructed from their config declarations. Adding a new notifier type is
	// a new case here plus a new impl file — the core is untouched.
	notifiers := map[string]ports.Notifier{
		"log": notifier.NewLog(),
	}
	for _, nd := range cfg.Notifiers {
		switch nd.Type {
		case "webhook":
			failuresOnly := true // sensible default: don't ping on every success
			if nd.FailuresOnly != nil {
				failuresOnly = *nd.FailuresOnly
			}
			notifiers[nd.Name] = notifier.NewWebhook(nd.Name, nd.URL, failuresOnly)
		}
	}

	deps := core.Deps{
		Parser:    parser,
		Trigger:   policy.NewSkipMissed(),
		Overlap:   policy.NewNoOverlap(),
		Executor:  executor.NewLocal(),
		Store:     st,
		Notifiers: notifiers,
	}
	return core.New(deps), st, nil
}

func mustRun(cfgPath string) {
	eng, st, err := buildEngine(cfgPath)
	if err != nil {
		dieConfig(err, cfgPath)
	}
	defer st.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-sig; eng.Stop() }()

	fmt.Println("cronhub: scheduler running")
	if err := eng.Run(ctx); err != nil && err != context.Canceled {
		die(err)
	}
}

// mustJobStatus renders the live dashboard: for each job, whether it's running
// now, its last completed run, and its next scheduled run. With watch=true it
// refreshes on an interval.
func mustJobStatus(cfgPath string, watch bool) {
	render := func() error {
		jobs, err := config.Load(cfgPath)
		if err != nil {
			return err
		}
		st, err := store.Open(dbPath())
		if err != nil {
			return err
		}
		defer st.Close()

		running, err := st.ListRunning()
		if err != nil {
			return err
		}
		runningSince := map[string]time.Time{}
		for _, r := range running {
			runningSince[r.JobName] = r.StartedAt
		}

		parser := schedule.NewAutoParser()
		now := time.Now()

		if watch {
			// Home the cursor and clear from there down. Combined with the
			// alternate screen buffer (entered in the watch loop below), this
			// redraws in place without touching the user's real scrollback.
			fmt.Print("\033[H\033[J")
			fmt.Printf("cronhub status — %s (refreshing; Ctrl+C to stop)\n\n", now.Format("15:04:05"))
		}
		fmt.Printf("  %-20s %-24s %-22s %s\n", "job", "state", "last run", "next run")
		for _, j := range jobs {
			// next run
			next := "—"
			if sched, perr := parser.Parse(j.Schedule); perr == nil {
				next = sched.Next(now).Format("Mon 15:04")
			}

			// state: running (with staleness guard) or idle
			state := "idle"
			if since, ok := runningSince[j.Name]; ok {
				elapsed := now.Sub(since)
				// Staleness guard: if a run has been "in progress" far longer
				// than it plausibly should, the daemon likely died mid-run and
				// left a ghost. Show uncertainty rather than a confident lie.
				limit := j.Timeout
				if limit == 0 {
					limit = 6 * time.Hour // hard ceiling when no timeout is set
				}
				if elapsed > limit {
					state = fmt.Sprintf("unknown (stuck %s?)", elapsed.Round(time.Second))
				} else {
					state = fmt.Sprintf("RUNNING (%s)", elapsed.Round(time.Second))
				}
			}

			// last completed run
			last := "never"
			if recs, herr := st.ReadHistory(j.Name, 1); herr == nil && len(recs) > 0 {
				r := recs[0]
				result := "ok"
				if !r.Success {
					result = "FAILED"
				}
				last = fmt.Sprintf("%s (%s)", r.Started.Local().Format("Mon 15:04"), result)
			}

			fmt.Printf("  %-20s %-24s %-22s %s\n", j.Name, state, last, next)
		}
		return nil
	}

	if !watch {
		if err := render(); err != nil {
			dieConfig(err, cfgPath)
		}
		return
	}

	// watch mode: use the terminal's alternate screen buffer (like top/htop/less)
	// so the live view is drawn on a separate screen and exiting — including via
	// Ctrl+C — restores the user's terminal exactly as it was, with their
	// scrollback intact.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	enterAlt := func() { fmt.Print("\033[?1049h\033[H") } // switch to alt screen, home
	leaveAlt := func() { fmt.Print("\033[?1049l") }       // switch back to normal screen

	enterAlt()
	// Guarantee restoration even if something panics.
	defer leaveAlt()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	if err := render(); err != nil {
		leaveAlt()
		dieConfig(err, cfgPath)
	}
	for {
		select {
		case <-sig:
			leaveAlt()
			return
		case <-ticker.C:
			if err := render(); err != nil {
				leaveAlt()
				die(err)
			}
		}
	}
}

func mustList(cfgPath string) {
	jobs, err := config.Load(cfgPath)
	if err != nil {
		dieConfig(err, cfgPath)
	}
	parser := schedule.NewAutoParser()
	now := time.Now()
	for _, j := range jobs {
		next := "invalid schedule"
		if sched, perr := parser.Parse(j.Schedule); perr != nil {
			// Fail loud: a bad schedule is a config error, surfaced by list too.
			die(fmt.Errorf("job %q: %w", j.Name, perr))
		} else {
			next = sched.Next(now).Format("Mon 2006-01-02 15:04")
		}
		fmt.Printf("%-20s %-22s next: %s\n", j.Name, j.Schedule, next)
	}
}

func mustHistory(jobName string, limit int, failedOnly bool, runN int) {
	if jobName == "" {
		die(fmt.Errorf("usage: cronhub history <job> [--limit N] [--failed] [--run N]"))
	}
	st, err := store.Open(dbPath())
	if err != nil {
		die(err)
	}
	defer st.Close()

	// Read a bit extra when filtering so --failed still fills the limit.
	readN := limit
	if failedOnly {
		readN = limit * 10
	}
	recs, err := st.ReadHistory(jobName, readN)
	if err != nil {
		die(err)
	}
	if len(recs) == 0 {
		fmt.Printf("cronhub: no runs recorded for job %q yet\n", jobName)
		return
	}

	// Filter to failures if asked.
	if failedOnly {
		var f []ports.RunRecord
		for _, r := range recs {
			if !r.Success {
				f = append(f, r)
			}
		}
		recs = f
		if len(recs) == 0 {
			fmt.Printf("cronhub: no failed runs for job %q\n", jobName)
			return
		}
	}
	if len(recs) > limit {
		recs = recs[:limit]
	}

	// Drill-down: show the full details of one run by its number.
	if runN > 0 {
		if runN > len(recs) {
			die(fmt.Errorf("run %d is out of range (only %d runs shown)", runN, len(recs)))
		}
		printRunDetail(jobName, runN, recs[runN-1])
		return
	}

	// Compact table. Runs are numbered newest-first (1 = most recent).
	fmt.Printf("Recent runs of %q (newest first):\n\n", jobName)
	fmt.Printf("  %-3s %-20s %-8s %-10s %s\n", "#", "when", "result", "duration", "exit")
	for i, r := range recs {
		result := "ok"
		if !r.Success {
			result = "FAILED"
		}
		fmt.Printf("  %-3d %-20s %-8s %-10s %d\n",
			i+1,
			r.Started.Format("2006-01-02 15:04:05"),
			result,
			r.Duration.Round(time.Millisecond),
			r.ExitCode,
		)
	}
	fmt.Printf("\nSee a run's output with: cronhub history %s --run N\n", jobName)
}

func printRunDetail(jobName string, n int, r ports.RunRecord) {
	result := "ok"
	if !r.Success {
		result = "FAILED"
	}
	fmt.Printf("Run #%d of %q\n", n, jobName)
	fmt.Printf("  when:     %s\n", r.Started.Format("2006-01-02 15:04:05 MST"))
	fmt.Printf("  result:   %s (exit code %d)\n", result, r.ExitCode)
	fmt.Printf("  duration: %s\n", r.Duration.Round(time.Millisecond))
	if r.Stdout != "" {
		fmt.Printf("\n--- stdout ---\n%s", r.Stdout)
		if !strings.HasSuffix(r.Stdout, "\n") {
			fmt.Println()
		}
	}
	if r.Stderr != "" {
		fmt.Printf("\n--- stderr ---\n%s", r.Stderr)
		if !strings.HasSuffix(r.Stderr, "\n") {
			fmt.Println()
		}
	}
	if r.Stdout == "" && r.Stderr == "" {
		fmt.Printf("\n(no output captured)\n")
	}
}

// multiFlag collects a repeatable string flag (e.g. --notify a --notify b).
type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func mustAdd(cfgPath string, spec config.NewJobSpec) {
	if spec.Name == "" || spec.Schedule == "" || spec.Command == "" {
		die(fmt.Errorf("add needs a name, --schedule, and --command\n  e.g. cronhub add backup --schedule \"every day at 3am\" --command \"/opt/backup.sh\""))
	}
	// Validate the schedule before writing, so a bad one never lands in the file.
	if _, err := schedule.NewAutoParser().Parse(spec.Schedule); err != nil {
		die(fmt.Errorf("invalid schedule: %w", err))
	}
	if err := config.AddJob(cfgPath, spec); err != nil {
		dieConfig(err, cfgPath)
	}
	fmt.Printf("cronhub: added job %q\n", spec.Name)
	fmt.Println("If the scheduler is running as a service, restart it to pick up the change.")
}

func mustRemove(cfgPath, name string) {
	if name == "" {
		die(fmt.Errorf("usage: cronhub remove <job-name>"))
	}
	hadComments, err := config.RemoveJob(cfgPath, name)
	if err != nil {
		dieConfig(err, cfgPath)
	}
	fmt.Printf("cronhub: removed job %q (backup written to %s.bak)\n", name, cfgPath)
	if hadComments {
		fmt.Println("note: the config had comments, which are not preserved when rewriting; see the .bak file if you need them.")
	}
}

func mustEdit(cfgPath string, spec config.NewJobSpec) {
	if spec.Name == "" {
		// Bare `cronhub edit` opens the config in $EDITOR.
		openInEditor(cfgPath)
		return
	}
	if spec.Schedule != "" {
		if _, err := schedule.NewAutoParser().Parse(spec.Schedule); err != nil {
			die(fmt.Errorf("invalid schedule: %w", err))
		}
	}
	hadComments, err := config.UpdateJob(cfgPath, spec)
	if err != nil {
		dieConfig(err, cfgPath)
	}
	fmt.Printf("cronhub: updated job %q (backup written to %s.bak)\n", spec.Name, cfgPath)
	if hadComments {
		fmt.Println("note: the config had comments, which are not preserved when rewriting; see the .bak file if you need them.")
	}
}

func openInEditor(cfgPath string) {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	c := exec.Command(editor, cfgPath)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		die(err)
	}
}

func mustService(systemLevel bool, action func(*svc.Adapter) error, past string) {
	a, err := svc.New(noopRunner{}, systemLevel)
	if err != nil {
		die(err)
	}
	if err := action(a); err != nil {
		die(err)
	}
	fmt.Printf("cronhub: service %s\n", past)
}

func mustStatus(systemLevel bool) {
	a, err := svc.New(noopRunner{}, systemLevel)
	if err != nil {
		die(err)
	}
	s, err := a.Status()
	if err != nil {
		die(err)
	}
	fmt.Printf("cronhub: %s\n", s)
}

// noopRunner is used for install/start/stop/status calls that don't run the loop.
type noopRunner struct{}

func (noopRunner) Start() error { return nil }
func (noopRunner) Stop() error  { return nil }

func defaultConfigPath() string {
	dir, _ := os.UserConfigDir()
	return filepath.Join(dir, "cronhub", "cronhub.toml")
}

func dbPath() string {
	dir, _ := os.UserConfigDir()
	return filepath.Join(dir, "cronhub", "cronhub.db")
}

// reorderArgs moves flag-like tokens (starting with '-', except a lone "-")
// ahead of positional args, and pairs a following value with known value-flags
// so both forms `--config x pos` and `pos --config x` parse. Known value flags:
// --config. Boolean flags (--force, --system) need no value.
func reorderArgs(args []string) []string {
	valueFlags := map[string]bool{
		"--config": true, "-config": true,
		"--limit": true, "-limit": true,
		"--run": true, "-run": true,
		"--schedule": true, "-schedule": true,
		"--command": true, "-command": true,
		"--name": true, "-name": true,
		"--on-overlap": true, "-on-overlap": true,
		"--on-missed": true, "-on-missed": true,
		"--timeout": true, "-timeout": true,
		"--timezone": true, "-timezone": true,
		"--notify": true, "-notify": true,
	}
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-" || !strings.HasPrefix(a, "-") {
			pos = append(pos, a)
			continue
		}
		flags = append(flags, a)
		// If it's a value flag in "--config value" form (no '='), take the next.
		if valueFlags[a] && !strings.Contains(a, "=") && i+1 < len(args) {
			flags = append(flags, args[i+1])
			i++
		}
	}
	return append(flags, pos...)
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "cronhub:", err)
	os.Exit(1)
}

// dieConfig gives a teaching error when the config file is simply missing,
// rather than a raw open error.
func dieConfig(err error, cfgPath string) {
	if errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "cronhub: no config found at %s\n", cfgPath)
		fmt.Fprintln(os.Stderr, "Run `cronhub init` to create one, `cronhub import-crontab <file>` to import an existing crontab, or pass `--config <path>`.")
		os.Exit(1)
	}
	die(err)
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: cronhub <init|import-crontab|list|add|edit|remove|history|status|run|install|uninstall|start|stop|service-status|version> [flags]")
	fmt.Fprintln(os.Stderr, "  init                 create a starter config in the OS-native location")
	fmt.Fprintln(os.Stderr, "  import-crontab FILE  import a classic crontab (use '-' for stdin)")
	fmt.Fprintln(os.Stderr, "  list                 list configured jobs")
	fmt.Fprintln(os.Stderr, "  add NAME             add a job (--schedule, --command, and optional --notify etc.)")
	fmt.Fprintln(os.Stderr, "  edit [NAME]          edit a job's fields, or open the config in $EDITOR")
	fmt.Fprintln(os.Stderr, "  remove NAME          remove a job (writes a .bak first)")
	fmt.Fprintln(os.Stderr, "  history JOB          show recent runs of a job (--limit N, --failed, --run N)")
	fmt.Fprintln(os.Stderr, "  status               live dashboard: what's running, last run, next run (--watch)")
	fmt.Fprintln(os.Stderr, "  run                  run the scheduler in the foreground")
	fmt.Fprintln(os.Stderr, "  install [--system]   register with the OS service manager")
	fmt.Fprintln(os.Stderr, "  version              print the cronhub version")
	fmt.Fprintln(os.Stderr, "flags: --config PATH, --system, --force, --limit N, --failed, --run N")
}
