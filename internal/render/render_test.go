package render

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/tomtomsen/tasks/internal/task"
)

func leaf(name string, s task.State) task.Snapshot {
	return task.Snapshot{Name: name, Path: name, State: s, IsGroup: false}
}

func group(name string, s task.State, children ...task.Snapshot) task.Snapshot {
	return task.Snapshot{Name: name, Path: name, State: s, IsGroup: true, Children: children}
}

// --- collapse behavior (pure renderTree) ---

func TestCollapseDefaultsToOff(t *testing.T) {
	snaps := []task.Snapshot{
		group("root", task.StateSucceeded,
			group("child", task.StateSucceeded, leaf("a", task.StateSucceeded)),
		),
	}
	lines := renderTree(snaps, 0, 80, false, false)
	if len(lines) != 3 {
		t.Fatalf("collapse off rendered %d lines, want 3: %v", len(lines), lines)
	}
}

func TestCollapseOnCollapsesSuccessfulGroups(t *testing.T) {
	snaps := []task.Snapshot{
		group("root", task.StateSucceeded,
			group("child", task.StateSucceeded, leaf("a", task.StateSucceeded)),
		),
	}
	lines := renderTree(snaps, 0, 80, false, true)
	if len(lines) != 1 {
		t.Fatalf("collapse on rendered %d lines, want 1: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "root") {
		t.Fatalf("line = %q", lines[0])
	}
}

func TestCollapseOnKeepsFailedGroupsExpanded(t *testing.T) {
	snaps := []task.Snapshot{
		group("root", task.StateFailed,
			group("child", task.StateFailed, leaf("a", task.StateFailed)),
		),
	}
	lines := renderTree(snaps, 0, 80, false, true)
	if len(lines) != 3 {
		t.Fatalf("failed group rendered %d lines, want 3: %v", len(lines), lines)
	}
	if !strings.Contains(lines[2], "a") {
		t.Fatalf("child missing: %v", lines)
	}
}

func TestCollapseOnKeepsRunningAndPendingExpanded(t *testing.T) {
	snaps := []task.Snapshot{
		group("root", task.StateRunning,
			leaf("a", task.StateRunning),
			leaf("b", task.StatePending),
		),
	}
	lines := renderTree(snaps, 0, 80, false, true)
	if len(lines) != 3 {
		t.Fatalf("running group rendered %d lines, want 3: %v", len(lines), lines)
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
	lines := renderTree(snaps, 0, 80, false, false)
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
	lines := renderTree(snaps, 0, 20, false, false)
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

// --- plain renderer (unchanged by collapse) ---

func TestPlainNoEscapeSequences(t *testing.T) {
	var buf bytes.Buffer
	r := newPlain(&buf)
	r.Start()

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
	r.Update([]task.Snapshot{leaf("A", task.StateSucceeded)})
	out := buf.String()
	if !strings.Contains(out, "START  A") || !strings.Contains(out, "PASS   A") {
		t.Fatalf("expected START and PASS for fast task: %q", out)
	}
}

// --- fake terminal + interactive renderer lifecycle ---

// fakeTerm is a minimal ANSI screen model sufficient to verify the interactive
// renderer's output: it handles cursor-up, line erase, and newlines and strips
// SGR color codes. It does not model line wrapping (test lines are short).
type fakeTerm struct {
	lines []string
	cur   int
}

func (t *fakeTerm) ensure(n int) {
	for len(t.lines) <= n {
		t.lines = append(t.lines, "")
	}
}

func (t *fakeTerm) feed(b []byte) {
	i := 0
	for i < len(b) {
		switch {
		case b[i] == 0x1b && i+1 < len(b) && b[i+1] == '[':
			j := i + 2
			for j < len(b) && (b[j] < 0x40 || b[j] > 0x7e) {
				j++
			}
			if j < len(b) {
				t.csi(string(b[i+2:j]), b[j])
				i = j + 1
			} else {
				i++
			}
		case b[i] == '\n':
			t.cur++
			t.ensure(t.cur)
			i++
		case b[i] == '\r':
			i++
		case b[i] < 0x20:
			i++
		default:
			r, size := utf8.DecodeRune(b[i:])
			t.ensure(t.cur)
			t.lines[t.cur] += string(r)
			i += size
		}
	}
}

func (t *fakeTerm) csi(params string, final byte) {
	switch final {
	case 'A':
		n := 1
		if params != "" {
			fmt.Sscanf(params, "%d", &n)
		}
		t.cur -= n
		if t.cur < 0 {
			t.cur = 0
		}
		t.ensure(t.cur)
	case 'K':
		t.ensure(t.cur)
		t.lines[t.cur] = ""
	}
	// All other sequences (SGR colors, cursor show/hide, etc.) are ignored.
}

func (t *fakeTerm) Write(p []byte) (int, error) {
	t.feed(p)
	return len(p), nil
}

// display returns the visible lines with trailing empty lines trimmed.
func (t *fakeTerm) display() []string {
	out := append([]string(nil), t.lines...)
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

func renderSequence(t *testing.T, collapse bool, frames ...[]task.Snapshot) []string {
	t.Helper()
	term := &fakeTerm{}
	r := New(Options{
		Mode:     ModeInteractive,
		Out:      term,
		Size:     func() (int, int) { return 80, 24 },
		Collapse: collapse,
	})
	r.Start()
	for _, f := range frames {
		r.Update(f)
	}
	r.Stop(frames[len(frames)-1])
	return term.display()
}

func succeededRoot() task.Snapshot {
	return task.Snapshot{
		Name: "root", Path: "root", State: task.StateSucceeded, IsGroup: true,
		Duration: 4 * time.Second,
		Children: []task.Snapshot{
			{Name: "child", Path: "root > child", State: task.StateSucceeded, IsGroup: false, Duration: time.Second},
		},
	}
}

func runningRoot() task.Snapshot {
	return task.Snapshot{
		Name: "root", Path: "root", State: task.StateRunning, IsGroup: true,
		Children: []task.Snapshot{
			{Name: "child", Path: "root > child", State: task.StateRunning, IsGroup: false},
		},
	}
}

func TestRendererFinalTreeAppearsOnceWhenCollapsing(t *testing.T) {
	display := renderSequence(t, true, []task.Snapshot{runningRoot()}, []task.Snapshot{succeededRoot()})

	if len(display) != 1 {
		t.Fatalf("final screen has %d lines, want 1: %#v", len(display), display)
	}
	line := display[0]
	if !strings.Contains(line, "root") || !strings.Contains(line, "✓") {
		t.Fatalf("final line = %q", line)
	}
	if strings.Contains(line, "child") {
		t.Fatalf("stale child content remains: %q", line)
	}
}

func TestRendererNoStaleContentWhenShrinking(t *testing.T) {
	// Grow to many lines, then shrink to one; stale lines must be cleared.
	big := []task.Snapshot{
		group("root", task.StateRunning,
			leaf("a", task.StateRunning),
			leaf("b", task.StateRunning),
			leaf("c", task.StateRunning),
			leaf("d", task.StateRunning),
		),
	}
	small := []task.Snapshot{succeededRoot()}

	display := renderSequence(t, true, big, small)

	for _, line := range display {
		if strings.Contains(line, "a") || strings.Contains(line, "b") ||
			strings.Contains(line, "c") || strings.Contains(line, "d") {
			t.Fatalf("stale task content remains: %#v", display)
		}
	}
	if len(display) != 1 {
		t.Fatalf("final screen has %d lines, want 1: %#v", len(display), display)
	}
}

func TestRendererCollapseOffKeepsAllSubtasks(t *testing.T) {
	display := renderSequence(t, false, []task.Snapshot{succeededRoot()})

	if len(display) != 2 {
		t.Fatalf("collapse off final screen has %d lines, want 2: %#v", len(display), display)
	}
	if !strings.Contains(display[0], "root") || !strings.Contains(display[1], "child") {
		t.Fatalf("subtasks not visible: %#v", display)
	}
}
