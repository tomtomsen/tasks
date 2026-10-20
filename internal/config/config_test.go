package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestValidNested(t *testing.T) {
	path := writeConfig(t, `
version: 1
tasks:
  - name: Root
    tasks:
      - name: A
        command: echo a
      - name: B
        tasks:
          - name: B1
            command: echo b1
`)
	res, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tasks) != 1 {
		t.Fatalf("want 1 root, got %d", len(res.Tasks))
	}
	root := res.Tasks[0]
	if !root.IsGroup || root.Name != "Root" {
		t.Fatalf("root: %+v", root)
	}
	if len(root.Children) != 2 {
		t.Fatalf("want 2 children, got %d", len(root.Children))
	}
	if root.Children[0].IsGroup || root.Children[0].Command != "echo a" {
		t.Fatalf("A: %+v", root.Children[0])
	}
	if !root.Children[1].IsGroup || len(root.Children[1].Children) != 1 {
		t.Fatalf("B: %+v", root.Children[1])
	}
}

func TestInheritedWorkingDir(t *testing.T) {
	path := writeConfig(t, `
version: 1
tasks:
  - name: Root
    working_dir: sub
    tasks:
      - name: A
        command: echo a
      - name: B
        working_dir: other
        tasks:
          - name: B1
            command: echo b1
`)
	res, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Dir(path)
	root := res.Tasks[0]
	if want := filepath.Join(base, "sub"); root.WorkingDir != want {
		t.Fatalf("root wd = %q, want %q", root.WorkingDir, want)
	}
	if want := filepath.Join(base, "sub"); root.Children[0].WorkingDir != want {
		t.Fatalf("A wd = %q, want %q", root.Children[0].WorkingDir, want)
	}
	if want := filepath.Join(base, "other"); root.Children[1].WorkingDir != want {
		t.Fatalf("B wd = %q, want %q", root.Children[1].WorkingDir, want)
	}
	if want := filepath.Join(base, "other"); root.Children[1].Children[0].WorkingDir != want {
		t.Fatalf("B1 wd = %q, want %q", root.Children[1].Children[0].WorkingDir, want)
	}
}

func TestEnvInheritance(t *testing.T) {
	path := writeConfig(t, `
version: 1
tasks:
  - name: Root
    env:
      A: "1"
      B: "root"
    tasks:
      - name: Child
        env:
          B: "child"
          C: "2"
        command: echo hi
`)
	res, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	env := res.Tasks[0].Children[0].Env
	if env["A"] != "1" {
		t.Fatalf("A not inherited: %v", env)
	}
	if env["B"] != "child" {
		t.Fatalf("B not overridden: %v", env)
	}
	if env["C"] != "2" {
		t.Fatalf("C missing: %v", env)
	}
}

func TestInvalidVersion(t *testing.T) {
	path := writeConfig(t, "version: 2\ntasks:\n  - name: A\n    command: echo a\n")
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("want version error, got %v", err)
	}
}

func TestMissingCommand(t *testing.T) {
	path := writeConfig(t, "version: 1\ntasks:\n  - name: A\n")
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("want empty error, got %v", err)
	}
}

func TestEmptyGroup(t *testing.T) {
	path := writeConfig(t, "version: 1\ntasks:\n  - name: A\n    tasks: []\n")
	if _, err := Load(path); err == nil {
		t.Fatalf("want error for empty group")
	}
}

func TestAmbiguous(t *testing.T) {
	path := writeConfig(t, "version: 1\ntasks:\n  - name: A\n    command: echo a\n    tasks:\n      - name: B\n        command: echo b\n")
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "both") {
		t.Fatalf("want ambiguous error, got %v", err)
	}
}

func TestInvalidConcurrency(t *testing.T) {
	for _, body := range []string{
		"version: 1\nsettings:\n  concurrency: 0\ntasks:\n  - name: A\n    command: echo a\n",
		"version: 1\nsettings:\n  concurrency: -1\ntasks:\n  - name: A\n    command: echo a\n",
	} {
		path := writeConfig(t, body)
		if _, err := Load(path); err == nil {
			t.Fatalf("want concurrency error for config: %s", body)
		}
	}
}

func TestInvalidTimeout(t *testing.T) {
	path := writeConfig(t, "version: 1\ntasks:\n  - name: A\n    command: echo a\n    timeout: notaduration\n")
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("want timeout error, got %v", err)
	}
}

func TestDefaultConcurrency(t *testing.T) {
	path := writeConfig(t, "version: 1\ntasks:\n  - name: A\n    command: echo a\n")
	res, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if res.Concurrency < 1 {
		t.Fatalf("default concurrency = %d", res.Concurrency)
	}
	if !res.ContinueOnError {
		t.Fatalf("continue_on_error should default to true")
	}
}

func TestCollapseDefaultsToFalse(t *testing.T) {
	path := writeConfig(t, "version: 1\ntasks:\n  - name: A\n    command: echo a\n")
	res, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if res.Collapse {
		t.Fatalf("collapse should default to false")
	}
}

func TestCollapseEnabled(t *testing.T) {
	path := writeConfig(t, "version: 1\nsettings:\n  renderer:\n    collapse: true\ntasks:\n  - name: A\n    command: echo a\n")
	res, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Collapse {
		t.Fatalf("collapse should be true")
	}
}
