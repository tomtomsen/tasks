package runner

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tomtomsen/tasks/internal/config"
	"github.com/tomtomsen/tasks/internal/process"
	"github.com/tomtomsen/tasks/internal/task"
)

// fakeExecutor runs a user-provided function and tracks concurrency.
type fakeExecutor struct {
	fn        func(ctx context.Context, spec process.Spec) process.Result
	active    atomic.Int32
	mu        sync.Mutex
	maxActive int32
	calls     []process.Spec
}

func (f *fakeExecutor) Run(ctx context.Context, spec process.Spec) process.Result {
	cur := f.active.Add(1)
	f.mu.Lock()
	if cur > f.maxActive {
		f.maxActive = cur
	}
	f.calls = append(f.calls, spec)
	f.mu.Unlock()
	defer f.active.Add(-1)
	return f.fn(ctx, spec)
}

func (f *fakeExecutor) maxConcurrency() int32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxActive
}

func makeLeaves(n int) []*config.TaskDef {
	defs := make([]*config.TaskDef, n)
	for i := range defs {
		name := string(rune('a' + i))
		defs[i] = &config.TaskDef{Name: name, Command: "cmd " + name, Path: name}
	}
	return defs
}

func TestConcurrencyLimit(t *testing.T) {
	defs := makeLeaves(10)
	model := task.NewModel(defs)
	exec := &fakeExecutor{fn: func(ctx context.Context, spec process.Spec) process.Result {
		time.Sleep(30 * time.Millisecond)
		return process.Result{ExitCode: 0}
	}}

	sched := NewScheduler(model, exec, 3, true, context.Background())
	sched.Run()

	if got := exec.maxConcurrency(); got > 3 {
		t.Fatalf("max concurrency = %d, want <= 3", got)
	}
	if got := exec.maxConcurrency(); got < 2 {
		t.Fatalf("max concurrency = %d, want >= 2 (tasks should overlap)", got)
	}
	for _, n := range model.Leaves() {
		if n.State() != task.StateSucceeded {
			t.Fatalf("leaf %s = %v, want succeeded", n.Def.Name, n.State())
		}
	}
}

func TestAllTasksTerminal(t *testing.T) {
	model := task.NewModel(makeLeaves(5))
	exec := &fakeExecutor{fn: func(ctx context.Context, spec process.Spec) process.Result {
		return process.Result{ExitCode: 0}
	}}
	NewScheduler(model, exec, 2, true, context.Background()).Run()
	for _, n := range model.Leaves() {
		if !n.State().Terminal() {
			t.Fatalf("leaf %s not terminal: %v", n.Def.Name, n.State())
		}
	}
}

func TestFailedDoesNotBlockIndependent(t *testing.T) {
	defs := []*config.TaskDef{
		{Name: "fail", Command: "fail", Path: "fail"},
		{Name: "ok", Command: "ok", Path: "ok"},
	}
	model := task.NewModel(defs)
	exec := &fakeExecutor{fn: func(ctx context.Context, spec process.Spec) process.Result {
		if spec.Command == "fail" {
			return process.Result{ExitCode: 1}
		}
		return process.Result{ExitCode: 0}
	}}
	NewScheduler(model, exec, 1, true, context.Background()).Run()

	if model.Leaves()[0].State() != task.StateFailed {
		t.Fatalf("fail leaf = %v", model.Leaves()[0].State())
	}
	if model.Leaves()[1].State() != task.StateSucceeded {
		t.Fatalf("ok leaf = %v, want succeeded (independent)", model.Leaves()[1].State())
	}
}

func TestFailFastSkipsRemaining(t *testing.T) {
	defs := makeLeaves(4)
	model := task.NewModel(defs)
	var started atomic.Int32
	exec := &fakeExecutor{fn: func(ctx context.Context, spec process.Spec) process.Result {
		if spec.Command == "cmd a" {
			started.Add(1)
			return process.Result{ExitCode: 1}
		}
		started.Add(1)
		time.Sleep(50 * time.Millisecond)
		return process.Result{ExitCode: 0}
	}}
	NewScheduler(model, exec, 2, false, context.Background()).Run()

	if model.Leaves()[0].State() != task.StateFailed {
		t.Fatalf("first leaf = %v, want failed", model.Leaves()[0].State())
	}
	// With concurrency 2, leaf b was already running when a failed; the rest
	// (c, d) must be skipped.
	for i := 2; i < 4; i++ {
		if model.Leaves()[i].State() != task.StateSkipped {
			t.Fatalf("leaf %d = %v, want skipped", i, model.Leaves()[i].State())
		}
	}
}

func TestParentAggregation(t *testing.T) {
	defs := []*config.TaskDef{
		{
			Name: "root", IsGroup: true, Path: "root",
			Children: []*config.TaskDef{
				{Name: "good", Command: "good", Path: "root > good"},
				{Name: "bad", Command: "bad", Path: "root > bad"},
			},
		},
	}
	model := task.NewModel(defs)
	exec := &fakeExecutor{fn: func(ctx context.Context, spec process.Spec) process.Result {
		if spec.Command == "bad" {
			return process.Result{ExitCode: 1}
		}
		return process.Result{ExitCode: 0}
	}}
	NewScheduler(model, exec, 2, true, context.Background()).Run()

	if got := model.Roots[0].State(); got != task.StateFailed {
		t.Fatalf("root = %v, want failed", got)
	}
}

func TestCancellation(t *testing.T) {
	model := task.NewModel(makeLeaves(4))
	ctx, cancel := context.WithCancel(context.Background())
	var started atomic.Int32
	exec := &fakeExecutor{fn: func(ctx context.Context, spec process.Spec) process.Result {
		started.Add(1)
		<-ctx.Done()
		return process.Result{Cancelled: true}
	}}

	sched := NewScheduler(model, exec, 2, true, ctx)
	done := make(chan struct{})
	go func() { sched.Run(); close(done) }()

	deadline := time.Now().Add(2 * time.Second)
	for started.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler did not finish after cancellation")
	}

	for _, n := range model.Leaves() {
		if n.State() != task.StateCancelled {
			t.Fatalf("leaf %s = %v, want cancelled", n.Def.Name, n.State())
		}
	}
}
