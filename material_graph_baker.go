// Package task bakes a material node graph into a material definition, while
// checking the graph topology, the port types and the keyword cache key.
package task

import (
	"errors"
	"sync"
)

// errNotImplemented marks behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// Node kinds understood by the baker.
const (
	KindConstant = "constant"
	KindAdd      = "add"
	KindMul      = "mul"
	KindClamp    = "clamp"
)

// Port addresses one endpoint of a graph link.
type Port struct {
	Node string
	Name string
	Type string
}

// Node is one material graph node.
type Node struct {
	ID         string
	Kind       string
	Params     map[string]float64
	OutputType string
}

// MaterialDef is the baked material handed to the renderer.
type MaterialDef struct {
	Name     string
	Hash     string
	Order    []string
	Values   map[string]float64
	Keywords []string
	Version  uint64
}

// BakerStats reports the graph size and the bake counters.
type BakerStats struct {
	Nodes     int
	Edges     int
	Bakes     int
	CacheHits uint64
	Version   uint64
}

// Errors reported by the baker.
var (
	ErrNodeExists        = errors.New("node id already exists")
	ErrNodeMissing       = errors.New("node id is not in the graph")
	ErrBadNode           = errors.New("node id is empty")
	ErrUnknownKind       = errors.New("node kind is unknown")
	ErrUnknownPort       = errors.New("port does not exist on the node")
	ErrPortType          = errors.New("port types do not match")
	ErrInputTaken        = errors.New("input port is already connected")
	ErrCycle             = errors.New("connection would create a cycle")
	ErrDangling          = errors.New("node input is not connected")
	ErrNodeHasDependents = errors.New("node still feeds another node")
	ErrEmptyGraph        = errors.New("graph has no nodes")
	ErrBadSnapshot       = errors.New("snapshot payload is invalid")
)

// MaterialGraphBaker owns the node graph and the baked material cache.
type MaterialGraphBaker struct {
	mu   sync.RWMutex
	name string
}

// NewMaterialGraphBaker builds an empty graph for one material name.
func NewMaterialGraphBaker(name string) *MaterialGraphBaker {
	return &MaterialGraphBaker{name: name}
}

// AddNode inserts one node into the graph.
func (b *MaterialGraphBaker) AddNode(node Node) error {
	return errNotImplemented
}

// RemoveNode removes a node that no other node references.
func (b *MaterialGraphBaker) RemoveNode(id string) error {
	return errNotImplemented
}

// Connect links one output port to one input port.
func (b *MaterialGraphBaker) Connect(from Port, to Port) error {
	return errNotImplemented
}

// Disconnect frees one input port.
func (b *MaterialGraphBaker) Disconnect(to Port) error {
	return errNotImplemented
}

// Bake evaluates the graph and returns the material definition.
func (b *MaterialGraphBaker) Bake() (*MaterialDef, error) {
	return nil, errNotImplemented
}

// Invalidate marks one node and its downstream nodes for re-evaluation.
func (b *MaterialGraphBaker) Invalidate(nodeID string) error {
	return errNotImplemented
}

// InvalidateAll marks every node for re-evaluation.
func (b *MaterialGraphBaker) InvalidateAll() error {
	return errNotImplemented
}

// Order reports the topological order of the graph.
func (b *MaterialGraphBaker) Order() ([]string, error) {
	return nil, errNotImplemented
}

// Node reports one stored node.
func (b *MaterialGraphBaker) Node(id string) (Node, bool) {
	return Node{}, false
}

// SetKeywords replaces the keyword set that participates in the cache key.
func (b *MaterialGraphBaker) SetKeywords(keywords []string) error {
	return errNotImplemented
}

// Keywords reports the normalized keyword set.
func (b *MaterialGraphBaker) Keywords() []string {
	return nil
}

// Stats reports the graph size and the bake counters.
func (b *MaterialGraphBaker) Stats() BakerStats {
	return BakerStats{}
}

// Snapshot serialises the nodes, the links, the keywords and the version.
func (b *MaterialGraphBaker) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore rebuilds the graph from a snapshot, either completely or not at all.
func (b *MaterialGraphBaker) Restore(payload []byte) error {
	return errNotImplemented
}

// Reset clears the graph, the keywords and the counters.
func (b *MaterialGraphBaker) Reset() error {
	return errNotImplemented
}
