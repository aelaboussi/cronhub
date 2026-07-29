# cronhub

cronhub runs scheduled jobs. It's meant to replace cron for people who are tired
of cron's two biggest problems: you can't read the schedule syntax, and when a
job fails at 3am, cron tells you nothing.

cronhub fixes both. You can write schedules in plain words ("every monday at
9am"), and every run is recorded — exit code, output, how long it took — so you
can actually see what happened. It runs on macOS, Linux, and Windows from a
single binary, and it can install itself as a proper background service that
survives reboots.

If you already have a crontab, you can import it and keep going without rewriting
anything.

## Contents

- [Install](#install)
- [First run](#first-run)
- [Writing schedules](#writing-schedules)
- [The config file](#the-config-file)
- [Notifications](#notifications)
- [Running as a background service](#running-as-a-background-service)
- [All commands](#all-commands)
- [Where things live](#where-things-live)
- [How it's built](#how-its-built)
- [Contributing](#contributing)
- [License](#license)

## Install

The quickest way, on macOS or Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/aelaboussi/cronhub/main/install.sh | sh
```

This downloads the right binary for your system and installs it. If you'd rather
not pipe a script into your shell (fair), read on for the manual options.

### Download a binary

Grab the binary for your platform from the
[latest release](https://github.com/aelaboussi/cronhub/releases/latest):

- macOS Apple Silicon: `cronhub-darwin-arm64`
- macOS Intel: `cronhub-darwin-amd64`
- Linux x86-64: `cronhub-linux-amd64`
- Linux ARM64: `cronhub-linux-arm64`
- Windows: `cronhub-windows-amd64.exe`

Make it executable and put it on your PATH:

```sh
chmod +x cronhub-darwin-arm64
sudo mv cronhub-darwin-arm64 /usr/local/bin/cronhub
cronhub --version
```

Each release also publishes `checksums.txt` so you can verify your download.

### Build from source

If you have Go 1.22 or newer:

```sh
git clone https://github.com/aelaboussi/cronhub.git
cd cronhub
go build -o cronhub ./cmd/cronhub
```

That produces a single `cronhub` binary in the current folder. Move it somewhere
on your PATH if you want to run it from anywhere:

```sh
sudo mv cronhub /usr/local/bin/
```

## First run

If you're starting fresh, create a config file:

```sh
cronhub init
```

This writes a starter config to the right place for your operating system (see
[Where things live](#where-things-live)) and tells you the path. Open that file,
add your jobs, then check they look right:

```sh
cronhub list
```

`list` shows each job, its schedule, and the next time it will run — which is a
quick way to confirm you wrote the schedule correctly.

To start the scheduler in your terminal:

```sh
cronhub run
```

It stays running and fires jobs on schedule, printing a line each time a job
runs. Press Ctrl+C to stop. This is the right mode while you're setting things
up. When you're happy, install it as a background service (further down) so it
keeps running without a terminal open.

If you already use cron, skip `init` and import your existing crontab instead:

```sh
crontab -l | cronhub import-crontab -
```

That reads your current crontab, converts every job into a cronhub job, and
writes the config for you. Review it with `cronhub list` and you're done.

## Writing schedules

cronhub understands two ways of writing a schedule. You can mix both in the same
config — use whichever is clearer for each job.

### Plain words

These are the readable forms. They're a fixed set — cronhub isn't guessing at
free-form English, it understands exactly these patterns, so they always behave
the same way:

```
every 30 seconds
every 5 minutes
every 2 hours
hourly
daily
weekly
monthly
yearly

every day at 9am
every day at 21:00
every day at noon
every day at midnight

every monday at 9am
every weekday at 8:30am          (Monday through Friday)
every weekend at 10am            (Saturday and Sunday)
every mon,wed,fri at 6pm

every month on the 1st at midnight
every month on the 15th at 9am
```

Times can be written as `9am`, `9:30am`, `9pm`, `21:00`, `noon`, or `midnight`.
Days can be full names (`monday`) or short (`mon`).

If you write something cronhub doesn't recognize, it won't run the job — it will
stop and print the full list of forms it does understand, so you can fix it.

### Classic cron

If you know cron syntax, or you need something the plain-word forms can't
express (complex ranges, specific month/day combinations), write a standard
five-field cron expression:

```
*/15 * * * *        every 15 minutes
0 9 * * 1           09:00 every Monday
30 2 1 * *          02:30 on the 1st of every month
```

cronhub decides which kind you wrote by looking at the first character: if it
starts with a letter it's treated as plain words, otherwise as cron. You never
have to tell it which.

## The config file

The config is a TOML file. A minimal job needs three things: a name, a schedule,
and a command.

```toml
version = 1

[[job]]
name     = "backup"
schedule = "every day at 3am"
command  = "/opt/backup.sh"
```

`version` is required and should stay `1` for now; it lets future versions of
cronhub upgrade old configs safely.

### All the job fields

Only `name`, `schedule`, and `command` are required. Everything else is
optional and has a sensible default:

```toml
[[job]]
name       = "backup"
schedule   = "every day at 3am"
command    = "/opt/backup.sh"

on_overlap = "skip"        # what to do if the previous run is still going
                           # when the next one is due. default: "skip".
                           # other values (queue, parallel, kill) are planned
                           # but not implemented yet.

on_missed  = "skip"        # what to do about runs that were missed while the
                           # machine was off or asleep. default: "skip"
                           # (missed runs are not replayed). catch-up options
                           # are planned but not implemented yet.

timeout    = "30m"         # kill the job if it runs longer than this.
                           # accepts things like "30s", "10m", "2h".
                           # default: no timeout.

timezone   = "Africa/Casablanca"   # which timezone the schedule is in.
                           # any IANA name. default: "UTC".

notify     = ["log"]       # where to send the result. see Notifications below.
                           # default: ["log"].
```

### Shared defaults

If several jobs share the same settings, put them once in a `[defaults]`
section. Any job can still override them.

```toml
version = 1

[defaults]
timezone = "Africa/Casablanca"
notify   = ["log"]

[[job]]
name     = "backup"
schedule = "every day at 3am"
command  = "/opt/backup.sh"
# inherits timezone and notify from defaults

[[job]]
name     = "report"
schedule = "every monday at 8am"
command  = "/opt/report.sh"
timezone = "UTC"           # overrides the default just for this job
```

The order settings are applied: built-in defaults first, then your `[defaults]`
section, then each job's own fields. The most specific one wins.

## Notifications

Every job writes a line to the log when it runs. That's the built-in `log`
notifier and it's always on unless you change `notify`.

If you want to be told about failures somewhere other than the log — Slack, a
webhook, your own endpoint — declare a notifier and point jobs at it by name.

```toml
version = 1

[[notifier]]
name          = "alerts"
type          = "webhook"
url           = "https://hooks.slack.com/services/your/webhook/url"
failures_only = true        # only send when a job fails. recommended, unless
                            # you want a message on every single successful run.

[[job]]
name     = "backup"
schedule = "every day at 3am"
command  = "/opt/backup.sh"
notify   = ["log", "alerts"]   # write to the log every time, and hit the
                               # webhook when it fails
```

The webhook sends a small JSON body with the job name, whether it succeeded, the
exit code, how long it took, and any output. Slack, Discord, and most services
accept this format directly.

If a job's `notify` list names a notifier you didn't declare, cronhub refuses to
start and tells you which one is missing — it won't quietly drop your alerts.

## Seeing what happened

cronhub records every run — when it happened, whether it succeeded, how long it
took, the exit code, and any output. This is the part cron can't do: when a job
fails overnight, you can actually find out why.

Show a job's recent runs:

```sh
cronhub history backup
```

```
Recent runs of "backup" (newest first):

  #   when                 result   duration   exit
  1   2026-07-29 07:00:00  ok       120ms      0
  2   2026-07-29 06:00:00  FAILED   90ms       1
  3   2026-07-29 05:00:00  ok       110ms      0

See a run's output with: cronhub history backup --run N
```

Each run has a number. To see the full output of one — including whatever it
printed to stderr — point at that number:

```sh
cronhub history backup --run 2
```

```
Run #2 of "backup"
  when:     2026-07-29 06:00:00 UTC
  result:   FAILED (exit code 1)
  duration: 90ms

--- stderr ---
disk full
```

Show more runs, or only the failures:

```sh
cronhub history backup --limit 30      # show 30 instead of the default 10
cronhub history backup --failed        # only runs that failed
```

## Running as a background service

`cronhub run` only runs while your terminal is open. To keep jobs running all the
time — including after a reboot — install cronhub as a service. It registers
with whatever your operating system already uses (systemd on Linux, launchd on
macOS, the Service Control Manager on Windows). cronhub does not replace those;
it just registers itself with them.

```sh
cronhub install        # install as a service for your user (no admin needed)
cronhub start          # start it
cronhub status         # check whether it's running
cronhub stop           # stop it
cronhub uninstall      # remove it
```

By default it installs at the user level, which needs no admin rights and runs
while you're logged in. If you need it to run at boot regardless of who's logged
in, add `--system` (this needs admin/root):

```sh
sudo cronhub install --system
```

## All commands

```
cronhub init                    create a starter config file
cronhub import-crontab FILE     import an existing crontab (use "-" for stdin)
cronhub list                    show all jobs and their next run time
cronhub history JOB             show recent runs of a job (--limit, --failed, --run)
cronhub run                     run the scheduler in the foreground
cronhub install                 register as a background service
cronhub uninstall               remove the background service
cronhub start                   start the installed service
cronhub stop                    stop the installed service
cronhub status                  show whether the service is running
cronhub version                 print the cronhub version
```

Flags that apply to most commands:

```
--config PATH     use a specific config file instead of the default location
--system          for install/uninstall/start/stop/status: act on the
                  system-level service instead of the user-level one
--force           for init/import-crontab: overwrite an existing config
```

## Where things live

`init` and `import-crontab` write to your operating system's standard config
location, so you don't have to create folders or remember paths:

| OS      | Config and database                                       |
|---------|-----------------------------------------------------------|
| macOS   | `~/Library/Application Support/cronhub/`                   |
| Linux   | `~/.config/cronhub/`                                       |
| Windows | `%AppData%\cronhub\`                                       |

That folder holds two files: `cronhub.toml` (your jobs) and `cronhub.db` (the
run history and saved job state — an SQLite database). If you ever want a clean
slate, deleting `cronhub.db` is safe; it gets rebuilt from your config.

## How it's built

The short version: there's a small core that owns the scheduling loop, and
everything the core needs — how to read a schedule, how to run a command, where
to store state, how to send a notification, how to register as a service — is
behind an interface. Each interface has one implementation today and can get
more later without touching the core.

That's what makes cronhub extensible: adding a new notification type, a new
schedule format, or a new way of running commands is a new file, not a rewrite.
The full explanation, including every interface and the reasoning behind the
design, is in [ARCHITECTURE.md](./ARCHITECTURE.md).

## Contributing

Contributions are welcome. The easiest places to start are the parts that are
designed to grow:

- **A new notification type.** Look at `internal/notifier/webhook.go` — it's
  about 40 lines. Email, Discord, desktop notifications, and others would all
  follow the same shape: implement two methods, add one line of wiring, add it
  to the config validation. The core doesn't change.
- **More schedule phrases.** `internal/schedule/human.go` holds the readable
  syntax. New patterns go there.
- **The Windows command runner.** `internal/executor/executor_windows.go` kills
  a job's process tree with `taskkill`. A proper Job Object would be more
  reliable and is a good, self-contained improvement.

Whatever you add, the rule is the same: depend on the interface, not on the
core's internals, and give any new behavior a documented default.

Run the tests before opening a pull request:

```sh
go test ./...
```

## License

MIT — see [LICENSE](./LICENSE). Free to use, change, and build on, including for
commercial work. Provided as-is, with no warranty.
