# cronhub

A reliable, cross-platform cron alternative. cronhub is an always-running
scheduler daemon that fires jobs on time, **logs every run** (exit code, stdout,
stderr, duration), **survives restarts**, and **registers with your OS's existing
service manager** (systemd / launchd / Windows SCM) so it comes back on boot.
Classic crontabs import and run unchanged.

cron fails silently. cronhub doesn't.

## Status

v1 skeleton. The architecture is complete and every seam has one working
implementation. See [ARCHITECTURE.md](./ARCHITECTURE.md) for the full design,
the port/seam model, and the scope boundary.

## Quick start

```sh
go build -o cronhub ./cmd/cronhub
```

**New user** — create a starter config (written to the OS-native location
automatically; you don't need to know the path):

```sh
./cronhub init              # writes a commented cronhub.toml, tells you where
# edit the generated file, then:
./cronhub list              # confirm your jobs
./cronhub run               # run the scheduler in the foreground (Ctrl+C to stop)
```

**Existing cron user** — import your crontab and you're done:

```sh
crontab -l | ./cronhub import-crontab -     # import from stdin
# or
./cronhub import-crontab /path/to/crontab
./cronhub list
./cronhub run
```

**Run as a real OS service** (survives reboots), once you're happy:

```sh
./cronhub install           # user-level, no root
./cronhub install --system  # system-level, needs root
./cronhub start | stop | status
```

Any command accepts `--config PATH` to use a specific config instead of the
default location. `init` and `import-crontab` accept `--force` to overwrite an
existing config.

### Where the config lives

`cronhub init` writes to the OS-native config directory, so you never have to
create folders or copy files by hand:

| OS      | Default config path                                      |
|---------|----------------------------------------------------------|
| macOS   | `~/Library/Application Support/cronhub/cronhub.toml`     |
| Linux   | `~/.config/cronhub/cronhub.toml`                         |
| Windows | `%AppData%\cronhub\cronhub.toml`                         |

## Example config (`cronhub.toml`)

```toml
version = 1

[defaults]                     # optional machine-wide overrides
timezone = "Africa/Casablanca"

[[job]]
name     = "heartbeat"
schedule = "* * * * *"         # standard cron syntax
command  = "echo alive"

[[job]]
name       = "backup"
schedule   = "0 3 * * *"
command    = "/opt/backup.sh"
on_overlap = "skip"            # skip | queue | parallel | kill
on_missed  = "skip"            # skip | catch_up_once | catch_up_all
timeout    = "30m"
notify     = ["log"]
```

A job needs only `name`, `schedule`, and `command`; everything else falls back to
documented defaults.

## Notifiers (how cronhub tells you what happened)

Every job logs by default. Beyond that, you declare **notifier instances** and
reference them per job. The built-in `log` notifier is always available; other
notifiers are declared in config:

```toml
[[notifier]]
name          = "alerts"
type          = "webhook"                  # POSTs the job outcome as JSON
url           = "https://hooks.slack.com/services/..."
failures_only = true                       # only fire when a job fails

[[job]]
name     = "nightly-backup"
schedule = "0 3 * * *"
command  = "/opt/backup.sh"
notify   = ["log", "alerts"]               # log every run, webhook on failure
```

A job that references an undeclared notifier is a hard config error — cronhub
tells you before it starts, rather than silently dropping notifications.

### Adding a new notifier type (the extension story)

This is the heart of cronhub's design. A new notifier is a ~40-line file
implementing one interface, plus one line of wiring:

1. Create `internal/notifier/yours.go` implementing `ports.Notifier`
   (`Name() string` and `Notify(ports.NotifyEvent) error`). It carries its own
   config in its struct.
2. Add a `case "yourtype":` in `buildEngine` (main.go) that constructs it from
   its config declaration.
3. Add its type + any required fields to config validation in `LoadConfig`.

The core engine is never touched. See `internal/notifier/webhook.go` as the
worked example. Slack/Discord/custom endpoints already work today via `webhook`.

## Design in one sentence

The core engine depends only on interfaces (ports); each port has exactly one v1
implementation and is independently swappable, so new schedule syntaxes, notifiers,
executors, stores, and OS service adapters are added as new files without touching
the core. Read [ARCHITECTURE.md](./ARCHITECTURE.md).

## Contributing

Each port is an independent contribution target. Good first contributions: a Slack
or email notifier (~30 lines against `ports.Notifier`), a human-readable schedule
parser, or refinements to the Windows executor's process-tree kill. The rule:
depend on the port interface, never on the core's internals, and ship with a
documented default.

## License

TBD.
