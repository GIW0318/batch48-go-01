package task

import (
	"encoding/json"
	"errors"
	"sync"
)

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

const (
	kindCondition = "condition"
	kindSet       = "set"
	kindSequence  = "sequence"
	kindSelector  = "selector"
	kindParallel  = "parallel"
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
	mu       sync.RWMutex
	agentsMu sync.Mutex
	opts     Options
	root     Node
	loaded   bool
	agents   map[string]*agentState
}

type agentState struct {
	mu        sync.Mutex
	board     Blackboard
	budget    int
	status    Status
	running   bool
	steps     []Step
	lastAt    int64
	lastAtSet bool
	exec      *execNode
}

// execNode is the resumable execution state of one definition node.
type execNode struct {
	Status      Status     `json:"status"`
	Cursor      int        `json:"cursor"`
	Children    []execNode `json:"children,omitempty"`
	Board       Blackboard `json:"board"`
	Interrupted bool       `json:"interrupted,omitempty"`
}

type snapshotData struct {
	Version int                      `json:"version"`
	Loaded  bool                     `json:"loaded"`
	Root    Node                     `json:"root"`
	Options Options                  `json:"options"`
	Agents  map[string]snapshotAgent `json:"agents"`
}

type snapshotAgent struct {
	Board     Blackboard `json:"board"`
	Budget    int        `json:"budget"`
	Status    Status     `json:"status"`
	Running   bool       `json:"running"`
	Steps     []Step     `json:"steps"`
	LastAt    int64      `json:"last_at"`
	LastAtSet bool       `json:"last_at_set"`
	Exec      *execNode  `json:"exec,omitempty"`
}

const snapshotVersion = 1

type tickContext struct {
	budget     int
	at         int64
	traceLimit int
	agent      *agentState
}

// NewBehaviorTreeRunner builds a runner with the given tuning.
func NewBehaviorTreeRunner(opts Options) *BehaviorTreeRunner {
	opts.Blackboard = cloneBlackboard(opts.Blackboard)
	if opts.TraceLimit < 0 {
		opts.TraceLimit = 0
	}
	return &BehaviorTreeRunner{
		opts:   opts,
		agents: make(map[string]*agentState),
	}
}

// Load validates and installs a tree definition.
func (g *BehaviorTreeRunner) Load(root Node) error {
	if err := validateTree(&root); err != nil {
		return err
	}
	installed := cloneNode(root)

	g.mu.Lock()
	defer g.mu.Unlock()

	g.root = installed
	g.loaded = true
	g.agentsMu.Lock()
	for _, agent := range g.agents {
		agent.mu.Lock()
		agent.exec = nil
		agent.running = false
		agent.status = StatusRunning
		agent.steps = nil
		agent.lastAt = 0
		agent.lastAtSet = false
		agent.mu.Unlock()
	}
	g.agentsMu.Unlock()
	return nil
}

// Loaded reports whether a tree definition has been installed.
func (g *BehaviorTreeRunner) Loaded() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.loaded
}

// Tick advances one agent by at most one node budget.
func (g *BehaviorTreeRunner) Tick(agent string, at int64) (Status, error) {
	state, unlock, err := g.acquireAgent(agent, true)
	if err != nil {
		return StatusRunning, err
	}
	defer unlock()

	state.mu.Lock()
	defer state.mu.Unlock()

	return state.tick(&g.root, at, g.opts.TraceLimit), nil
}

// Interrupt stops the running execution of one agent.
func (g *BehaviorTreeRunner) Interrupt(agent, reason string) error {
	_ = reason

	g.mu.RLock()
	defer g.mu.RUnlock()

	state := g.lookupAgent(agent)
	if state == nil {
		return ErrUnknownAgent
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.running {
		return ErrNotRunning
	}
	state.exec = nil
	state.running = false
	state.status = StatusFailure
	return nil
}

// Blackboard returns a copy of the visible blackboard of one agent.
func (g *BehaviorTreeRunner) Blackboard(agent string) Blackboard {
	g.mu.RLock()
	defer g.mu.RUnlock()

	state := g.lookupAgent(agent)
	if state == nil {
		return Blackboard{}
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	return cloneBlackboard(state.board)
}

// SetBlackboard installs starting values for one agent.
func (g *BehaviorTreeRunner) SetBlackboard(agent string, values map[string]int) error {
	g.mu.RLock()
	defer g.mu.RUnlock()
	g.agentsMu.Lock()
	state := g.agents[agent]
	if state == nil {
		state = &agentState{
			board:  cloneBlackboard(values),
			budget: g.opts.Budget,
			status: StatusRunning,
		}
		g.agents[agent] = state
	}
	g.agentsMu.Unlock()

	state.mu.Lock()
	defer state.mu.Unlock()
	state.board = cloneBlackboard(values)
	state.exec = nil
	state.running = false
	state.status = StatusRunning
	return nil
}

// SetBudget overrides the node budget of one agent.
func (g *BehaviorTreeRunner) SetBudget(agent string, nodes int) error {
	if nodes <= 0 {
		return ErrInvalidBudget
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	state := g.lookupAgent(agent)
	if state == nil {
		return ErrUnknownAgent
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	state.budget = nodes
	return nil
}

// Trace returns the bounded evaluation records of one agent.
func (g *BehaviorTreeRunner) Trace(agent string) []Step {
	g.mu.RLock()
	defer g.mu.RUnlock()

	state := g.lookupAgent(agent)
	if state == nil {
		return nil
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.steps) == 0 {
		return nil
	}
	steps := make([]Step, len(state.steps))
	copy(steps, state.steps)
	return steps
}

// State reports the last status of one agent.
func (g *BehaviorTreeRunner) State(agent string) Status {
	g.mu.RLock()
	defer g.mu.RUnlock()

	state := g.lookupAgent(agent)
	if state == nil {
		return StatusRunning
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	return state.status
}

// Snapshot serialises agents, blackboards, cursors and records.
func (g *BehaviorTreeRunner) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	data := snapshotData{
		Version: snapshotVersion,
		Loaded:  g.loaded,
		Root:    cloneNode(g.root),
		Options: Options{
			Budget:     g.opts.Budget,
			TraceLimit: g.opts.TraceLimit,
			Blackboard: cloneBlackboard(g.opts.Blackboard),
		},
		Agents: make(map[string]snapshotAgent, len(g.agents)),
	}

	g.agentsMu.Lock()
	for name, state := range g.agents {
		state.mu.Lock()
		entry := snapshotAgent{
			Board:     cloneBlackboard(state.board),
			Budget:    state.budget,
			Status:    state.status,
			Running:   state.running,
			LastAt:    state.lastAt,
			LastAtSet: state.lastAtSet,
		}
		if len(state.steps) != 0 {
			entry.Steps = make([]Step, len(state.steps))
			copy(entry.Steps, state.steps)
		}
		if state.exec != nil {
			entry.Exec = cloneExec(state.exec)
		}
		state.mu.Unlock()
		data.Agents[name] = entry
	}
	g.agentsMu.Unlock()

	payload, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	return payload, nil
}

// Restore replaces the runtime state from a snapshot.
func (g *BehaviorTreeRunner) Restore(data []byte) error {
	var decoded snapshotData
	if err := json.Unmarshal(data, &decoded); err != nil {
		return ErrBadSnapshot
	}
	if err := validateSnapshot(&decoded); err != nil {
		return ErrBadSnapshot
	}

	restored := make(map[string]*agentState, len(decoded.Agents))
	for name, entry := range decoded.Agents {
		state := &agentState{
			board:     cloneBlackboard(entry.Board),
			budget:    entry.Budget,
			status:    entry.Status,
			running:   entry.Running,
			lastAt:    entry.LastAt,
			lastAtSet: entry.LastAtSet,
		}
		if len(entry.Steps) != 0 {
			state.steps = make([]Step, len(entry.Steps))
			copy(state.steps, entry.Steps)
		}
		if entry.Exec != nil {
			state.exec = cloneExec(entry.Exec)
		}
		restored[name] = state
	}

	opts := decoded.Options
	opts.Blackboard = cloneBlackboard(opts.Blackboard)
	if opts.TraceLimit < 0 {
		opts.TraceLimit = 0
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.opts = opts
	g.root = cloneNode(decoded.Root)
	g.loaded = decoded.Loaded
	g.agentsMu.Lock()
	g.agents = restored
	g.agentsMu.Unlock()
	return nil
}

// acquireAgent looks up an agent and, for Tick, creates it on first use. The
// returned unlock function releases the read lock held for the whole operation.
func (g *BehaviorTreeRunner) acquireAgent(name string, requireLoaded bool) (*agentState, func(), error) {
	g.mu.RLock()
	if requireLoaded && !g.loaded {
		g.mu.RUnlock()
		return nil, nil, ErrNotLoaded
	}

	state := g.lookupAgent(name)
	if state == nil {
		g.agentsMu.Lock()
		if state = g.agents[name]; state == nil {
			state = &agentState{
				board:  cloneBlackboard(g.opts.Blackboard),
				budget: g.opts.Budget,
				status: StatusRunning,
			}
			g.agents[name] = state
		}
		g.agentsMu.Unlock()
	}
	return state, g.mu.RUnlock, nil
}

func (g *BehaviorTreeRunner) lookupAgent(name string) *agentState {
	g.agentsMu.Lock()
	state := g.agents[name]
	g.agentsMu.Unlock()
	return state
}

func (a *agentState) tick(root *Node, at int64, traceLimit int) Status {
	if a.exec == nil {
		a.exec = newExec(*root)
		a.status = StatusRunning
		a.running = true
	}

	ctx := &tickContext{
		budget:     a.budget,
		at:         at,
		traceLimit: traceLimit,
		agent:      a,
	}
	status := evalNode(root, a.exec, a.board, ctx)
	a.status = status
	if status == StatusRunning {
		a.running = true
	} else {
		a.running = false
		a.exec = nil
	}
	return status
}

func (c *tickContext) record(nodeID string, status Status) {
	at := c.at
	if c.agent.lastAtSet && at < c.agent.lastAt {
		at = c.agent.lastAt
	}
	c.agent.lastAt = at
	c.agent.lastAtSet = true

	if c.traceLimit <= 0 {
		return
	}
	c.agent.steps = append(c.agent.steps, Step{NodeID: nodeID, Status: status, At: at})
	if len(c.agent.steps) > c.traceLimit {
		trimmed := make([]Step, c.traceLimit)
		copy(trimmed, c.agent.steps[len(c.agent.steps)-c.traceLimit:])
		c.agent.steps = trimmed
	}
}

func evalNode(n *Node, frame *execNode, scope Blackboard, ctx *tickContext) Status {
	switch n.Kind {
	case kindCondition, kindSet:
		return evalLeaf(n, frame, scope, ctx)
	case kindSequence:
		return evalSequence(n, frame, scope, ctx)
	case kindSelector:
		return evalSelector(n, frame, scope, ctx)
	case kindParallel:
		return evalParallel(n, frame, scope, ctx)
	default:
		return StatusFailure
	}
}

func evalLeaf(n *Node, frame *execNode, scope Blackboard, ctx *tickContext) Status {
	if frame.Status != StatusRunning {
		return frame.Status
	}
	if ctx.budget <= 0 {
		return StatusRunning
	}
	ctx.budget--

	status := StatusSuccess
	if n.Kind == kindCondition {
		value, ok := scope[n.Key]
		if !ok {
			value = 0
		}
		switch n.Op {
		case "eq":
			if value != n.Value {
				status = StatusFailure
			}
		case "ne":
			if value == n.Value {
				status = StatusFailure
			}
		case "gt":
			if value <= n.Value {
				status = StatusFailure
			}
		case "lt":
			if value >= n.Value {
				status = StatusFailure
			}
		default:
			status = StatusFailure
		}
	} else {
		scope[n.Key] = n.Value
	}

	frame.Status = status
	ctx.record(n.ID, status)
	return status
}

func evalSequence(n *Node, frame *execNode, scope Blackboard, ctx *tickContext) Status {
	if frame.Status != StatusRunning {
		return frame.Status
	}

	for frame.Cursor < len(n.Children) {
		childNode := &n.Children[frame.Cursor]
		childFrame := &frame.Children[frame.Cursor]

		if childFrame.Status == StatusSuccess {
			frame.Cursor++
			discardBranch(childFrame)
			continue
		}
		if childFrame.Status != StatusRunning {
			frame.Status = StatusFailure
			return StatusFailure
		}
		if ctx.budget <= 0 {
			return StatusRunning
		}

		childScope := prepareScope(childNode, childFrame, scope, false)
		status := evalNode(childNode, childFrame, childScope, ctx)
		switch status {
		case StatusRunning:
			return StatusRunning
		case StatusFailure:
			frame.Status = StatusFailure
			discardBranch(childFrame)
			return StatusFailure
		default:
			frame.Cursor++
			discardBranch(childFrame)
		}
	}

	frame.Status = StatusSuccess
	return StatusSuccess
}

func evalSelector(n *Node, frame *execNode, scope Blackboard, ctx *tickContext) Status {
	if frame.Status != StatusRunning {
		return frame.Status
	}

	for frame.Cursor < len(n.Children) {
		childNode := &n.Children[frame.Cursor]
		childFrame := &frame.Children[frame.Cursor]

		if childFrame.Status == StatusFailure {
			frame.Cursor++
			discardBranch(childFrame)
			continue
		}
		if childFrame.Status == StatusSuccess {
			frame.Status = StatusSuccess
			return StatusSuccess
		}
		if ctx.budget <= 0 {
			return StatusRunning
		}

		childScope := prepareScope(childNode, childFrame, scope, false)
		status := evalNode(childNode, childFrame, childScope, ctx)
		switch status {
		case StatusRunning:
			return StatusRunning
		case StatusFailure:
			frame.Cursor++
			discardBranch(childFrame)
			continue
		default:
			frame.Status = StatusSuccess
			return StatusSuccess
		}
	}

	frame.Status = StatusFailure
	return StatusFailure
}

func evalParallel(n *Node, frame *execNode, scope Blackboard, ctx *tickContext) Status {
	if frame.Status != StatusRunning {
		return frame.Status
	}

	for i := range n.Children {
		childFrame := &frame.Children[i]
		if childFrame.Interrupted || childFrame.Status == StatusFailure {
			frame.Status = StatusFailure
			interruptOthers(frame, i)
			return StatusFailure
		}
	}

	for i := range n.Children {
		childNode := &n.Children[i]
		childFrame := &frame.Children[i]
		if childFrame.Status == StatusSuccess {
			continue
		}
		if ctx.budget <= 0 {
			return StatusRunning
		}

		childScope := prepareScope(childNode, childFrame, scope, true)
		status := evalNode(childNode, childFrame, childScope, ctx)
		switch status {
		case StatusRunning:
			return StatusRunning
		case StatusFailure:
			childFrame.Status = StatusFailure
			frame.Status = StatusFailure
			interruptOthers(frame, i)
			return StatusFailure
		default:
			childFrame.Status = StatusSuccess
			discardBranch(childFrame)
		}
	}

	for i := range frame.Children {
		if frame.Children[i].Status != StatusSuccess {
			return StatusRunning
		}
	}
	frame.Status = StatusSuccess
	return StatusSuccess
}

func prepareScope(node *Node, frame *execNode, parent Blackboard, isolate bool) Blackboard {
	if !isolate && !isComposite(node.Kind) {
		return parent
	}
	if frame.Board == nil {
		frame.Board = cloneBlackboard(parent)
	}
	return frame.Board
}

func interruptOthers(frame *execNode, skipped int) {
	for i := range frame.Children {
		if i == skipped {
			continue
		}
		child := &frame.Children[i]
		if child.Status == StatusSuccess {
			continue
		}
		markInterrupted(child)
	}
}

func markInterrupted(frame *execNode) {
	if frame.Status == StatusRunning {
		frame.Status = StatusFailure
	}
	frame.Interrupted = true
	frame.Board = nil
	for i := range frame.Children {
		markInterrupted(&frame.Children[i])
	}
}

func discardBranch(frame *execNode) {
	frame.Board = nil
	for i := range frame.Children {
		discardBranch(&frame.Children[i])
	}
}

func isComposite(kind string) bool {
	return kind == kindSequence || kind == kindSelector || kind == kindParallel
}

func validateTree(node *Node) error {
	if node.ID == "" {
		return ErrInvalidNode
	}
	switch node.Kind {
	case kindCondition:
		switch node.Op {
		case "eq", "ne", "gt", "lt":
			return nil
		default:
			return ErrInvalidNode
		}
	case kindSet:
		return nil
	case kindSequence, kindSelector, kindParallel:
		if len(node.Children) == 0 {
			return ErrEmptyChildren
		}
		for i := range node.Children {
			if err := validateTree(&node.Children[i]); err != nil {
				return err
			}
		}
		return nil
	default:
		return ErrUnknownKind
	}
}

func newExec(node Node) *execNode {
	frame := &execNode{Status: StatusRunning}
	if len(node.Children) != 0 {
		frame.Children = make([]execNode, len(node.Children))
		for i := range node.Children {
			frame.Children[i] = *newExec(node.Children[i])
		}
	}
	return frame
}

func cloneNode(node Node) Node {
	out := node
	if node.Children != nil {
		out.Children = make([]Node, len(node.Children))
		for i := range node.Children {
			out.Children[i] = cloneNode(node.Children[i])
		}
	}
	return out
}

func cloneExec(frame *execNode) *execNode {
	if frame == nil {
		return nil
	}
	out := &execNode{
		Status:      frame.Status,
		Cursor:      frame.Cursor,
		Interrupted: frame.Interrupted,
	}
	if frame.Board != nil {
		out.Board = cloneBlackboard(frame.Board)
	}
	if frame.Children != nil {
		out.Children = make([]execNode, len(frame.Children))
		for i := range frame.Children {
			out.Children[i] = *cloneExec(&frame.Children[i])
		}
	}
	return out
}

func cloneBlackboard(board Blackboard) Blackboard {
	out := make(Blackboard, len(board))
	for key, value := range board {
		out[key] = value
	}
	return out
}

func validateSnapshot(data *snapshotData) error {
	if data.Version != snapshotVersion {
		return ErrBadSnapshot
	}
	if data.Loaded {
		if err := validateTree(&data.Root); err != nil {
			return ErrBadSnapshot
		}
	}
	if data.Options.TraceLimit < 0 {
		return ErrBadSnapshot
	}
	if data.Agents == nil {
		return ErrBadSnapshot
	}

	for _, agent := range data.Agents {
		if !validStatus(agent.Status) {
			return ErrBadSnapshot
		}
		if agent.Running {
			if !data.Loaded || agent.Exec == nil || agent.Status != StatusRunning {
				return ErrBadSnapshot
			}
		} else if agent.Exec != nil {
			return ErrBadSnapshot
		}
		if len(agent.Steps) > data.Options.TraceLimit {
			return ErrBadSnapshot
		}
		for i, step := range agent.Steps {
			if !validStatus(step.Status) {
				return ErrBadSnapshot
			}
			if i > 0 && step.At < agent.Steps[i-1].At {
				return ErrBadSnapshot
			}
		}
		if agent.LastAtSet && len(agent.Steps) != 0 {
			if agent.Steps[len(agent.Steps)-1].At > agent.LastAt {
				return ErrBadSnapshot
			}
		}
		if agent.Exec != nil {
			if err := validateExec(&data.Root, agent.Exec); err != nil {
				return ErrBadSnapshot
			}
		}
	}
	return nil
}

func validateExec(node *Node, frame *execNode) error {
	if frame == nil || !validStatus(frame.Status) {
		return ErrBadSnapshot
	}
	if frame.Interrupted && frame.Status == StatusSuccess {
		return ErrBadSnapshot
	}

	switch node.Kind {
	case kindCondition, kindSet:
		if frame.Cursor != 0 || len(frame.Children) != 0 {
			return ErrBadSnapshot
		}
	case kindSequence, kindSelector, kindParallel:
		if len(frame.Children) != len(node.Children) {
			return ErrBadSnapshot
		}
		if frame.Cursor < 0 || frame.Cursor > len(node.Children) {
			return ErrBadSnapshot
		}
		for i := range frame.Children {
			if err := validateExec(&node.Children[i], &frame.Children[i]); err != nil {
				return ErrBadSnapshot
			}
		}

		if frame.Interrupted {
			return nil
		}

		switch node.Kind {
		case kindSequence:
			for i := 0; i < frame.Cursor; i++ {
				if frame.Children[i].Status != StatusSuccess {
					return ErrBadSnapshot
				}
			}
			if frame.Status == StatusFailure {
				if frame.Cursor >= len(frame.Children) ||
					frame.Children[frame.Cursor].Status != StatusFailure {
					return ErrBadSnapshot
				}
			}
		case kindSelector:
			for i := 0; i < frame.Cursor; i++ {
				if frame.Children[i].Status != StatusFailure {
					return ErrBadSnapshot
				}
			}
			if frame.Status == StatusFailure && frame.Cursor != len(frame.Children) {
				return ErrBadSnapshot
			}
			if frame.Status == StatusSuccess {
				if frame.Cursor >= len(frame.Children) ||
					frame.Children[frame.Cursor].Status != StatusSuccess {
					return ErrBadSnapshot
				}
			}
		case kindParallel:
			if frame.Cursor != 0 {
				return ErrBadSnapshot
			}
			if frame.Status == StatusSuccess {
				for i := range frame.Children {
					if frame.Children[i].Status != StatusSuccess {
						return ErrBadSnapshot
					}
				}
			}
		}
	default:
		return ErrBadSnapshot
	}
	return nil
}

func validStatus(status Status) bool {
	return status == StatusRunning || status == StatusSuccess || status == StatusFailure
}
