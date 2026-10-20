package render

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/tomtomsen/tasks/internal/task"
)

func leaf(name string, s task.State) task.Snapshot {
	return task.Snapshot{Name: name, Path: name, State: s, IsGroup: false}
}

func group(name string, s task.State, children ...task.Snapshot) task.Snapshot {
	return task.Snapshot{Name: name, Path: name, State: s, IsGroup: true, Children: children}
}

func TestSuccessfulGroupCollapses(t *testing.T) {
	snaps := []task.Snapshot{
		group("root", task.StateSucceeded,
			group("child", task.StateSucceeded, leaf("a", task.StateSucceeded)),
		),
	}
	lines := renderTree(snaps, 0, 80, false)
	if len(lines) != 1 {
		t.Fatalf("collapsed group rendered %d lines: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "root") {
		t.Fatalf("line = %q", lines[0])
	}
}

func TestFailedGroupExpands(t *testing.T) {
	snaps := []task.Snapshot{
		group("root", task.StateFailed,
			group("child", task.StateFailed, leaf("a", task.StateFailed)),
		),
	}
	lines := renderTree(snaps, 0, 80, false)
	if len(lines) != 3 {
		t.Fatalf("failed group rendered %d lines: %v", len(lines), lines)
	}
	if !strings.Contains(lines[1], "child") || !strings.Contains(lines[2], "a") {
		t.Fatalf("lines = %v", lines)
	}
}

func TestRunningGroupExpands(t *testing.T) {
	snaps := []task.Snapshot{
		group("root", task.StateRunning,
			leaf("a", task.StateRunning),
			leaf("b", task.StatePending),
		),
	}
	lines := renderTree(snaps, 0, 80, false)
	if len(lines) != 3 {
		t.Fatalf("running group rendered %d lines: %v", len(lines), lines)
	}
}

func TestDeepNesting(t *testing.T) {
	snaps := []task.Snapshot{
		group("l0", task.StateFailed,
			group("l1", task.StateFailed,
				group("l2", task.StateFailed,
					leaf("l3", task.StateFailed),
				),
			),
		),
	}
	lines := renderTree(snaps, 0, 80, false)
	if len(lines) != 4 {
		t.Fatalf("got %d lines: %v", len(lines), lines)
	}
	for i, l := range lines {
		wantIndent := strings.Repeat("  ", i)
		if !strings.HasPrefix(l, wantIndent) {
			t.Fatalf("line %d = %q, want indent %q", i, l, wantIndent)
		}
	}
}

func TestTruncation(t *testing.T) {
	snaps := []task.Snapshot{leaf("a-very-long-task-name-that-exceeds-width", task.StateSucceeded)}
	lines := renderTree(snaps, 0, 20, false)
	for _, l := range lines {
		if visibleLen(l) > 20 {
			t.Fatalf("line too long (%d): %q", visibleLen(l), l)
		}
	}
}

func TestDurationFormatting(t *testing.T) {
	if got := formatDuration(1200 * time.Millisecond); got != "1.2s" {
		t.Fatalf("1200ms = %q", got)
	}
	if got := formatDuration(150 * time.Millisecond); got != "150ms" {
		t.Fatalf("150ms = %q", got)
	}
}

func TestPlainNoEscapeSequences(t *testing.T) {
	var buf bytes.Buffer
	r := newPlain(&buf)
	r.Start()

	// Pending -> Running (START), then Running -> Succeeded (PASS).
	r.Update([]task.Snapshot{leaf("A", task.StateRunning)})
	r.Update([]task.Snapshot{
		leaf("A", task.StateSucceeded),
		leaf("B", task.StateFailed),
	})

	out := buf.String()
	if strings.Contains(out, "\x1b") {
		t.Fatalf("plain output contains escape sequences: %q", out)
	}
	if !strings.Contains(out, "START  A") {
		t.Fatalf("missing START line: %q", out)
	}
	if !strings.Contains(out, "PASS   A") {
		t.Fatalf("missing PASS line: %q", out)
	}
	if !strings.Contains(out, "FAIL   B") {
		t.Fatalf("missing FAIL line: %q", out)
	}
}

func TestPlainFastTaskPrintsBothLines(t *testing.T) {
	var buf bytes.Buffer
	r := newPlain(&buf)
	// A task that starts and finishes before the renderer sees it should
	// produce both a START and a result line.
	r.Update([]task.Snapshot{leaf("A", task.StateSucceeded)})
	out := buf.String()
	if !strings.Contains(out, "START  A") || !strings.Contains(out, "PASS   A") {
		t.Fatalf("expected START and PASS for fast task: %q", out)
	}
}
