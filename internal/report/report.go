// Package report builds and prints the post-execution output: the failure
// report, the summary, and the optional full log file.
package report

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/tomtomsen/tasks/internal/task"
)

const separator = "────────────────────────────────────────"

// Failure describes a single failed leaf command.
type Failure struct {
	Path       string
	Command    string
	WorkingDir string
	ExitCode   int
	TimedOut   bool
	StartErr   string
	Stdout     []byte
	Stderr     []byte
	Duration   string
}

// Report summarizes the outcome of a run.
type Report struct {
	Failures  []Failure
	Total     int
	Passed    int
	Failed    int
	Skipped   int
	Cancelled int
}

// Build computes a report from the state model.
func Build(model *task.Model) Report {
	var rep Report
	for _, n := range model.Leaves() {
		rep.Total++
		switch n.State() {
		case task.StateSucceeded:
			rep.Passed++
		case task.StateFailed:
			rep.Failed++
			r := n.Result()
			rep.Failures = append(rep.Failures, Failure{
				Path:       n.Def.Path,
				Command:    n.Def.Command,
				WorkingDir: n.Def.WorkingDir,
				ExitCode:   r.ExitCode,
				TimedOut:   r.TimedOut,
				StartErr:   r.StartErr,
				Stdout:     r.Stdout,
				Stderr:     r.Stderr,
				Duration:   formatDuration(r.Duration),
			})
		case task.StateSkipped:
			rep.Skipped++
		case task.StateCancelled:
			rep.Cancelled++
		}
	}
	return rep
}

// ExitCode returns the process exit code for the report (0 or 1).
func (r Report) ExitCode() int {
	if r.Failed > 0 {
		return 1
	}
	return 0
}

// WriteFailures prints the complete failure report, one entry per failed task
// in deterministic configuration order.
func WriteFailures(w io.Writer, r Report) {
	if len(r.Failures) == 0 {
		return
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, separator)
	fmt.Fprintf(w, "FAILURES (%d)\n", len(r.Failures))
	fmt.Fprintln(w, separator)

	for _, f := range r.Failures {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "✗ %s\n", f.Path)
		fmt.Fprintln(w)
		fmt.Fprintf(w, "Command: %s\n", f.Command)
		fmt.Fprintf(w, "Working directory: %s\n", f.WorkingDir)
		switch {
		case f.StartErr != "":
			fmt.Fprintf(w, "Error: %s\n", f.StartErr)
		case f.TimedOut:
			fmt.Fprintf(w, "Timed out after %s\n", f.Duration)
		default:
			fmt.Fprintf(w, "Exit code: %d\n", f.ExitCode)
		}
		fmt.Fprintln(w)
		writeStream(w, "stdout", f.Stdout)
		writeStream(w, "stderr", f.Stderr)
		fmt.Fprintln(w, separator)
	}
}

// WriteVerbose prints captured output for successful tasks as well, for
// --verbose mode.
func WriteVerbose(w io.Writer, model *task.Model) {
	var successes []*task.Node
	for _, n := range model.Leaves() {
		if n.State() == task.StateSucceeded {
			successes = append(successes, n)
		}
	}
	if len(successes) == 0 {
		return
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, separator)
	fmt.Fprintf(w, "SUCCESSFUL TASKS (%d)\n", len(successes))
	fmt.Fprintln(w, separator)

	for _, n := range successes {
		r := n.Result()
		fmt.Fprintln(w)
		fmt.Fprintf(w, "✓ %s\n", n.Def.Path)
		fmt.Fprintln(w)
		fmt.Fprintf(w, "Command: %s\n", n.Def.Command)
		fmt.Fprintf(w, "Working directory: %s\n", n.Def.WorkingDir)
		fmt.Fprintf(w, "Duration: %s\n", formatDuration(r.Duration))
		fmt.Fprintln(w)
		writeStream(w, "stdout", r.Stdout)
		writeStream(w, "stderr", r.Stderr)
		fmt.Fprintln(w, separator)
	}
}

// WriteSummary prints the final one-line summary.
func WriteSummary(w io.Writer, r Report) {
	fmt.Fprintln(w)
	switch {
	case r.Failed == 0 && r.Skipped == 0 && r.Cancelled == 0:
		fmt.Fprintf(w, "All %d checks passed.\n", r.Passed)
	case r.Failed == 0:
		fmt.Fprintf(w, "%d checks passed", r.Passed)
		writeSuffix(w, r.Skipped, "skipped", r.Cancelled, "cancelled")
		fmt.Fprintln(w, ".")
	default:
		fmt.Fprintf(w, "%d checks passed, %d failed", r.Passed, r.Failed)
		writeSuffix(w, r.Skipped, "skipped", r.Cancelled, "cancelled")
		fmt.Fprintln(w, ".")
	}
}

func writeSuffix(w io.Writer, n1 int, label1 string, n2 int, label2 string) {
	if n1 > 0 {
		fmt.Fprintf(w, ", %d %s", n1, label1)
	}
	if n2 > 0 {
		fmt.Fprintf(w, ", %d %s", n2, label2)
	}
}

// WriteLogFile writes a full log reconstructing which task produced each
// message. Each task's command, working directory, exit code, and complete
// stdout/stderr are recorded.
func WriteLogFile(path string, model *task.Model) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	fmt.Fprintf(f, "# tasks run log\n")
	for _, n := range model.Leaves() {
		r := n.Result()
		fmt.Fprintf(f, "\n=== %s ===\n", n.Def.Path)
		fmt.Fprintf(f, "command: %s\n", n.Def.Command)
		fmt.Fprintf(f, "working_dir: %s\n", n.Def.WorkingDir)
		fmt.Fprintf(f, "state: %s\n", n.State())
		fmt.Fprintf(f, "exit_code: %d\n", r.ExitCode)
		fmt.Fprintf(f, "duration: %s\n", formatDuration(r.Duration))
		if r.TimedOut {
			fmt.Fprintf(f, "timed_out: true\n")
		}
		if r.StartErr != "" {
			fmt.Fprintf(f, "error: %s\n", r.StartErr)
		}
		fmt.Fprintf(f, "--- stdout ---\n")
		if _, err := f.Write(r.Stdout); err != nil {
			return err
		}
		if len(r.Stdout) == 0 || r.Stdout[len(r.Stdout)-1] != '\n' {
			fmt.Fprintln(f)
		}
		fmt.Fprintf(f, "--- stderr ---\n")
		if _, err := f.Write(r.Stderr); err != nil {
			return err
		}
		if len(r.Stderr) == 0 || r.Stderr[len(r.Stderr)-1] != '\n' {
			fmt.Fprintln(f)
		}
	}
	return f.Close()
}

func writeStream(w io.Writer, label string, data []byte) {
	if len(data) == 0 {
		return
	}
	fmt.Fprintf(w, "Output (%s):\n", label)
	writeIndented(w, data, "  ")
	fmt.Fprintln(w)
}

func writeIndented(w io.Writer, data []byte, indent string) {
	s := strings.TrimSuffix(string(data), "\n")
	if s == "" {
		return
	}
	for _, line := range strings.Split(s, "\n") {
		fmt.Fprintf(w, "%s%s\n", indent, line)
	}
}

func formatDuration(d time.Duration) string {
	if d < time.Millisecond {
		return "0.0s"
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}
