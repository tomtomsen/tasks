# tasks

A standalone command-line tool that runs configurable shell commands
concurrently and shows progress through a clean, interactive terminal
interface. It is designed for linting, static analysis, and tests across a
monorepo — especially from Git pre-commit hooks — but is general-purpose.

Key properties:

- Arbitrarily nested task groups and shell commands defined in YAML.
- Commands run concurrently up to a configurable limit.
- A live, hierarchical progress view (spinners, collapsing, inline updates).
- Subprocess output never corrupts the terminal: it is captured and shown in a
  deterministic failure report after execution.
- Fails fast in CI with a stable, line-oriented, ANSI-free log.
- A single compiled executable with no runtime dependencies.

## Design principles

The implementation is split into small, focused packages with a unidirectional
flow:

```
Configuration  ->  Task Tree  ->  Scheduler  ->  State Model  ->  Renderer
```

- **config** parses and validates YAML, resolving working directories and
  environment inheritance.
- **task** holds the runtime task tree and the explicit state machine
  (`pending`, `running`, `succeeded`, `failed`, `skipped`, `cancelled`).
  Group states are *derived* from their descendants.
- **runner** schedules leaf commands against a bounded worker pool and applies
  results to the state model. It never touches the terminal.
- **process** executes commands through `/bin/sh -c` in their own process
  group, capturing stdout and stderr separately.
- **render** draws the two renderers (interactive and plain) from state
  snapshots.
- **report** prints deferred failures, verbose output, and the summary.

The scheduler, the state model, and the renderers are independent: the state
model supports deterministic unit tests without spawning real processes, and
renderers consume snapshots without controlling execution.

## Installation

```sh
go build -o tasks ./cmd/tasks
```

Or install to your `GOBIN`:

```sh
go install ./cmd/tasks
```

The result is a single executable for Linux or macOS. There is no Node.js,
PHP, Python, or other runtime dependency.

## Configuration

The configuration file is `tasks.yaml` by default; select another file with
`--config`:

```sh
tasks run --config=custom.yaml
```

```yaml
version: 1

settings:
  concurrency: 4          # maximum simultaneously running commands
  continue_on_error: true # keep running independent tasks after a failure

tasks:
  - name: Pre-commit checks
    tasks:
      - name: Core
        description: Shared domain libraries
        tasks:
          - name: PHPStan
            command: just core-phpstan

          - name: PHPUnit
            command: just core-test

      - name: Web Admin
        working_dir: apps/web-admin
        env:
          APP_ENV: test
        tasks:
          - name: PHPStan
            command: just phpstan
            timeout: 5m
```

### Task semantics

- A task is either a **group** (has child `tasks`) or a **command** (has
  `command`). A node with both, or neither, is rejected.
- Nesting depth is arbitrary.
- Sibling tasks are independent and eligible to run concurrently.
- Groups do not consume concurrency slots; only commands do.
- A parent succeeds only when every descendant succeeds, and fails when any
  descendant fails.
- `working_dir` is inherited by descendants unless overridden, and is resolved
  relative to the configuration file's directory (not the caller's working
  directory). The default working directory is the configuration file's
  directory.
- `env` variables are inherited and overridden by descendants, and are merged
  into the tool's environment at execution time.
- `timeout` is an optional Go duration (`30s`, `5m`, `1h30m`) per command.

### Validation

The configuration is validated before any command runs. The version must be
`1`. Empty groups, missing commands, ambiguous nodes, invalid concurrency, and
invalid timeouts are all rejected with messages that identify the offending
task path (e.g. `Pre-commit checks > Web Admin`).

Defaults: `concurrency` defaults to the number of CPUs; `continue_on_error`
defaults to `true`.

## Execution and concurrency

Commands run through an explicit POSIX shell (`/bin/sh -c`), so configuration
commands may use normal shell syntax. Success is determined solely by the
process exit status — task output is never interpreted.

A worker pool of `concurrency` workers drains leaf commands in configuration
order. Each command captures stdout and stderr separately, records its exit
code and duration, and keeps its own output buffer (there is no limit, so
diagnostics are never truncated by default).

### Failure handling

By default (`continue_on_error: true`), independent tasks keep running after a
failure. A failed task never blocks unrelated tasks.

With `continue_on_error: false`, the runner stops scheduling new commands after
the first failure and marks not-yet-started commands as *skipped*. Commands
already running are allowed to finish. A small number of commands already
queued may still start; this is a deliberate, documented best-effort bound.

Skipped and cancelled tasks are distinct from failed tasks.

### Cancellation

`Ctrl+C` (SIGINT) and `SIGTERM` cancel execution. Each command runs in its own
process group, so cancellation sends SIGKILL to the whole group, terminating
the shell and its descendants rather than leaking orphans. The terminal state
is restored, and the process exits `130` on SIGINT and `143` on SIGTERM. A
second signal forces immediate exit.

## Rendering

Two renderers are selected automatically, or explicitly with `--renderer`:

- **interactive** (default on a TTY): animated spinners, a hierarchical tree,
  successful groups collapse automatically, failed and running groups stay
  expanded, durations on completed tasks, in-place updates with a hidden
  cursor. It does not use an alternate screen buffer, so the final result
  remains in the terminal scrollback.
- **plain** (default when output is redirected or in CI): stable,
  line-oriented `START`/`PASS`/`FAIL`/`SKIP`/`CANCEL` records with no ANSI
  cursor movement, no spinners, and no color.

Selecting `--renderer=interactive` without a usable TTY fails with a clear
error.

During execution only the renderer writes to the terminal. Subprocess output
is buffered and never interleaves with the live view.

## Output and error reporting

After all runnable tasks finish, the runner stops the live renderer and prints,
in order:

1. the final task tree (interactive) or progress lines (plain),
2. the failure report — one entry per failed task, grouped by task path in
   configuration order, with the command, working directory, exit code, and
   complete captured stdout/stderr (never truncated by default),
3. the summary (`All 12 checks passed.` or `11 checks passed, 1 failed.`).

```sh
tasks run --verbose             # also include successful tasks' output
tasks run --log-file=tasks.log  # write a full per-task log
```

The log file records each task's command, working directory, exit code,
duration, and complete stdout and stderr, so any message can be attributed to
the task that produced it.

## CLI

```sh
tasks run                         # run tasks.yaml
tasks run --config=custom.yaml
tasks run --concurrency=8         # override settings.concurrency
tasks run --renderer=plain
tasks run --renderer=interactive
tasks run --verbose
tasks run --log-file=tasks.log
tasks --version
tasks --help
```

Command-line options override the corresponding configuration settings.

Exit codes:

- `0` — all tasks succeeded.
- `1` — one or more tasks failed.
- `2` — configuration or usage error.
- `130` — interrupted by SIGINT.
- `143` — terminated by SIGTERM.

## Git hook integration

Add a `pre-commit` hook:

```sh
#!/bin/sh
set -eu

exec tasks run --config=tasks.yaml
```

The tool propagates its exit status, so a failure blocks the commit. The same
configuration runs unchanged from a hook, a terminal, or CI (where the plain
renderer is selected automatically).

## Running the example

```sh
go run ./cmd/tasks run --config=examples/tasks.yaml
```

The example uses only portable shell commands (`echo`, `sleep`), so it runs
without PHP, Composer, or PNPM.

## Testing

```sh
go test ./...
```

Fast unit tests use controlled fake executors and simulated state; a smaller
set of integration tests exercise real subprocesses, large output, timeouts,
cancellation, and process-group cleanup.

## Known limitations

- Commands are interpreted by `/bin/sh -c`; shell features are those of the
  platform's POSIX shell.
- stdout and stderr are captured separately, but their relative ordering is
  not preserved.
- On cancellation, process groups are terminated with SIGKILL (a second grace
  period is not attempted).
- There is no templating, condition logic, plugin system, or task dependency
  graph; tasks only form a hierarchy of independent siblings.
