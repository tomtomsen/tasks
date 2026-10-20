package process

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSuccessful(t *testing.T) {
	r := NewExecutor().Run(context.Background(), Spec{Command: "exit 0"})
	if r.ExitCode != 0 || r.Failed() {
		t.Fatalf("result = %+v", r)
	}
}

func TestFailed(t *testing.T) {
	r := NewExecutor().Run(context.Background(), Spec{Command: "exit 7"})
	if r.ExitCode != 7 || !r.Failed() {
		t.Fatalf("result = %+v", r)
	}
}

func TestStdoutStderr(t *testing.T) {
	r := NewExecutor().Run(context.Background(), Spec{Command: "echo out; echo err >&2"})
	if string(r.Stdout) != "out\n" {
		t.Fatalf("stdout = %q", r.Stdout)
	}
	if string(r.Stderr) != "err\n" {
		t.Fatalf("stderr = %q", r.Stderr)
	}
}

func TestLargeOutput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r := NewExecutor().Run(ctx, Spec{Command: "yes | head -c 1048576"})
	if len(r.Stdout) != 1048576 {
		t.Fatalf("stdout length = %d, want 1048576", len(r.Stdout))
	}
}

func TestStartFailure(t *testing.T) {
	r := NewExecutor().Run(context.Background(), Spec{
		Command:    "echo hi",
		WorkingDir: filepath.Join(t.TempDir(), "does-not-exist"),
	})
	if r.StartErr == "" || !r.Failed() {
		t.Fatalf("result = %+v, want start error", r)
	}
}

func TestTimeout(t *testing.T) {
	start := time.Now()
	r := NewExecutor().Run(context.Background(), Spec{
		Command: "sleep 5",
		Timeout: 100 * time.Millisecond,
	})
	if !r.TimedOut {
		t.Fatalf("result = %+v, want timeout", r)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("timeout took too long: %v", time.Since(start))
	}
}

func TestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	r := NewExecutor().Run(ctx, Spec{Command: "sleep 5"})
	if !r.Cancelled {
		t.Fatalf("result = %+v, want cancelled", r)
	}
}

func TestCancellationKillsProcessGroup(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	childFile := filepath.Join(dir, "child")
	script := fmt.Sprintf("sleep 300 & child=$!; echo $$ > %s; echo $child > %s; wait", pidFile, childFile)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan Result, 1)
	go func() {
		done <- NewExecutor().Run(ctx, Spec{Command: script})
	}()

	waitForFile(t, pidFile, 2*time.Second)
	waitForFile(t, childFile, 2*time.Second)
	cancel()

	select {
	case r := <-done:
		if !r.Cancelled {
			t.Fatalf("result = %+v, want cancelled", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return after cancellation")
	}

	pid := readPid(t, pidFile)
	child := readPid(t, childFile)
	assertDead(t, pid)
	assertDead(t, child)
}

func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("file %s not created within %v", path, timeout)
}

func readPid(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &pid); err != nil {
		t.Fatalf("parse pid from %q: %v", data, err)
	}
	return pid
}

func assertDead(t *testing.T, pid int) {
	t.Helper()
	// The process may take a moment to be reaped by init; retry briefly.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		if err == syscall.ESRCH {
			return
		}
		if err == syscall.EPERM {
			t.Fatalf("pid %d still alive (EPERM)", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("pid %d still alive after cancellation", pid)
}
