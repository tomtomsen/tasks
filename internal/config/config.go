// Package config loads, validates, and resolves the YAML task configuration.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Supported configuration versions.
const supportedVersion = 1

// DefaultConcurrency is used when settings.concurrency is omitted.
var DefaultConcurrency = runtime.NumCPU()

// Config is the raw on-disk shape of tasks.yaml.
type Config struct {
	Version  int      `yaml:"version"`
	Settings Settings `yaml:"settings"`
	Tasks    []Task   `yaml:"tasks"`
}

// Settings holds global runner options.
type Settings struct {
	// Concurrency is the maximum number of simultaneously running commands.
	// A nil/0 value means "use the default".
	Concurrency *int `yaml:"concurrency"`
	// ContinueOnError controls whether independent tasks keep running after
	// a failure. nil means true (the default).
	ContinueOnError *bool `yaml:"continue_on_error"`
}

// Task is a raw task node as it appears in the YAML file.
type Task struct {
	Name        string            `yaml:"name"`
	Description string            `yaml:"description"`
	Command     string            `yaml:"command"`
	WorkingDir  string            `yaml:"working_dir"`
	Timeout     string            `yaml:"timeout"`
	Env         map[string]string `yaml:"env"`
	Tasks       []Task            `yaml:"tasks"`
}

// TaskDef is a fully resolved task definition. Working directories are
// absolute, environment variables are fully inherited from ancestors, and
// timeouts are parsed durations.
type TaskDef struct {
	Name        string
	Description string
	Command     string
	WorkingDir  string
	Timeout     time.Duration
	Env         map[string]string
	Children    []*TaskDef
	Path        string
	IsGroup     bool
}

// Resolved is the result of loading and resolving a configuration file.
type Resolved struct {
	Concurrency     int
	ContinueOnError bool
	Tasks           []*TaskDef
}

// Load reads, validates, and resolves the configuration file at path.
func Load(path string) (*Resolved, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	if cfg.Version != supportedVersion {
		return nil, fmt.Errorf("unsupported config version %d (expected %d)", cfg.Version, supportedVersion)
	}

	baseDir, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("resolve config directory: %w", err)
	}

	resolved := &Resolved{
		Concurrency:     DefaultConcurrency,
		ContinueOnError: true,
	}

	if cfg.Settings.Concurrency != nil {
		if *cfg.Settings.Concurrency < 1 {
			return nil, fmt.Errorf("settings.concurrency must be at least 1, got %d", *cfg.Settings.Concurrency)
		}
		resolved.Concurrency = *cfg.Settings.Concurrency
	}

	if cfg.Settings.ContinueOnError != nil {
		resolved.ContinueOnError = *cfg.Settings.ContinueOnError
	}

	if len(cfg.Tasks) == 0 {
		return nil, fmt.Errorf("config contains no tasks")
	}

	resolved.Tasks = make([]*TaskDef, 0, len(cfg.Tasks))
	for i := range cfg.Tasks {
		def, err := resolveTask(cfg.Tasks[i], baseDir, baseDir, nil, "")
		if err != nil {
			return nil, err
		}
		resolved.Tasks = append(resolved.Tasks, def)
	}

	return resolved, nil
}

// resolveTask recursively validates a raw task and produces a resolved TaskDef.
// baseDir is the configuration file directory (used to resolve relative
// working directories); inheritedDir is the working directory inherited from
// the nearest ancestor; inheritedEnv is the environment inherited from
// ancestors; path is the accumulated "A > B > C" path used in error messages.
func resolveTask(t Task, baseDir, inheritedDir string, inheritedEnv map[string]string, path string) (*TaskDef, error) {
	name := strings.TrimSpace(t.Name)
	if name == "" {
		return nil, fmt.Errorf("task %q has no name", pathFor(path, t.Name))
	}

	if path == "" {
		path = name
	} else {
		path = path + " > " + name
	}

	hasCommand := strings.TrimSpace(t.Command) != ""
	hasChildren := len(t.Tasks) > 0

	if hasCommand && hasChildren {
		return nil, fmt.Errorf("task %q has both a command and child tasks; a task must be one or the other", path)
	}
	if !hasCommand && !hasChildren {
		return nil, fmt.Errorf("task %q is empty: it needs a command or child tasks", path)
	}

	def := &TaskDef{
		Name:        name,
		Description: strings.TrimSpace(t.Description),
		Path:        path,
	}

	// Resolve and inherit the working directory.
	wd := inheritedDir
	if strings.TrimSpace(t.WorkingDir) != "" {
		p := t.WorkingDir
		if !filepath.IsAbs(p) {
			p = filepath.Join(baseDir, p)
		}
		wd = filepath.Clean(p)
	}
	def.WorkingDir = wd

	// Merge and inherit environment variables.
	env := make(map[string]string, len(inheritedEnv)+len(t.Env))
	for k, v := range inheritedEnv {
		env[k] = v
	}
	for k, v := range t.Env {
		env[k] = v
	}
	def.Env = env

	if t.Timeout != "" {
		d, err := time.ParseDuration(t.Timeout)
		if err != nil {
			return nil, fmt.Errorf("task %q has invalid timeout %q: %v", path, t.Timeout, err)
		}
		if d <= 0 {
			return nil, fmt.Errorf("task %q has non-positive timeout %q", path, t.Timeout)
		}
		def.Timeout = d
	}

	if hasCommand {
		def.Command = strings.TrimSpace(t.Command)
		def.IsGroup = false
		return def, nil
	}

	def.IsGroup = true
	def.Children = make([]*TaskDef, 0, len(t.Tasks))
	for i := range t.Tasks {
		child, err := resolveTask(t.Tasks[i], baseDir, wd, env, path)
		if err != nil {
			return nil, err
		}
		def.Children = append(def.Children, child)
	}
	return def, nil
}

// pathFor returns a best-effort task path for error messages when the task has
// not yet been fully resolved (for example, when it is missing a name).
func pathFor(parent, name string) string {
	if name != "" {
		if parent == "" {
			return name
		}
		return parent + " > " + name
	}
	if parent == "" {
		return "<unnamed>"
	}
	return parent
}
