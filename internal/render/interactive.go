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
type interactiveRenderer struct {
	out   io.Writer
	size  func() (width, height int)
	color bool

	frame        int
	drawn        int
	cursorHidden bool
}

func newInteractive(opts Options) *interactiveRenderer {
	return &interactiveRenderer{
		out:   opts.Out,
		size:  opts.Size,
		color: !opts.NoColor,
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
	r.redraw(renderTree(snaps, r.frame, r.width(), r.color))
}

func (r *interactiveRenderer) Stop(snaps []task.Snapshot) {
	r.redraw(renderTree(snaps, r.frame, r.width(), r.color))
	if r.cursorHidden {
		_, _ = io.WriteString(r.out, "\x1b[?25h")
		r.cursorHidden = false
	}
	r.drawn = 0
}

// redraw overwrites the previously drawn lines with the new frame, leaving the
// cursor on the line immediately after the last drawn line.
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

	leftover := r.drawn - len(lines)
	for i := 0; i < leftover; i++ {
		b.WriteString("\x1b[2K\n")
	}

	_, _ = r.out.Write(b.Bytes())
	r.drawn = len(lines)
}
