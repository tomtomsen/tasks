package report

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tomtomsen/tasks/internal/config"
	"github.com/tomtomsen/tasks/internal/task"
)

func buildModel() *task.Model {
	model := task.NewModel([]*config.TaskDef{
		{
			Name: "root", IsGroup: true, Path: "root",
			Children: []*config.TaskDef{
				{Name: "ok", Command: "ok", Path: "root > ok"},
				{Name: "bad", Command: "bad", Path: "root > bad"},
			},
		},
	})
	ok, bad := model.Leaves()[0], model.Leaves()[1]
	model.MarkRunning(ok)
	model.MarkFinished(ok, task.Result{ExitCode: 0, Stdout: []byte("fine")})
	model.MarkRunning(bad)
	model.MarkFinished(bad, task.Result{ExitCode: 1, Stdout: []byte("out"), Stderr: []byte("boom")})
	return model
}

func TestBuildReport(t *testing.T) {
	rep := Build(buildModel())
	if rep.Total != 2 || rep.Passed != 1 || rep.Failed != 1 {
		t.Fatalf("report = %+v", rep)
	}
	if rep.ExitCode() != 1 {
		t.Fatalf("exit code = %d, want 1", rep.ExitCode())
	}
	if len(rep.Failures) != 1 || rep.Failures[0].Path != "root > bad" {
		t.Fatalf("failures = %+v", rep.Failures)
	}
}

func TestWriteFailures(t *testing.T) {
	var buf bytes.Buffer
	rep := Build(buildModel())
	WriteFailures(&buf, rep)
	out := buf.String()
	for _, want := range []string{"FAILURES (1)", "root > bad", "Command: bad", "Exit code: 1", "boom"} {
		if !strings.Contains(out, want) {
			t.Fatalf("failure report missing %q: %s", want, out)
		}
	}
	// stdout and stderr are reported separately.
	if !strings.Contains(out, "Output (stderr)") {
		t.Fatalf("missing stderr section: %s", out)
	}
}

func TestWriteSummaryAllPassed(t *testing.T) {
	model := task.NewModel([]*config.TaskDef{
		{Name: "a", Command: "a", Path: "a"},
		{Name: "b", Command: "b", Path: "b"},
	})
	for _, n := range model.Leaves() {
		model.MarkRunning(n)
		model.MarkFinished(n, task.Result{ExitCode: 0})
	}
	var buf bytes.Buffer
	WriteSummary(&buf, Build(model))
	if !strings.Contains(buf.String(), "All 2 checks passed.") {
		t.Fatalf("summary = %q", buf.String())
	}
}

func TestWriteSummaryMixed(t *testing.T) {
	var buf bytes.Buffer
	WriteSummary(&buf, Build(buildModel()))
	if !strings.Contains(buf.String(), "1 checks passed, 1 failed.") {
		t.Fatalf("summary = %q", buf.String())
	}
}

// Failure reports must carry the full task path even when the interactive
// renderer collapses ancestors, because the report is built directly from the
// task tree, not from the rendered view.
func TestFailureReportsFullPath(t *testing.T) {
	model := task.NewModel([]*config.TaskDef{
		{
			Name: "root", IsGroup: true, Path: "root",
			Children: []*config.TaskDef{
				{
					Name: "sub", IsGroup: true, Path: "root > sub",
					Children: []*config.TaskDef{
						{Name: "leaf", Command: "cmd", Path: "root > sub > leaf"},
					},
				},
			},
		},
	})
	n := model.Leaves()[0]
	model.MarkRunning(n)
	model.MarkFinished(n, task.Result{ExitCode: 1})

	rep := Build(model)
	if len(rep.Failures) != 1 {
		t.Fatalf("failures = %+v", rep.Failures)
	}
	if rep.Failures[0].Path != "root > sub > leaf" {
		t.Fatalf("path = %q, want full path %q", rep.Failures[0].Path, "root > sub > leaf")
	}
}
