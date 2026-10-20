package runner

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/tomtomsen/tasks/internal/config"
	"github.com/tomtomsen/tasks/internal/process"
	"github.com/tomtomsen/tasks/internal/render"
	"github.com/tomtomsen/tasks/internal/report"
	"github.com/tomtomsen/tasks/internal/task"
)

// Options controls a run, overriding or extending the configuration.
type Options struct {
	Concurrency     int
	ContinueOnError bool
	Renderer        string
	Verbose         bool
	LogFile         string
	Collapse        bool
	Size            func() (width, height int)
	NoColor         bool
}

// Run executes the resolved configuration and reports the result. It returns
// the process exit code (0 or 1) and a usage error, if any.
func Run(ctx context.Context, res *config.Resolved, opts Options, stdout, stderr io.Writer, stdoutTTY bool) (int, error) {
	mode, err := render.SelectMode(opts.Renderer, stdoutTTY)
	if err != nil {
		return 2, err
	}

	model := task.NewModel(res.Tasks)
	exec := process.NewExecutor()
	sched := NewScheduler(model, exec, opts.Concurrency, opts.ContinueOnError, ctx)

	r := render.New(render.Options{
		Mode:     mode,
		Out:      stdout,
		Size:     opts.Size,
		NoColor:  opts.NoColor,
		Collapse: opts.Collapse,
	})

	r.Start()

	done := make(chan struct{})
	go func() {
		sched.Run()
		close(done)
	}()

	var ticker *time.Ticker
	var tickC <-chan time.Time
	if mode == render.ModeInteractive {
		ticker = time.NewTicker(render.RefreshInterval)
		tickC = ticker.C
	}

loop:
	for {
		select {
		case <-model.Changes():
			r.Update(model.Snapshot())
		case <-tickC:
			r.Update(model.Snapshot())
		case <-done:
			break loop
		}
	}
	if ticker != nil {
		ticker.Stop()
	}

	r.Stop(model.Snapshot())

	rep := report.Build(model)
	report.WriteFailures(stdout, rep)
	if opts.Verbose {
		report.WriteVerbose(stdout, model)
	}
	report.WriteSummary(stdout, rep)

	if opts.LogFile != "" {
		if err := report.WriteLogFile(opts.LogFile, model); err != nil {
			fmt.Fprintf(stderr, "warning: could not write log file: %v\n", err)
		}
	}

	return rep.ExitCode(), nil
}
