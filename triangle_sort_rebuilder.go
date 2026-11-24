// Package task rebuilds the draw order of a semi transparent mesh for one
// view direction: triangles are sorted by the projection of their centroid on
// the view direction, equal depths keep a stable triangle id order and the
// previous order is reused when the camera did not move.
package task

import (
	"errors"
	"sync"
)

// errNotImplemented marks behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// Vec3 is a point or direction in world space.
type Vec3 struct {
	X float64
	Y float64
	Z float64
}

// Vertex is one mesh vertex.
type Vertex struct {
	Pos Vec3
}

// Tri is one triangle of a mesh, addressed by three vertex indices.
type Tri struct {
	A uint32
	B uint32
	C uint32
}

// Mesh is the vertex and triangle table of one semi transparent mesh.
type Mesh struct {
	Vertices []Vertex
	Tris     []Tri
}

// IndexBuffer is a triangle draw order.
type IndexBuffer []uint32

// OrderStats reports what the most recent rebuild did.
type OrderStats struct {
	Triangles  int
	Degenerate int
	Sorted     int
	Reused     bool
	Version    uint64
}

// Errors reported by the rebuilder.
var (
	ErrBadBudget       = errors.New("index budget must be positive")
	ErrBadMesh         = errors.New("mesh is invalid")
	ErrNoMesh          = errors.New("no mesh has been set")
	ErrBadViewDir      = errors.New("view direction is degenerate")
	ErrBudgetExceeded  = errors.New("triangle count exceeds the index budget")
	ErrBadVertexCount  = errors.New("vertex count does not match the mesh")
	ErrBadSnapshot     = errors.New("snapshot payload is invalid")
)

// TriangleSortRebuilder owns one mesh and its current draw order.
type TriangleSortRebuilder struct {
	mu       sync.Mutex
	budget   int
	mesh     Mesh
	version  uint64
	order    IndexBuffer
	view     Vec3
	viewed   bool
	stats    OrderStats
	degener  int
}

// NewTriangleSortRebuilder builds a rebuilder with the given index budget.
func NewTriangleSortRebuilder(budget int) *TriangleSortRebuilder {
	return &TriangleSortRebuilder{budget: budget}
}

// SetIndexBudget changes how many triangles one order may contain.
func (r *TriangleSortRebuilder) SetIndexBudget(n int) error {
	return errNotImplemented
}

// IndexBudget reports the configured index budget.
func (r *TriangleSortRebuilder) IndexBudget() int {
	return 0
}

// SetMesh replaces the mesh and bumps its version.
func (r *TriangleSortRebuilder) SetMesh(mesh Mesh) error {
	return errNotImplemented
}

// MeshVersion reports the version of the current mesh.
func (r *TriangleSortRebuilder) MeshVersion() uint64 {
	return 0
}

// Update replaces the vertex positions of the current mesh.
func (r *TriangleSortRebuilder) Update(verts []Vertex) error {
	return errNotImplemented
}

// Rebuild sorts the triangles for one view direction.
func (r *TriangleSortRebuilder) Rebuild(viewDir Vec3) (IndexBuffer, error) {
	return nil, errNotImplemented
}

// LastOrder reports the draw order produced by the last successful rebuild.
func (r *TriangleSortRebuilder) LastOrder() (IndexBuffer, bool) {
	return nil, false
}

// LastView reports the view direction of the last successful rebuild.
func (r *TriangleSortRebuilder) LastView() (Vec3, bool) {
	return Vec3{}, false
}

// OrderStats reports the counters of the last rebuild.
func (r *TriangleSortRebuilder) OrderStats() OrderStats {
	return OrderStats{}
}

// DegenerateCount reports how many degenerate triangles the last rebuild skipped.
func (r *TriangleSortRebuilder) DegenerateCount() int {
	return 0
}

// Reset drops the mesh, the order and the counters.
func (r *TriangleSortRebuilder) Reset() error {
	return errNotImplemented
}

// Snapshot serialises the mesh, the version and the current order.
func (r *TriangleSortRebuilder) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore rebuilds the rebuilder from a snapshot, either completely or not at all.
func (r *TriangleSortRebuilder) Restore(payload []byte) error {
	return errNotImplemented
}
