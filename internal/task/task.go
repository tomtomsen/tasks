// Package task defines the runtime task tree and its state model.
//
// Task definitions (config.TaskDef) are kept separate from runtime state.
// The Node tree owns mutable state; leaf nodes transition through an explicit
// state machine while group nodes derive their state from their children.
package task

import (
	"sync"
	"time"

	"github.com/tomtomsen/tasks/internal/config"
)

// State is the lifecycle state of a task.
type State int

const (
	StatePending State = iota
	StateRunning
	StateSucceeded
	StateFailed
	StateSkipped
	StateCancelled
)

// String returns a stable, human-readable name for the state.
func (s State) String() string {
	switch s {
	case StatePending:
		return "pending"
	case StateRunning:
		return "running"
	case StateSucceeded:
		return "succeeded"
	case StateFailed:
		return "failed"
	case StateSkipped:
		return "skipped"
	case StateCancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

// Terminal reports whether the state is a final, non-running state.
func (s State) Terminal() bool {
	switch s {
	case StateSucceeded, StateFailed, StateSkipped, StateCancelled:
		return true
	default:
		return false
	}
}

// Result is the outcome of executing a leaf command.
type Result struct {
	ExitCode int
	Duration time.Duration
	Stdout   []byte
	Stderr   []byte
	TimedOut bool
	StartErr string
}

// Node is a single node in the runtime task tree.
type Node struct {
	Def      *config.TaskDef
	children []*Node
	notify   func()

	mu        sync.Mutex
	state     State
	exitCode  int
	duration  time.Duration
	stdout    []byte
	stderr    []byte
	timedOut  bool
	startErr  string
	startedAt time.Time
	endedAt   time.Time
}

// IsGroup reports whether the node is a group (has children) or a leaf command.
func (n *Node) IsGroup() bool { return n.Def.IsGroup }

// Children returns the node's children (nil for a leaf).
func (n *Node) Children() []*Node { return n.children }

// Snapshot is an immutable, lock-free view of a subtree suitable for rendering.
type Snapshot struct {
	Name        string
	Description string
	Path        string
	State       State
	Duration    time.Duration
	IsGroup     bool
	Children    []Snapshot
}

// Model is the thread-safe state model that owns the runtime task tree.
type Model struct {
	Roots  []*Node
	leaves []*Node

	change chan struct{}
}

// NewModel builds a runtime tree from resolved task definitions.
func NewModel(defs []*config.TaskDef) *Model {
	m := &Model{
		change: make(chan struct{}, 1),
	}
	m.Roots = make([]*Node, 0, len(defs))
	for _, d := range defs {
		m.Roots = append(m.Roots, m.buildNode(d))
	}
	return m
}

func (m *Model) buildNode(d *config.TaskDef) *Node {
	n := &Node{
		Def:    d,
		state:  StatePending,
		notify: m.signal,
	}
	if d.IsGroup {
		n.children = make([]*Node, 0, len(d.Children))
		for _, c := range d.Children {
			n.children = append(n.children, m.buildNode(c))
		}
	} else {
		m.leaves = append(m.leaves, n)
	}
	return n
}

// Leaves returns all leaf command nodes in deterministic configuration order.
func (m *Model) Leaves() []*Node { return m.leaves }

// Changes returns a channel that is signalled whenever the model state changes.
// Signals may coalesce; readers should always call Snapshot after receiving one.
func (m *Model) Changes() <-chan struct{} { return m.change }

func (m *Model) signal() {
	select {
	case m.change <- struct{}{}:
	default:
	}
}

// Snapshot returns the current state of the whole tree.
func (m *Model) Snapshot() []Snapshot {
	out := make([]Snapshot, 0, len(m.Roots))
	for _, r := range m.Roots {
		s, _, _ := r.snapshot()
		out = append(out, s)
	}
	return out
}

// MarkRunning transitions a leaf from pending to running.
func (m *Model) MarkRunning(n *Node) {
	n.mu.Lock()
	if n.state == StatePending {
		n.state = StateRunning
		n.startedAt = time.Now()
	}
	n.mu.Unlock()
	m.signal()
}

// MarkFinished records the outcome of a leaf command.
func (m *Model) MarkFinished(n *Node, r Result) {
	n.mu.Lock()
	n.endedAt = time.Now()
	if n.startedAt.IsZero() {
		n.startedAt = n.endedAt
	}
	n.exitCode = r.ExitCode
	n.duration = r.Duration
	n.stdout = r.Stdout
	n.stderr = r.Stderr
	n.timedOut = r.TimedOut
	n.startErr = r.StartErr

	switch {
	case r.TimedOut || r.StartErr != "" || r.ExitCode != 0:
		n.state = StateFailed
	default:
		n.state = StateSucceeded
	}
	n.mu.Unlock()
	m.signal()
}

// MarkSkipped transitions a pending leaf to skipped (fail-fast).
func (m *Model) MarkSkipped(n *Node) {
	n.mu.Lock()
	if n.state == StatePending {
		n.state = StateSkipped
	}
	n.mu.Unlock()
	m.signal()
}

// MarkCancelled transitions a pending or running leaf to cancelled (interrupt).
func (m *Model) MarkCancelled(n *Node) {
	n.mu.Lock()
	if n.state == StatePending || n.state == StateRunning {
		n.state = StateCancelled
	}
	n.mu.Unlock()
	m.signal()
}

// Result returns the recorded outcome of a leaf command.
func (n *Node) Result() Result {
	n.mu.Lock()
	defer n.mu.Unlock()
	return Result{
		ExitCode: n.exitCode,
		Duration: n.duration,
		Stdout:   n.stdout,
		Stderr:   n.stderr,
		TimedOut: n.timedOut,
		StartErr: n.startErr,
	}
}

// State returns the node's current state, deriving group states from children.
func (n *Node) State() State {
	if !n.IsGroup() {
		n.mu.Lock()
		defer n.mu.Unlock()
		return n.state
	}
	return aggregate(n.children)
}

func (n *Node) snapshot() (Snapshot, time.Time, time.Time) {
	if !n.IsGroup() {
		n.mu.Lock()
		defer n.mu.Unlock()
		return Snapshot{
			Name:        n.Def.Name,
			Description: n.Def.Description,
			Path:        n.Def.Path,
			State:       n.state,
			Duration:    n.duration,
			IsGroup:     false,
		}, n.startedAt, n.endedAt
	}

	children := make([]Snapshot, 0, len(n.children))
	var minStart, maxEnd time.Time
	for _, c := range n.children {
		cs, st, et := c.snapshot()
		children = append(children, cs)
		if !st.IsZero() && (minStart.IsZero() || st.Before(minStart)) {
			minStart = st
		}
		if et.After(maxEnd) {
			maxEnd = et
		}
	}

	var dur time.Duration
	if !minStart.IsZero() && !maxEnd.IsZero() {
		dur = maxEnd.Sub(minStart)
	}

	return Snapshot{
		Name:        n.Def.Name,
		Description: n.Def.Description,
		Path:        n.Def.Path,
		State:       aggregate(n.children),
		Duration:    dur,
		IsGroup:     true,
		Children:    children,
	}, minStart, maxEnd
}

// aggregate derives a group state from its children's states.
func aggregate(children []*Node) State {
	hasRunning := false
	hasFailed := false
	hasPending := false
	hasCancelled := false
	hasSkipped := false
	for _, c := range children {
		switch c.State() {
		case StateRunning:
			hasRunning = true
		case StateFailed:
			hasFailed = true
		case StatePending:
			hasPending = true
		case StateCancelled:
			hasCancelled = true
		case StateSkipped:
			hasSkipped = true
		}
	}

	switch {
	case hasRunning:
		return StateRunning
	case hasFailed:
		return StateFailed
	case hasPending:
		return StatePending
	case hasCancelled:
		return StateCancelled
	case hasSkipped:
		return StateSkipped
	default:
		return StateSucceeded
	}
}
