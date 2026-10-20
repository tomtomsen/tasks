package render

import (
	"bytes"
	"fmt"
	"io"

	"github.com/tomtomsen/tasks/internal/task"
)

// interactiveRenderer draws an animated, hierarchical progress view in place on
// a terminal. It does not use an alternate screen buffer: frames are drawn by
// moving the cursor up and overwriting previous lines, leaving the final state
// in the scrollback.
//
// Rendering invariant: after every frame, the cursor sits at the start of the
// line immediately below the last rendered line, and exactly `drawn` lines of
// content are on screen.
type interactiveRenderer struct {
	out      io.Writer
	size     func() (width, height int)
	color    bool
	collapse bool

	frame        int
	drawn        int
	cursorHidden bool
}

func newInteractive(opts Options) *interactiveRenderer {
	return &interactiveRenderer{
		out:      opts.Out,
		size:     opts.Size,
		color:    !opts.NoColor,
		collapse: opts.Collapse,
	}
}

func (r *interactiveRenderer) width() int {
	if r.size == nil {
		return FallbackWidth
	}
	w, _ := r.size()
	if w < 10 {
		return FallbackWidth
	}
	return w
}

func (r *interactiveRenderer) Start() {
	r.cursorHidden = true
	_, _ = io.WriteString(r.out, "\x1b[?25l")
}

func (r *interactiveRenderer) Update(snaps []task.Snapshot) {
	r.frame++
	r.redraw(renderTree(snaps, r.frame, r.width(), r.color, r.collapse))
}

func (r *interactiveRenderer) Stop(snaps []task.Snapshot) {
	// The final frame is rendered exactly once here, with no spinner animation.
	r.redraw(renderTree(snaps, r.frame, r.width(), r.color, r.collapse))
	if r.cursorHidden {
		_, _ = io.WriteString(r.out, "\x1b[?25h")
		r.cursorHidden = false
	}
	r.drawn = 0
}

// redraw overwrites the previously drawn lines with the new frame, leaving the
// cursor at the start of the line immediately after the last rendered line.
func (r *interactiveRenderer) redraw(lines []string) {
	var b bytes.Buffer

	if r.drawn > 0 {
		fmt.Fprintf(&b, "\x1b[%dA", r.drawn)
	}

	for _, ln := range lines {
		b.WriteString("\x1b[2K")
		b.WriteString(ln)
		b.WriteByte('\n')
	}

	// Clear any lines left over from a previous, taller frame, then move the
	// cursor back up so it sits just below the new frame (restoring the
	// invariant above).
	leftover := r.drawn - len(lines)
	for i := 0; i < leftover; i++ {
		b.WriteString("\x1b[2K\n")
	}
	if leftover > 0 {
		fmt.Fprintf(&b, "\x1b[%dA", leftover)
	}

	_, _ = r.out.Write(b.Bytes())
	r.drawn = len(lines)
}
