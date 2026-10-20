// Command tasks runs configurable shell commands concurrently with a live,
// hierarchical terminal interface.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"golang.org/x/term"

	"github.com/tomtomsen/tasks/internal/config"
	"github.com/tomtomsen/tasks/internal/runner"
)

// version is overridable at build time via -ldflags "-X main.version=...".
var version = "0.1.0"

const usageText = `tasks — run configurable shell commands concurrently

Usage:
  tasks run [flags]
  tasks --version
  tasks --help

Run flags:
  --config=FILE       configuration file (default "tasks.yaml")
  --concurrency=N     override settings.concurrency
  --renderer=MODE     "interactive" or "plain" (default: auto by TTY)
  --collapse          collapse successfully completed task groups
  --no-collapse       keep all tasks visible (default)
  --verbose           include successful task output in the report
  --log-file=FILE     write a full per-task log to FILE
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usageText)
		return 2
	}

	switch args[0] {
	case "--version", "-version", "version":
		fmt.Fprintf(stdout, "tasks %s\n", version)
		return 0
	case "--help", "-h", "help":
		fmt.Fprint(stdout, usageText)
		return 0
	case "run":
		return runCommand(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], usageText)
		return 2
	}
}

func runCommand(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "tasks.yaml", "configuration file")
	concurrency := fs.Int("concurrency", 0, "override settings.concurrency")
	renderer := fs.String("renderer", "", "interactive or plain")
	collapseOn := fs.Bool("collapse", false, "collapse successfully completed task groups")
	collapseOff := fs.Bool("no-collapse", false, "keep all tasks visible")
	verbose := fs.Bool("verbose", false, "include successful task output")
	logFile := fs.String("log-file", "", "write a full per-task log")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "unexpected arguments: %v\n", fs.Args())
		return 2
	}

	res, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "config error: %v\n", err)
		return 2
	}

	if *concurrency > 0 {
		res.Concurrency = *concurrency
	}

	collapse, err := resolveCollapse(res.Collapse, *collapseOn, *collapseOff)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}

	stdoutTTY := isTerminal(stdout)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		interrupted bool
		terminated  bool
		mu          sync.Mutex
	)

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case sig := <-sigCh:
				cancel()
				mu.Lock()
				switch sig {
				case syscall.SIGINT:
					if interrupted {
						mu.Unlock()
						restoreCursor(stdoutTTY, stdout)
						os.Exit(130)
					}
					interrupted = true
				case syscall.SIGTERM:
					if terminated {
						mu.Unlock()
						os.Exit(143)
					}
					terminated = true
				}
				mu.Unlock()
			case <-done:
				return
			}
		}
	}()
	defer func() {
		signal.Stop(sigCh)
		close(done)
	}()

	opts := runner.Options{
		Concurrency:     res.Concurrency,
		ContinueOnError: res.ContinueOnError,
		Renderer:        *renderer,
		Verbose:         *verbose,
		LogFile:         *logFile,
		Collapse:        collapse,
		Size: func() (int, int) {
			return termSize(stdout)
		},
		NoColor: os.Getenv("NO_COLOR") != "",
	}

	code, err := runner.Run(ctx, res, opts, stdout, stderr, stdoutTTY)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}

	mu.Lock()
	wasInterrupted := interrupted
	wasTerminated := terminated
	mu.Unlock()

	switch {
	case wasInterrupted:
		return 130
	case wasTerminated:
		return 143
	default:
		return code
	}
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

func termSize(w io.Writer) (int, int) {
	f, ok := w.(*os.File)
	if !ok {
		return 80, 24
	}
	width, height, err := term.GetSize(int(f.Fd()))
	if err != nil {
		return 80, 24
	}
	return width, height
}

func restoreCursor(tty bool, w io.Writer) {
	if tty {
		_, _ = io.WriteString(w, "\x1b[?25h")
	}
}

// resolveCollapse merges the collapse CLI flags with the configuration value.
// The CLI overrides the configuration in either direction; passing both flags
// is an error.
func resolveCollapse(configValue, collapseOn, collapseOff bool) (bool, error) {
	switch {
	case collapseOn && collapseOff:
		return false, fmt.Errorf("--collapse and --no-collapse cannot both be set")
	case collapseOn:
		return true, nil
	case collapseOff:
		return false, nil
	default:
		return configValue, nil
	}
}
