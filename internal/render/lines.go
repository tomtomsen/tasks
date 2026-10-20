package render

import (
	"fmt"
	"strings"
	"time"

	"github.com/tomtomsen/tasks/internal/task"
)

// Spinner frames for the animated running indicator.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// ANSI styling codes.
const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiGreen  = "\x1b[32m"
	ansiRed    = "\x1b[31m"
	ansiCyan   = "\x1b[36m"
	ansiYellow = "\x1b[33m"
)

// glyphFor returns the status indicator for a state. frame and idx are used to
// offset spinner animation so concurrent tasks appear slightly out of phase.
func glyphFor(s task.State, frame, idx int) string {
	switch s {
	case task.StateRunning:
		return spinnerFrames[(frame+idx)%len(spinnerFrames)]
	case task.StateSucceeded:
		return "✓"
	case task.StateFailed:
		return "✗"
	case task.StateSkipped:
		return "–"
	case task.StateCancelled:
		return "×"
	default:
		return "○"
	}
}

// colorizeGlyph applies color to a status glyph.
func colorizeGlyph(s task.State, glyph string, color bool) string {
	if !color {
		return glyph
	}
	switch s {
	case task.StateSucceeded:
		return ansiGreen + glyph + ansiReset
	case task.StateFailed:
		return ansiRed + glyph + ansiReset
	case task.StateRunning:
		return ansiCyan + glyph + ansiReset
	case task.StateSkipped, task.StateCancelled:
		return ansiDim + glyph + ansiReset
	default:
		return ansiDim + glyph + ansiReset
	}
}

// formatDuration renders a duration in a compact, human-friendly form.
func formatDuration(d time.Duration) string {
	if d < time.Millisecond {
		return "0.0s"
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// visibleLen returns the number of runes in s.
func visibleLen(s string) int {
	return len([]rune(s))
}

// truncate cuts s to at most max runes, appending an ellipsis when truncated.
func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 0 {
		return ""
	}
	if max == 1 {
		return "…"
	}
	return string(r[:max-1]) + "…"
}

// renderTree builds the visible lines for a task tree, honoring the collapse
// option and terminal width constraints. When collapse is enabled, a
// successfully completed group is rendered as a single line; running, failed,
// pending, skipped, and cancelled groups always remain expanded.
func renderTree(snaps []task.Snapshot, frame, width int, color, collapse bool) []string {
	lines := make([]string, 0, len(snaps)*2)
	for i, s := range snaps {
		renderNode(s, 0, frame, i, width, color, collapse, &lines)
	}
	return lines
}

func renderNode(s task.Snapshot, depth, frame, idx, width int, color, collapse bool, lines *[]string) {
	indent := strings.Repeat("  ", depth)
	glyphPlain := glyphFor(s.State, frame, idx)

	var suffixPlain string
	if s.State == task.StateRunning && s.Description != "" {
		suffixPlain = "  " + s.Description
	} else if s.State.Terminal() && s.Duration > 0 {
		suffixPlain = "  " + formatDuration(s.Duration)
	}

	// Truncate the plain name so the whole line fits within the terminal width.
	// Lengths are measured on the uncolored text.
	prefixPlain := indent + glyphPlain + " "
	available := width - visibleLen(prefixPlain) - visibleLen(suffixPlain)
	if available < 1 {
		available = 1
	}
	name := truncate(s.Name, available)

	glyph := colorizeGlyph(s.State, glyphPlain, color)
	displayName := name
	if s.IsGroup && color {
		displayName = ansiBold + name + ansiReset
	}
	suffix := suffixPlain
	if suffixPlain != "" && color {
		suffix = ansiDim + suffixPlain + ansiReset
	}

	*lines = append(*lines, indent+glyph+" "+displayName+suffix)

	// Only successfully completed groups collapse, and only when the collapse
	// option is enabled. Everything else stays expanded.
	expanded := !collapse || !s.IsGroup || s.State != task.StateSucceeded
	if !expanded {
		return
	}
	for j, c := range s.Children {
		renderNode(c, depth+1, frame, idx+j, width, color, collapse, lines)
	}
}
