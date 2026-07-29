# cronhub — Architecture

A cross-platform cron alternative: an always-running scheduler daemon that fires
jobs reliably, logs every run, survives restarts, and can register itself with
the operating system's existing service manager. Standard crontabs import and run
unchanged.

This document is the contract the code conforms to. It defines the seams (ports),
their v1 default implementations, the config model, and — deliberately — the scope
boundary of v1. Read the "Scope discipline" section before adding anything.

---

## 1. Design principles

1. **The core depends on abstractions, never on concretes.** The engine knows
   about interfaces (ports). It never imports SQLite, systemd, cron syntax, or a
   shell directly. Concrete implementations are constructed at startup and injected.
   This one rule is what makes every future addition a new file instead of a rewrite.

2. **Every behavioral decision is a policy with a documented default.** "What happens
   when a run overlaps the next trigger?" is not an `if` buried in the loop — it is a
   named, swappable policy. Silence erodes trust; a documented default is a promise.

3. **Beginner touches nothing; expert overrides everything.** Config resolves in
   layers. An empty config runs correctly on compiled-in defaults. Each layer below
   overrides the one above:
   - compiled-in defaults
   - global config file
   - per-job overrides
   - (later) env vars / CLI flags

4. **Fail loud at load time, never guess at run time.** A malformed config is a hard
   error before the daemon starts, not a silently-wrong schedule at 3am.

5. **Draw every seam in v1. Implement exactly one impl per seam in v1.** The interfaces
   are the architecture and they are cheap. The extra implementations are the roadmap,
   not the release. Do not build impl #2 of anything until v1 has shipped and a real
   user has asked.

6. **Use the OS's existing service manager. Never build one.** cronhub is a *client*
   of systemd / launchd / Windows SCM, not a competitor. A service adapter's entire
   job is: emit the correct config file for the platform and run one register command.

---

## 2. The relationship between the three parts

```
Project side            Engine (daemon)              System service
config + CLI    --->    always running        <---   systemd / launchd / SCM
declares jobs           fires, logs, persists         keeps engine alive
                             |
                             v
                         Disk state (survives restart)
```

- **Project side** declares jobs (a committed config file + CLI). The project may
  then be down; irrelevant — the engine is a separate process.
- **Engine** is the always-on daemon. It owns the tick loop and fires jobs. It needs
  the system service to stay alive across reboots and crashes. Without that, it's a toy.
- **System service** is the OS's existing supervisor. cronhub registers with it.
- **Disk state** is why cronhub is not a discover-then-delete tool: jobs and run
  history survive restarts.

---

## 3. The ports (seams)

The core engine depends on these seven interfaces and nothing else concrete. Each
has exactly one v1 implementation. The "later" column is roadmap, not v1 work.

| Port              | Question it answers                          | v1 default impl              | Later (community / future phases)          |
|-------------------|----------------------------------------------|------------------------------|--------------------------------------------|
| Schedule parser   | How does a user express *when*?              | standard cron syntax         | human syntax, intervals, sunrise/sunset    |
| Trigger policy    | Machine was asleep — catch up or skip?       | skip missed                  | catch-up-once, catch-up-all                |
| Overlap policy    | Previous run still going at next trigger?    | no overlap (skip)            | queue, kill-and-restart, allow-parallel    |
| Executor          | How does a job actually run?                 | local shell (build-tagged)   | container, SSH, HTTP call                  |
| Store             | Where does state live across restarts?       | SQLite (single file)         | Postgres (multi-node)                      |
| Notifier          | How does the user learn a job failed?        | log only                     | email, Slack, webhook, desktop             |
| Service adapter   | How does the OS keep the daemon alive?       | kardianos/service (tri-OS)   | hand-rolled per-OS refinements             |
| Config loader     | What file format defines jobs?               | TOML                         | YAML, DB-backed, API-driven                |

### Port contracts (conceptual — Go signatures live in the skeleton)

- **Schedule parser:** `Parse(spec string) (Schedule, error)` where
  `Schedule.Next(after time.Time) time.Time`. The core only asks "when is the next
  fire after T?" It knows nothing about cron syntax.

- **Trigger policy:** given `(scheduledFor, now, lastRun)` returns a decision:
  run now / skip / (later) catch-up. Selected per-job via config, default skip-missed.

- **Overlap policy:** consulted when a job is due but its previous run is still active.
  Returns skip / queue / kill-previous / allow-parallel. Default skip.

- **Executor:** `Run(ctx, job) (Handle, error)`, `Kill(handle) error`, and result
  reporting (exit code, captured stdout/stderr, duration). **This is the one seam with
  genuine per-OS work** — process termination-with-children differs on Unix vs Windows.
  Signature is OS-agnostic; platform code lives behind build tags
  (`executor_unix.go` / `executor_windows.go`).

- **Store:** `SaveJob`, `LoadJobs`, `RecordRun`, `ReadHistory`. Transactional. v1 SQLite,
  single file under the config dir. Swapping to Postgres later is a new impl, not surgery.

- **Notifier:** `Notify(event)` where event describes a run outcome. v1 writes to the log.

- **Service adapter:** `Install`, `Uninstall`, `Start`, `Stop`, `Status`. Wraps
  `kardianos/service` so the core is not coupled to that library's API. Defaults to
  **user-level** registration (no root, simplest install); system-level is opt-in.

- **Config loader:** `Load(path) (Config, error)`. Parses a file into in-memory structs.
  The core sees only structs — never the file format. v1 TOML. TOML is chosen because it
  is explicitly typed and fails loud: no YAML "Norway problem" (`NO` → false), no
  base-60 time coercion (`08:00` → 480), no whitespace-significant silent errors —
  all of which are hazards directly in a scheduler's domain.

---

## 4. Config model

A job at minimum needs a schedule and a command. Everything else is optional and
falls back to compiled-in defaults. Conceptual shape (TOML):

```toml
version = 1                      # schema version — present from day one

[defaults]                       # optional; overrides compiled-in defaults machine-wide
on_overlap = "skip"
on_missed  = "skip"
notify     = ["log"]
timezone   = "Africa/Casablanca"

[[job]]
name     = "backup"
schedule = "0 3 * * *"           # required
command  = "/opt/backup.sh"      # required
# --- everything below optional; defaults apply if omitted ---
on_overlap = "skip"              # skip | queue | parallel | kill
on_missed  = "skip"              # skip | catch_up_once | catch_up_all
timeout    = "30m"
notify     = ["log"]             # log | email | slack | webhook
timezone   = "Africa/Casablanca"
```

- `version` is mandatory and enables painless migration later.
- A job with only `name`, `schedule`, `command` is valid — a beginner writes three lines.
- An expert overrides any policy per job.

---

## 5. Cross-OS scope in v1

cronhub runs on Linux, macOS, and Windows (incl. ARM) from v1. Effort is not uniform:

| Layer                                   | Cross-OS cost                                    |
|-----------------------------------------|--------------------------------------------------|
| Core / parser / store / config          | Free — Go compiles native binaries per platform. |
| Service adapter                         | Low — `kardianos/service` emits the right file.  |
| Executor: spawn + capture               | Low — `os/exec`.                                 |
| Executor: kill / timeout **with children** | **Real per-OS work** — build-tagged files.    |

The last row is the only place genuine cross-OS effort concentrates (process groups
on Unix vs job objects on Windows; the classic "works on my Linux box, zombies on
Windows" bug). It is contained entirely within the Executor seam — the core just calls
`Run` and `Kill`.

---

## 6. v1 reliability defaults (the promises)

These are the documented defaults that make cronhub trustworthy. All are overridable.

- **Overlap:** if the previous run of a job is still active, skip this trigger.
- **Missed runs:** if the daemon was down/asleep at a scheduled time, that run is
  skipped (not replayed) in v1. Catch-up policies are roadmap.
- **Restart:** on daemon start, state is re-read from disk and the schedule resumes.
  No job definitions or run history are lost.
- **Every run is recorded:** exit code, captured stdout/stderr, start time, duration.
  This is cron's single biggest failure and cronhub's core selling point.
- **Service:** defaults to user-level registration (runs when the user is logged in,
  no root). System-level (runs at boot regardless of login, needs root) is opt-in.

---

## 7. Scope discipline (read before adding anything)

**In v1:**
- Engine + tick loop.
- One implementation of each of the eight ports above.
- Layered TOML config with versioned schema.
- Cross-OS binaries (Linux/macOS/Windows).
- `cronhub import-crontab` — read an existing crontab and run those jobs unchanged
  (the zero-friction adoption hook).
- `cronhub install` / `start` / `stop` / `status` / `list` / `logs`.

**Explicitly NOT in v1 (roadmap — do not build until shipped + requested):**
- Distributed / multi-node scheduling.
- Web UI.
- Any second implementation of any port (extra executors, notifiers, stores, parsers).
- Catch-up / replay policies.

The architecture is *ready* for all of the above because the seams exist. Readiness is
not a reason to build them. Ship one impl per seam; let real users and contributors
pull the rest into existence.

---

## 8. Contribution surface (open source)

Each port is an independent contribution target — a contributor adds an implementation
against a stable interface without touching the core:

- New **schedule parsers** (human-readable syntax, intervals).
- New **notifiers** (Slack, email, webhook, desktop) — ~30 lines each against one interface.
- New **executors** (container, SSH, HTTP).
- New **service adapter** refinements per OS.
- New **config loaders** (YAML).
- New **stores** (Postgres).

The rule for every contribution: depend on the port interface, never on the core's
internals, and ship with a documented default behavior.
