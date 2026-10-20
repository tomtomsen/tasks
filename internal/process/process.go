// Package process executes shell commands and captures their output.
//
// Commands run through an explicit POSIX shell ("/bin/sh -c") so that
// configuration commands may use normal shell syntax. Each command runs in its
// own process group so that timeouts and cancellation can terminate the whole
// process tree rather than leaving orphaned children.
package process

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// Shell is the interpreter used to run configured commands.
const Shell = "/bin/sh"

// Spec describes a command to run.
type Spec struct {
	Command    string
	WorkingDir string
	Env        []string
	Timeout    time.Duration
}

// Result is the outcome of running a command.
type Result struct {
	ExitCode  int
	Stdout    []byte
	Stderr    []byte
	Duration  time.Duration
	TimedOut  bool
	Cancelled bool
	StartErr  string
}

// Failed reports whether the command failed (as opposed to being cancelled).
func (r Result) Failed() bool {
	return r.StartErr != "" || r.TimedOut || r.ExitCode != 0
}

// Executor runs command specs.
type Executor struct{}

// NewExecutor returns a process executor.
func NewExecutor() *Executor { return &Executor{} }

// Run executes the command and blocks until it finishes, times out, or the
// context is cancelled.
func (e *Executor) Run(ctx context.Context, spec Spec) Result {
	cmd := exec.Command(Shell, "-c", spec.Command)
	cmd.Dir = spec.WorkingDir
	cmd.Env = spec.Env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return Result{
			ExitCode: -1,
			Duration: time.Since(start),
			StartErr: err.Error(),
		}
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var timeoutC <-chan time.Time
	var timer *time.Timer
	if spec.Timeout > 0 {
		timer = time.NewTimer(spec.Timeout)
		timeoutC = timer.C
	}

	select {
	case err := <-done:
		if timer != nil {
			timer.Stop()
		}
		r := resultFromError(err)
		r.Stdout = stdout.Bytes()
		r.Stderr = stderr.Bytes()
		r.Duration = time.Since(start)
		return r

	case <-ctx.Done():
		if timer != nil {
			timer.Stop()
		}
		killGroup(cmd)
		<-done
		return Result{
			ExitCode:  -1,
			Stdout:    stdout.Bytes(),
			Stderr:    stderr.Bytes(),
			Duration:  time.Since(start),
			Cancelled: true,
		}

	case <-timeoutC:
		killGroup(cmd)
		<-done
		return Result{
			ExitCode: -1,
			Stdout:   stdout.Bytes(),
			Stderr:   stderr.Bytes(),
			Duration: time.Since(start),
			TimedOut: true,
		}
	}
}

// resultFromError converts the error returned by cmd.Wait into a Result with
// the process exit code.
func resultFromError(err error) Result {
	if err == nil {
		return Result{ExitCode: 0}
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return Result{ExitCode: ee.ExitCode()}
	}
	return Result{ExitCode: -1}
}

// killGroup sends SIGKILL to the process group containing the command, which
// terminates the shell and all of its descendants. Sending to a negative pid
// targets the whole process group established via Setpgid.
func killGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	// Best effort: the process may already have exited.
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

// BuildEnv merges environment variable overrides into a copy of the current
// process environment, producing a clean KEY=VALUE list with no duplicate keys.
func BuildEnv(overrides map[string]string) []string {
	base := os.Environ()
	m := make(map[string]string, len(base)+len(overrides))
	for _, kv := range base {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				m[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	for k, v := range overrides {
		m[k] = v
	}
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}
