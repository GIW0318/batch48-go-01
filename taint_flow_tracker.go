// Package task solves label-level taint propagation over a graph of assignment,
// alias and call edges and reports which sinks stay reachable.
package task

import "errors"

var errNotImplemented = errors.New("taint flow tracker: not implemented")

// Errors surfaced while building and solving the taint graph.
var (
	ErrBadNode       = errors.New("taint flow tracker: bad node")
	ErrEmptyLabels   = errors.New("taint flow tracker: empty labels")
	ErrUnknownEdge   = errors.New("taint flow tracker: unknown edge kind")
	ErrUnknownNode   = errors.New("taint flow tracker: unknown node")
	ErrDuplicateSink = errors.New("taint flow tracker: duplicate sink")
	ErrNoSummary     = errors.New("taint flow tracker: no complete summary")
	ErrNotSolved     = errors.New("taint flow tracker: not solved")
	ErrBadBudget     = errors.New("taint flow tracker: bad budget")
)

// EdgeKind tells how taint travels between two nodes.
type EdgeKind int

// Supported edge kinds.
const (
	EdgeAssign EdgeKind = iota
	EdgeAlias
	EdgeCall
)

// Options bounds a solver instance.
type Options struct {
	MaxPaths      int
	MaxIterations int
}

// Sink is a program point that reports tainted input.
type Sink struct {
	Node string
	Kind string
}

// SinkHit is one reachable sink together with the labels that reach it.
type SinkHit struct {
	Node   string
	Kind   string
	Labels []string
}

// Summary is the last complete interprocedural result of one function.
type Summary struct {
	Function     string
	Complete     bool
	ReturnLabels []string
	Paths        int
}

// Solution is the settled result of one solve.
type Solution struct {
	Complete   bool
	Iterations int
	Sinks      []SinkHit
}

// SolveStats describes the last solve.
type SolveStats struct {
	Iterations int
	Paths      int
	Truncated  bool
}

// TaintTracker accumulates sources, edges, sanitizers and sinks.
type TaintTracker struct {
	opts Options
}

// NewTaintTracker returns a tracker configured by opts.
func NewTaintTracker(opts Options) *TaintTracker {
	return &TaintTracker{opts: opts}
}

// MarkSource tags node with the given labels.
func (t *TaintTracker) MarkSource(node string, labels []string) error {
	return errNotImplemented
}

// AddSink registers node as a sink of the given kind.
func (t *TaintTracker) AddSink(node, kind string) error {
	return errNotImplemented
}

// Propagate adds one edge of the given kind.
func (t *TaintTracker) Propagate(from, to string, kind EdgeKind) error {
	return errNotImplemented
}

// ApplySanitizer removes the named labels at node.
func (t *TaintTracker) ApplySanitizer(node string, labels []string) error {
	return errNotImplemented
}

// SetSummary installs the last complete summary of a callee.
func (t *TaintTracker) SetSummary(fn string, s Summary) error {
	return errNotImplemented
}

// Solve runs the fixed point until labels stop changing.
func (t *TaintTracker) Solve() (Solution, error) {
	return Solution{}, errNotImplemented
}

// ReachableSinks reports the settled sink hits in stable order.
func (t *TaintTracker) ReachableSinks() ([]SinkHit, error) {
	return nil, errNotImplemented
}

// Labels reports the settled label set of one node.
func (t *TaintTracker) Labels(node string) ([]string, error) {
	return nil, errNotImplemented
}

// SetBudget bounds the next solve.
func (t *TaintTracker) SetBudget(maxPaths, maxIterations int) error {
	return errNotImplemented
}

// Reset clears the graph, the solution and the counters.
func (t *TaintTracker) Reset() error {
	return errNotImplemented
}

// Stats describes the last solve.
func (t *TaintTracker) Stats() SolveStats {
	return SolveStats{}
}

// Nodes reports every node id in lexical order.
func (t *TaintTracker) Nodes() []string {
	return nil
}
