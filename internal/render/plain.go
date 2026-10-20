package render

import (
	"fmt"
	"io"

	"github.com/tomtomsen/tasks/internal/task"
)

// plainRenderer is the noninteractive line-oriented renderer. It emits a
// stable, machine-friendly log with no ANSI escapes or cursor movement.
type plainRenderer struct {
	out      io.Writer
	lastSeen map[string]task.State
}

func newPlain(out io.Writer) *plainRenderer {
	return &plainRenderer{
		out:      out,
		lastSeen: make(map[string]task.State),
	}
}

// leafInfo is a flattened view of a leaf task.
type leafInfo struct {
	Path     string
	State    task.State
	Duration string
}

func flattenLeaves(snaps []task.Snapshot, out *[]leafInfo) {
	for _, s := range snaps {
		if s.IsGroup {
			flattenLeaves(s.Children, out)
			continue
		}
		*out = append(*out, leafInfo{
			Path:     s.Path,
			State:    s.State,
			Duration: formatDuration(s.Duration),
		})
	}
}

func (r *plainRenderer) Start() {}

func (r *plainRenderer) Update(snaps []task.Snapshot) {
	var leaves []leafInfo
	flattenLeaves(snaps, &leaves)

	for _, l := range leaves {
		prev, seen := r.lastSeen[l.Path]
		if !seen {
			prev = task.StatePending
		}
		if l.State == prev {
			continue
		}

		// A task may start and finish between updates; emit both lines so the
		// log is complete.
		if prev == task.StatePending && l.State.Terminal() && l.State != task.StateSkipped && l.State != task.StateCancelled {
			r.printLine("START", l.Path, "")
		}

		switch l.State {
		case task.StateRunning:
			r.printLine("START", l.Path, "")
		case task.StateSucceeded:
			r.printLine("PASS", l.Path, l.Duration)
		case task.StateFailed:
			r.printLine("FAIL", l.Path, l.Duration)
		case task.StateSkipped:
			r.printLine("SKIP", l.Path, "")
		case task.StateCancelled:
			r.printLine("CANCEL", l.Path, "")
		}
		r.lastSeen[l.Path] = l.State
	}
}

func (r *plainRenderer) Stop(snaps []task.Snapshot) {
	r.Update(snaps)
}

func (r *plainRenderer) printLine(label, path, extra string) {
	line := fmt.Sprintf("%-6s %s", label, path)
	if extra != "" {
		line += " (" + extra + ")"
	}
	fmt.Fprintln(r.out, line)
}
