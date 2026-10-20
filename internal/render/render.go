// Package render implements the terminal renderers.
//
// Two renderers are provided behind a small interface: an interactive inline
// renderer for terminals (animated spinners, hierarchical tree, collapsing of
// successful groups) and a plain line-oriented renderer for pipes and CI.
package render

import (
	"fmt"
	"io"
	"time"

	"github.com/tomtomsen/tasks/internal/task"
)

// Mode selects a renderer.
type Mode int

const (
	// ModeAuto picks interactive on a TTY and plain otherwise.
	ModeAuto Mode = iota
	// ModePlain is the noninteractive line-oriented renderer.
	ModePlain
	// ModeInteractive is the animated inline renderer.
	ModeInteractive
)

// RefreshInterval is the interactive renderer's animation frame period.
const RefreshInterval = 50 * time.Millisecond

// FallbackWidth is used when the terminal width cannot be determined.
const FallbackWidth = 80

// Renderer consumes task state snapshots and presents them.
type Renderer interface {
	// Start begins rendering (hides the cursor for interactive mode).
	Start()
	// Update renders a new state snapshot.
	Update(snap []task.Snapshot)
	// Stop finalizes rendering, leaving the final frame visible.
	Stop(snap []task.Snapshot)
}

// Options configures a renderer.
type Options struct {
	Mode Mode
	Out  io.Writer
	// Size returns the terminal dimensions. If nil, a fallback is used.
	Size func() (width, height int)
	// NoColor disables ANSI colors in interactive mode.
	NoColor bool
	// Collapse enables collapsing of successfully completed groups in the
	// interactive renderer. It has no effect on the plain renderer.
	Collapse bool
}

// SelectMode resolves the renderer mode from an explicit flag and TTY status.
func SelectMode(requested string, tty bool) (Mode, error) {
	switch requested {
	case "":
		if tty {
			return ModeInteractive, nil
		}
		return ModePlain, nil
	case "plain":
		return ModePlain, nil
	case "interactive":
		if !tty {
			return ModeInteractive, fmt.Errorf("interactive renderer requires a terminal (stdout is not a TTY)")
		}
		return ModeInteractive, nil
	default:
		return ModeAuto, fmt.Errorf("unknown renderer %q (expected \"plain\" or \"interactive\")", requested)
	}
}

// New creates a renderer for the given options.
func New(opts Options) Renderer {
	switch opts.Mode {
	case ModeInteractive:
		return newInteractive(opts)
	default:
		return newPlain(opts.Out)
	}
}
