package task

import (
	"testing"

	"github.com/tomtomsen/tasks/internal/config"
)

func leafDef(name string) *config.TaskDef {
	return &config.TaskDef{Name: name, Command: "echo " + name, Path: name}
}

func groupDef(name string, children ...*config.TaskDef) *config.TaskDef {
	return &config.TaskDef{Name: name, IsGroup: true, Children: children, Path: name}
}

func TestAggregateStates(t *testing.T) {
	model := NewModel([]*config.TaskDef{
		groupDef("root",
			leafDef("a"),
			leafDef("b"),
			leafDef("c"),
		),
	})
	root := model.Roots[0]
	a, b, c := root.Children()[0], root.Children()[1], root.Children()[2]

	if got := root.State(); got != StatePending {
		t.Fatalf("initial group state = %v, want pending", got)
	}

	model.MarkRunning(a)
	if got := root.State(); got != StateRunning {
		t.Fatalf("group with running child = %v, want running", got)
	}

	model.MarkFinished(a, Result{ExitCode: 0})
	model.MarkFinished(b, Result{ExitCode: 1})
	model.MarkFinished(c, Result{ExitCode: 0})
	if got := root.State(); got != StateFailed {
		t.Fatalf("group with failed child = %v, want failed", got)
	}

	// Mark a failing child cancelled and confirm a pure-success group succeeds.
	model2 := NewModel([]*config.TaskDef{
		groupDef("root2", leafDef("x"), leafDef("y")),
	})
	for _, n := range model2.Leaves() {
		model2.MarkRunning(n)
		model2.MarkFinished(n, Result{ExitCode: 0})
	}
	if got := model2.Roots[0].State(); got != StateSucceeded {
		t.Fatalf("all-success group = %v, want succeeded", got)
	}
}

func TestSkippedAndCancelled(t *testing.T) {
	model := NewModel([]*config.TaskDef{
		groupDef("root", leafDef("a"), leafDef("b")),
	})
	a, b := model.Roots[0].Children()[0], model.Roots[0].Children()[1]

	model.MarkSkipped(a)
	if a.State() != StateSkipped {
		t.Fatalf("a = %v, want skipped", a.State())
	}
	model.MarkCancelled(b)
	if b.State() != StateCancelled {
		t.Fatalf("b = %v, want cancelled", b.State())
	}
	// Cancelled has higher aggregate priority than skipped.
	if got := model.Roots[0].State(); got != StateCancelled {
		t.Fatalf("group of skipped+cancelled = %v, want cancelled (priority)", got)
	}
}

func TestResultAndSnapshot(t *testing.T) {
	model := NewModel([]*config.TaskDef{
		groupDef("root", leafDef("a")),
	})
	a := model.Leaves()[0]
	model.MarkRunning(a)
	model.MarkFinished(a, Result{ExitCode: 7, Stdout: []byte("out"), Stderr: []byte("err")})

	r := a.Result()
	if r.ExitCode != 7 || string(r.Stdout) != "out" || string(r.Stderr) != "err" {
		t.Fatalf("result = %+v", r)
	}

	snap := model.Snapshot()
	if len(snap) != 1 || snap[0].Name != "root" || len(snap[0].Children) != 1 {
		t.Fatalf("snapshot = %+v", snap)
	}
	child := snap[0].Children[0]
	if child.State != StateFailed || child.Path != "a" {
		t.Fatalf("child snapshot = %+v", child)
	}
}
