// Package runner orchestrates task execution: it schedules leaf commands
// against a concurrency limit, applies results to the task state model, and
// drives the renderer. It never writes to the terminal directly; all process
// output is captured and reported after execution finishes.
package runner

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/tomtomsen/tasks/internal/process"
	"github.com/tomtomsen/tasks/internal/task"
)

// Executor runs a single command. It exists as an interface so the scheduler
// can be tested with fake executors that never spawn real processes.
type Executor interface {
	Run(ctx context.Context, spec process.Spec) process.Result
}

// Scheduler runs all leaf commands respecting the concurrency limit.
type Scheduler struct {
	model           *task.Model
	exec            Executor
	concurrency     int
	continueOnError bool
	ctx             context.Context
}

// NewScheduler creates a scheduler for the given model.
func NewScheduler(model *task.Model, exec Executor, concurrency int, continueOnError bool, ctx context.Context) *Scheduler {
	return &Scheduler{
		model:           model,
		exec:            exec,
		concurrency:     concurrency,
		continueOnError: continueOnError,
		ctx:             ctx,
	}
}

// Run schedules all leaf commands and blocks until they finish, are skipped,
// or are cancelled.
func (s *Scheduler) Run() {
	leaves := s.model.Leaves()
	if len(leaves) == 0 {
		return
	}

	jobs := make(chan *task.Node, s.concurrency)
	var failed atomic.Bool

	var wg sync.WaitGroup
	for i := 0; i < s.concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := range jobs {
				if s.ctx.Err() != nil {
					s.model.MarkCancelled(n)
					continue
				}
				if !s.continueOnError && failed.Load() {
					s.model.MarkSkipped(n)
					continue
				}
				s.runLeaf(n, &failed)
			}
		}()
	}

	for _, n := range leaves {
		if s.ctx.Err() != nil {
			s.model.MarkCancelled(n)
			continue
		}
		if !s.continueOnError && failed.Load() {
			s.model.MarkSkipped(n)
			continue
		}
		jobs <- n
	}
	close(jobs)
	wg.Wait()
}

func (s *Scheduler) runLeaf(n *task.Node, failed *atomic.Bool) {
	s.model.MarkRunning(n)

	def := n.Def
	spec := process.Spec{
		Command:    def.Command,
		WorkingDir: def.WorkingDir,
		Env:        process.BuildEnv(def.Env),
		Timeout:    def.Timeout,
	}

	res := s.exec.Run(s.ctx, spec)

	if res.Cancelled {
		s.model.MarkCancelled(n)
		return
	}

	s.model.MarkFinished(n, task.Result{
		ExitCode: res.ExitCode,
		Duration: res.Duration,
		Stdout:   res.Stdout,
		Stderr:   res.Stderr,
		TimedOut: res.TimedOut,
		StartErr: res.StartErr,
	})

	if res.Failed() {
		failed.Store(true)
	}
}
