# How cronhub is built

This explains the design of cronhub and the reasoning behind it. If you just want
to use the tool, the README covers that. Read this if you want to change cronhub,
add to it, or understand why it's put together the way it is.

## The one idea

There's a small core that runs the scheduling loop. Everything the core needs
from the outside world is defined as an interface: how to read a schedule, how to
run a command, where to keep state, how to send a notification, how to register
with the operating system. The core only ever talks to those interfaces. It never
directly refers to SQLite, systemd, cron syntax, or a shell.

Each interface has one working implementation today. When we want a second way of
doing something — a new notification channel, a new schedule format — we write
another implementation of the same interface and select it. The core doesn't
change. That's the whole point: new features are new files, not rewrites.

We call these interfaces "ports."

## Principles

**The core depends on interfaces, not on concrete code.** The scheduling loop is
handed its dependencies at startup and uses them through their interfaces. If the
core ever imports a specific database or a specific service manager directly, that
rule is broken and the design starts to rot. Keeping the core ignorant of the
concretes is what keeps everything else swappable.

**Every behavioral decision is a named choice with a documented default.** "What
happens if a job is still running when it's due again?" is not an `if` statement
buried somewhere. It's a policy with a name (`on_overlap`) and a stated default
(skip). A user who does nothing gets sensible behavior; a user who wants something
else changes one line. Undocumented behavior is how a tool loses people's trust.

**A beginner should be able to ignore almost everything.** An empty-ish config
runs correctly because every optional setting has a built-in default. Settings are
resolved in layers: built-in defaults, then the config's `[defaults]` section,
then each job's own fields. The most specific value wins. So a three-line job
works, and a job that overrides ten things also works.

**Fail loudly, and early.** A bad schedule, an unknown setting, a job pointing at
a notifier that doesn't exist — all of these stop cronhub before it starts, with a
clear message. The alternative (starting up and then silently doing the wrong
thing at 3am) is exactly the cron behavior we're trying to get away from.

**Ship one implementation per port; don't build the second one until it's
needed.** The interfaces are cheap and worth defining up front. The extra
implementations are a roadmap, not a to-do list. Building a container-based
command runner or a Postgres backend before anyone has asked is how a solo project
stalls.

**Use the operating system's service manager; never write one.** systemd, launchd,
and the Windows Service Control Manager already exist and already handle starting
things at boot and restarting them on crash. cronhub registers with them. Building
our own would mean replacing part of the operating system, which is both enormous
and pointless.

## The three pieces

cronhub has three parts, and they're deliberately separate.

The **engine** is a long-running process. It holds the jobs and fires them on
schedule. As long as it's running, jobs run — whether or not any particular
application on the machine is up.

The **service registration** is what keeps the engine alive across reboots and
crashes. It's a thin adapter that tells the operating system's service manager to
supervise the engine. Without it, the engine only runs while you keep a terminal
open, which is fine for testing and useless in production.

The **project side** is how jobs get declared: a config file (and the commands
that read and write it). The application whose jobs these are doesn't need to be
running for the jobs to fire — it just declares them once. The engine, running
separately, does the actual work.

This separation is the answer to "if my app is down, do my jobs stop?" They don't,
because the engine is not your app. It's its own process, kept alive by the
service manager.

## The ports

The core depends on these interfaces and nothing else concrete. Each has one
implementation today; the last column is what could be added later against the
same interface, without touching the core.

| Port            | What it decides                          | Today                        | Could be added later                     |
|-----------------|------------------------------------------|------------------------------|------------------------------------------|
| Schedule parser | How a schedule is written                | cron syntax + readable words | more readable phrases; one-shot times    |
| Trigger policy  | Whether a due (or missed) run happens    | skip missed runs             | replay missed runs (catch-up)            |
| Overlap policy  | What to do if the last run is still going| skip the new run             | queue, run in parallel, kill the old one |
| Executor        | How a command actually runs              | local shell                  | containers, SSH, HTTP calls              |
| Store           | Where jobs and history are kept          | SQLite (one file)            | Postgres for multiple machines           |
| Notifier        | How results are reported                 | log; webhook                 | email, Slack, Discord, desktop           |
| Service adapter | How the OS keeps the engine alive        | systemd / launchd / Windows  | refinements per platform                 |
| Config loader   | What the config file looks like          | TOML                         | other formats; config from a database    |

A note on the schedule parser, since it has an unusual shape: there are actually
two implementations (one for cron syntax, one for the readable words) plus a small
router that picks between them based on the first character of the schedule. The
core still sees a single schedule-parser interface and is unaware of the split.
This is the port design working exactly as intended — two implementations behind
one interface, chosen at the edge.

### What each interface promises

**Schedule parser.** Given a schedule string, return something that can answer
"when is the next run after this moment?" The core asks that question and nothing
else. It has no idea whether a cron expression or the phrase "every monday at 9am"
produced the answer.

**Trigger policy.** Given a scheduled time, the current time, and when the job last
ran, decide whether to run now or skip. Today's policy skips anything that was due
while the engine was down. Replaying missed runs would be a different policy.

**Overlap policy.** When a job comes due but its previous run hasn't finished,
decide what to do. Today: skip the new run. The other behaviors (queue it, allow
both, kill the old one) are defined in the interface but not implemented yet.

**Executor.** Run a job's command, enforce its timeout, and report back the exit
code, the output, and how long it took. This is the one port with real
platform-specific work inside it: stopping a command and everything it spawned is
genuinely different on Unix (process groups) and Windows (taskkill, and eventually
a Job Object). The interface hides that; the difference lives in files selected by
build tag.

**Store.** Save and load jobs, record each run, and read back a job's history.
Today it's a single SQLite file, which needs no setup and no server. The interface
is small enough that moving to a networked database later is a new implementation,
not surgery on everything.

**Notifier.** Report the outcome of a run. A job lists the notifiers it wants by
name. The `log` notifier is always available. Others (currently a webhook) are
declared in the config and built at startup. Because a notifier carries its own
settings in its own struct, the interface itself stays tiny: a name and a
"here's what happened" method.

**Service adapter.** Install, start, stop, and check the engine as an operating
system service. It wraps a library that already knows how to write the correct
service definition for each platform, so cronhub isn't hand-rolling systemd units
and launchd plists. It defaults to a per-user service, which needs no admin rights.

**Config loader.** Read the config file into plain in-memory structures. The core
only sees those structures, never the file format. We use TOML because it's
strict: a malformed file is an error, not a silent misreading. (Cron-adjacent
formats like YAML have a habit of turning `08:00` into a number and `NO` into
`false` — bad traits for a file that controls when things run.)

## The config, and how defaults resolve

A job needs a name, a schedule, and a command. Everything else is optional. The
config file also has an optional `[defaults]` section and optional `[[notifier]]`
declarations.

Values resolve in three layers, most specific winning:

1. Built-in defaults compiled into cronhub.
2. The config's `[defaults]` section.
3. Each job's own fields.

The config file format is versioned (`version = 1`) from the start, so a future
release can recognize and migrate an older file instead of misreading it.

## Cross-platform

cronhub builds to a single binary for macOS, Linux, and Windows. Most of the code
is platform-independent and the Go compiler handles the rest. The two places with
real per-platform work are:

- **The executor**, specifically stopping a running command and its children,
  which works differently on each OS. This lives in build-tagged files so the rest
  of the executor stays shared.
- **The service adapter**, which is handled by a library that emits the right
  service definition per platform.

Everything else — the loop, the parsers, the store, the config — is the same code
everywhere.

## What's intentionally not built yet

The interfaces above describe more than cronhub currently does. That's on purpose.
The following are designed for but not implemented, and shouldn't be built until
there's a real need:

- Replaying missed runs (catch-up).
- Overlap handling beyond "skip" (queue, parallel, kill).
- Running commands anywhere but the local machine.
- Storing state anywhere but the local SQLite file.
- One-shot schedules ("run once on Monday at 9pm and then stop") — this needs the
  engine to let a job retire itself, which is real new machinery, so it's its own
  future feature rather than part of the schedule syntax.
- A web interface, and multi-machine scheduling.

The design being ready for these is not a reason to build them.

## Adding to cronhub

Every port is a place you can extend without touching the core. The pattern is
always the same: write a new implementation of the interface, add one line that
constructs it, and add any validation for its config. The core stays as it is.

The best-documented example is the webhook notifier
(`internal/notifier/webhook.go`) — around 40 lines. A new notification channel
follows the same three steps. New schedule phrases go in
`internal/schedule/human.go`. The Windows command-stopping code
(`internal/executor/executor_windows.go`) is a good self-contained thing to
harden.

The rule for anything new: depend on the interface rather than the core's
internals, and give any new behavior a clear default.
