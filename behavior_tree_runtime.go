package task

import (
	"errors"
	"sync"
)

// errNotImplemented is returned by every behaviour that still needs a fix.
var errNotImplemented = errors.New("not implemented")

// Errors reported by the behaviour tree runtime.
var (
	ErrInvalidNode   = errors.New("node definition rejected")
	ErrUnknownKind   = errors.New("unknown node kind")
	ErrEmptyChildren = errors.New("composite node has no children")
	ErrNotLoaded     = errors.New("tree is not loaded")
	ErrInvalidBudget = errors.New("node budget rejected")
	ErrUnknownAgent  = errors.New("agent has no runtime state")
	ErrNotRunning    = errors.New("agent is not running")
	ErrBadSnapshot   = errors.New("snapshot payload rejected")
)

// Status is the outcome of one tick or one node evaluation.
type Status int

// Status values reported by the runner.
const (
	StatusRunning Status = iota
	StatusSuccess
	StatusFailure
)

// Node is one node of a tree definition.
type Node struct {
	ID       string
	Kind     string
	Children []Node
	Key      string
	Op       string
	Value    int
}

// Step is one evaluation record of the runtime.
type Step struct {
	NodeID string
	Status Status
	At     int64
}

// Blackboard is the mutable data seen by one agent.
type Blackboard map[string]int

// Options carries the tuning of one runtime build.
type Options struct {
	Budget     int
	TraceLimit int
	Blackboard map[string]int
}

// BehaviorTreeRunner executes one tree for many agents.
type BehaviorTreeRunner struct {
	mu     sync.Mutex
	opts   Options
	root   Node
	loaded bool
	agents map[string]*agentState
}

type agentState struct {
	board   Blackboard
	budget  int
	status  Status
	running bool
	steps   []Step
}

// NewBehaviorTreeRunner builds a runner with the given tuning.
func NewBehaviorTreeRunner(opts Options) *BehaviorTreeRunner {
	return &BehaviorTreeRunner{opts: opts, agents: make(map[string]*agentState)}
}

// Load validates and installs a tree definition.
func (g *BehaviorTreeRunner) Load(root Node) error {
	return errNotImplemented
}

// Loaded reports whether a tree definition has been installed.
func (g *BehaviorTreeRunner) Loaded() bool {
	return false
}

// Tick advances one agent by at most one node budget.
func (g *BehaviorTreeRunner) Tick(agent string, at int64) (Status, error) {
	return StatusRunning, errNotImplemented
}

// Interrupt stops the running execution of one agent.
func (g *BehaviorTreeRunner) Interrupt(agent, reason string) error {
	return errNotImplemented
}

// Blackboard returns a copy of the visible blackboard of one agent.
func (g *BehaviorTreeRunner) Blackboard(agent string) Blackboard {
	return nil
}

// SetBlackboard installs starting values for one agent.
func (g *BehaviorTreeRunner) SetBlackboard(agent string, values map[string]int) error {
	return errNotImplemented
}

// SetBudget overrides the node budget of one agent.
func (g *BehaviorTreeRunner) SetBudget(agent string, nodes int) error {
	return errNotImplemented
}

// Trace returns the bounded evaluation records of one agent.
func (g *BehaviorTreeRunner) Trace(agent string) []Step {
	return nil
}

// State reports the last status of one agent.
func (g *BehaviorTreeRunner) State(agent string) Status {
	return StatusRunning
}

// Snapshot serialises agents, blackboards, cursors and records.
func (g *BehaviorTreeRunner) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the runtime state from a snapshot.
func (g *BehaviorTreeRunner) Restore(data []byte) error {
	return errNotImplemented
}
