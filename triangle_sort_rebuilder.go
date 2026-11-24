// Package task rebuilds the draw order of a semi transparent mesh for one
// view direction: triangles are sorted by the projection of their centroid on
// the view direction, equal depths keep a stable triangle id order and the
// previous order is reused when the view direction is unchanged.
package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
)

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

// IndexBuffer is a triangle draw order. Entries are triangle ids.
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
	ErrBadBudget      = errors.New("index budget must be positive")
	ErrBadMesh        = errors.New("mesh is invalid")
	ErrNoMesh         = errors.New("no mesh has been set")
	ErrBadViewDir     = errors.New("view direction is degenerate")
	ErrBudgetExceeded = errors.New("triangle count exceeds the index budget")
	ErrBadVertexCount = errors.New("vertex count does not match the mesh")
	ErrBadSnapshot    = errors.New("snapshot payload is invalid")
)

const (
	snapshotMagic  = "gsb.triangle-sort-rebuilder.snapshot"
	snapshotFormat = 1
)

// TriangleSortRebuilder owns one mesh and its current draw order.
//
// All mutable state is guarded by mu. A stored order is an immutable
// snapshot: it is never modified after being installed and every accessor
// hands out an independent copy.
type TriangleSortRebuilder struct {
	mu sync.Mutex

	budget int

	mesh    Mesh
	hasMesh bool
	version uint64

	order    IndexBuffer
	orderVer uint64
	view     Vec3
	viewed   bool

	stats   OrderStats
	degener int
}

// NewTriangleSortRebuilder builds a rebuilder with the given index budget.
// A non-positive budget is retained verbatim so IndexBudget round trips;
// SetIndexBudget rejects such values and every rebuild enforces the cap.
func NewTriangleSortRebuilder(budget int) *TriangleSortRebuilder {
	return &TriangleSortRebuilder{budget: budget}
}

// SetIndexBudget changes how many triangles one order may contain.
// The budget is counted in triangles, not vertices.
func (r *TriangleSortRebuilder) SetIndexBudget(n int) error {
	if n <= 0 {
		return fmt.Errorf("%w: got %d", ErrBadBudget, n)
	}
	r.mu.Lock()
	r.budget = n
	r.mu.Unlock()
	return nil
}

// IndexBudget reports the configured index budget.
func (r *TriangleSortRebuilder) IndexBudget() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.budget
}

// SetMesh validates and replaces the mesh and bumps its version.
// A rejected mesh leaves the previous state untouched.
func (r *TriangleSortRebuilder) SetMesh(mesh Mesh) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := validateMesh(mesh); err != nil {
		return err
	}

	r.mesh = cloneMesh(mesh)
	r.hasMesh = true
	r.version++
	r.resetDerivedLocked()
	return nil
}

// MeshVersion reports the version of the current mesh.
func (r *TriangleSortRebuilder) MeshVersion() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.version
}

// Update replaces the vertex positions of the current mesh. The number of
// supplied vertices must exactly match the mesh. A successful update bumps
// the mesh version and invalidates any order built for the old positions.
func (r *TriangleSortRebuilder) Update(verts []Vertex) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(verts) != len(r.mesh.Vertices) {
		return fmt.Errorf("%w: got %d vertices, mesh requires %d",
			ErrBadVertexCount, len(verts), len(r.mesh.Vertices))
	}
	if !r.hasMesh {
		// Vacuous empty update while no mesh is set; nothing to version.
		return nil
	}

	copy(r.mesh.Vertices, verts)
	r.version++
	r.resetDerivedLocked()
	return nil
}

// Rebuild sorts the triangles for one view direction.
//
// The sort key is the dot product of the triangle centroid with viewDir,
// ascending; equal keys fall back to ascending triangle id. Degenerate
// triangles are counted and omitted from the order. When viewDir is exactly
// the last successful view for the current mesh version, the previous
// immutable order is reused and Reused is reported. Any other direction
// triggers a full, deterministic rebuild.
func (r *TriangleSortRebuilder) Rebuild(viewDir Vec3) (IndexBuffer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.hasMesh {
		return nil, ErrNoMesh
	}
	if isZeroVec(viewDir) {
		return nil, fmt.Errorf("%w: view direction must not be the zero vector", ErrBadViewDir)
	}

	total := len(r.mesh.Tris)
	if total > r.budget {
		// Hard cap: never truncate and never replace the last usable order.
		return nil, fmt.Errorf("%w: mesh has %d triangles but budget is %d",
			ErrBudgetExceeded, total, r.budget)
	}

	if r.order != nil && r.viewed && r.view == viewDir && r.orderVer == r.version {
		r.stats.Reused = true
		return cloneIndexBuffer(r.order), nil
	}

	order, degenerate := sortTriangles(r.mesh, viewDir)

	r.order = order
	r.orderVer = r.version
	r.view = viewDir
	r.viewed = true
	r.degener = degenerate
	r.stats = OrderStats{
		Triangles:  total,
		Degenerate: degenerate,
		Sorted:     len(order),
		Reused:     false,
		Version:    r.version,
	}

	return cloneIndexBuffer(r.order), nil
}

// LastOrder reports the draw order produced by the last successful rebuild.
// The returned buffer is an independent copy.
func (r *TriangleSortRebuilder) LastOrder() (IndexBuffer, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.order == nil {
		return nil, false
	}
	return cloneIndexBuffer(r.order), true
}

// LastView reports the view direction of the last successful rebuild.
func (r *TriangleSortRebuilder) LastView() (Vec3, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.view, r.viewed
}

// OrderStats reports the counters of the last rebuild.
func (r *TriangleSortRebuilder) OrderStats() OrderStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stats
}

// DegenerateCount reports how many degenerate triangles the last rebuild skipped.
func (r *TriangleSortRebuilder) DegenerateCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.degener
}

// Reset drops the mesh, the order and the counters. The configured budget is
// retained because it is setup rather than derived state.
func (r *TriangleSortRebuilder) Reset() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.mesh = Mesh{}
	r.hasMesh = false
	r.version = 0
	r.resetDerivedLocked()
	r.orderVer = 0
	return nil
}

// Snapshot serialises the mesh, its version, the budget, the last view and
// the current order (plus the counters needed to restore the observations).
func (r *TriangleSortRebuilder) Snapshot() ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	data := snapshotData{
		Magic:        snapshotMagic,
		Format:       snapshotFormat,
		Budget:       r.budget,
		Version:      r.version,
		HasMesh:      r.hasMesh,
		Vertices:     r.mesh.Vertices,
		Tris:         r.mesh.Tris,
		HasOrder:     r.order != nil,
		OrderVersion: r.orderVer,
		Order:        r.order,
		Viewed:       r.viewed,
		View:         r.view,
		Degenerate:   r.degener,
		Stats:        r.stats,
	}

	payload, err := json.Marshal(&data)
	if err != nil {
		return nil, err
	}
	return payload, nil
}

// Restore rebuilds the rebuilder from a snapshot, either completely or not at
// all. The payload is fully parsed and validated before any receiver state is
// touched, so a rejected payload preserves the previous state.
func (r *TriangleSortRebuilder) Restore(payload []byte) error {
	var data snapshotData
	if err := json.Unmarshal(payload, &data); err != nil {
		return fmt.Errorf("%w: %v", ErrBadSnapshot, err)
	}
	if err := validateSnapshot(&data); err != nil {
		return err
	}

	mesh := Mesh{}
	if data.HasMesh {
		mesh = Mesh{
			Vertices: append([]Vertex(nil), data.Vertices...),
			Tris:     append([]Tri(nil), data.Tris...),
		}
	}
	var order IndexBuffer
	if data.HasOrder {
		order = make(IndexBuffer, len(data.Order))
		copy(order, data.Order)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.budget = data.Budget
	r.mesh = mesh
	r.hasMesh = data.HasMesh
	r.version = data.Version
	r.order = order
	if data.HasOrder {
		r.orderVer = data.OrderVersion
	} else {
		r.orderVer = 0
	}
	r.viewed = data.Viewed
	r.view = data.View
	r.degener = data.Degenerate
	r.stats = data.Stats
	return nil
}

// resetDerivedLocked clears every value derived from a particular revision of
// the mesh. It does not touch the version counter itself.
func (r *TriangleSortRebuilder) resetDerivedLocked() {
	r.order = nil
	r.orderVer = 0
	r.view = Vec3{}
	r.viewed = false
	r.stats = OrderStats{}
	r.degener = 0
}

// snapshotData is the on-disk representation of a rebuilder.
type snapshotData struct {
	Magic        string      `json:"magic"`
	Format       int         `json:"format"`
	Budget       int         `json:"budget"`
	Version      uint64      `json:"version"`
	HasMesh      bool        `json:"has_mesh"`
	Vertices     []Vertex    `json:"vertices"`
	Tris         []Tri       `json:"tris"`
	HasOrder     bool        `json:"has_order"`
	OrderVersion uint64      `json:"order_version"`
	Order        IndexBuffer `json:"order"`
	Viewed       bool        `json:"viewed"`
	View         Vec3        `json:"view"`
	Degenerate   int         `json:"degenerate"`
	Stats        OrderStats  `json:"stats"`
}

func validateSnapshot(data *snapshotData) error {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrBadSnapshot, fmt.Sprintf(format, args...))
	}

	if data.Magic != snapshotMagic {
		return bad("unrecognised snapshot magic")
	}
	if data.Format != snapshotFormat {
		return bad("unsupported snapshot format %d", data.Format)
	}

	var mesh Mesh
	if data.HasMesh {
		mesh = Mesh{Vertices: data.Vertices, Tris: data.Tris}
		if err := validateMesh(mesh); err != nil {
			return fmt.Errorf("%w: invalid mesh: %v", ErrBadSnapshot, err)
		}
	}

	if data.HasOrder {
		if !data.HasMesh {
			return bad("order present without a mesh")
		}
		if data.Order == nil {
			return bad("order flag set but order data is missing")
		}
		if data.OrderVersion != data.Version {
			return bad("order version %d does not match snapshot version %d",
				data.OrderVersion, data.Version)
		}
		if err := validateOrderForMesh(data.Order, mesh); err != nil {
			return fmt.Errorf("%w: %v", ErrBadSnapshot, err)
		}
	}

	if data.Viewed {
		if !data.HasOrder {
			return bad("view marked as used without a stored order")
		}
		if isZeroVec(data.View) {
			return bad("last view is the zero vector")
		}
	}

	return nil
}

// validateMesh enforces the structural mesh contract: at least one triangle
// and every index inside the vertex table.
func validateMesh(mesh Mesh) error {
	vertexCount := uint64(len(mesh.Vertices))
	if len(mesh.Tris) == 0 {
		return fmt.Errorf("%w: mesh has no triangles", ErrBadMesh)
	}
	if vertexCount == 0 {
		return fmt.Errorf("%w: mesh has no vertices", ErrBadMesh)
	}
	for i, tri := range mesh.Tris {
		if uint64(tri.A) >= vertexCount ||
			uint64(tri.B) >= vertexCount ||
			uint64(tri.C) >= vertexCount {
			return fmt.Errorf("%w: triangle %d references an out-of-range vertex", ErrBadMesh, i)
		}
	}
	return nil
}

// sortItem is one triangle id paired with its sort key.
type sortItem struct {
	id  uint32
	key float64
}

// sortTriangles computes the canonical draw order for mesh under view.
// It is a pure, deterministic function of its inputs. Degenerate triangles
// are counted and excluded; every other triangle is returned exactly once.
func sortTriangles(mesh Mesh, view Vec3) (IndexBuffer, int) {
	total := len(mesh.Tris)
	items := make([]sortItem, 0, total)
	degenerate := 0

	for id, tri := range mesh.Tris {
		a := mesh.Vertices[tri.A].Pos
		b := mesh.Vertices[tri.B].Pos
		c := mesh.Vertices[tri.C].Pos

		if isDegenerateTri(a, b, c) {
			degenerate++
			continue
		}

		centroid := Vec3{
			X: (a.X + b.X + c.X) / 3.0,
			Y: (a.Y + b.Y + c.Y) / 3.0,
			Z: (a.Z + b.Z + c.Z) / 3.0,
		}
		key := centroid.X*view.X + centroid.Y*view.Y + centroid.Z*view.Z
		items = append(items, sortItem{id: uint32(id), key: key})
	}

	// Key ascending, then triangle id ascending. The unique id tie-break
	// makes the result independent of traversal order.
	sort.Slice(items, func(i, j int) bool {
		if items[i].key != items[j].key {
			return items[i].key < items[j].key
		}
		return items[i].id < items[j].id
	})

	order := make(IndexBuffer, len(items))
	for i, item := range items {
		order[i] = item.id
	}
	return order, degenerate
}

// validateOrderForMesh checks that order contains every non-degenerate
// triangle exactly once and nothing else.
func validateOrderForMesh(order IndexBuffer, mesh Mesh) error {
	total := len(mesh.Tris)
	expected := make(map[uint32]struct{}, total)
	for id, tri := range mesh.Tris {
		a := mesh.Vertices[tri.A].Pos
		b := mesh.Vertices[tri.B].Pos
		c := mesh.Vertices[tri.C].Pos
		if !isDegenerateTri(a, b, c) {
			expected[uint32(id)] = struct{}{}
		}
	}

	seen := make(map[uint32]struct{}, len(order))
	for _, id := range order {
		if int(id) < 0 || int(id) >= total {
			return fmt.Errorf("order contains triangle id %d outside [0,%d)", id, total)
		}
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("triangle %d appears more than once", id)
		}
		if _, ok := expected[id]; !ok {
			return fmt.Errorf("triangle %d is degenerate or unexpected in the order", id)
		}
		seen[id] = struct{}{}
	}

	if len(seen) != len(expected) {
		return fmt.Errorf("order covers %d triangles, want %d", len(seen), len(expected))
	}
	return nil
}

// isDegenerateTri reports whether a triangle has zero geometric area, i.e.
// the cross product of two edge vectors is the zero vector.
func isDegenerateTri(a, b, c Vec3) bool {
	abx := b.X - a.X
	aby := b.Y - a.Y
	abz := b.Z - a.Z
	acx := c.X - a.X
	acy := c.Y - a.Y
	acz := c.Z - a.Z

	cx := aby*acz - abz*acy
	cy := abz*acx - abx*acz
	cz := abx*acy - aby*acx
	return cx == 0.0 && cy == 0.0 && cz == 0.0
}

func isZeroVec(v Vec3) bool {
	return v.X == 0.0 && v.Y == 0.0 && v.Z == 0.0
}

func cloneMesh(mesh Mesh) Mesh {
	return Mesh{
		Vertices: append([]Vertex(nil), mesh.Vertices...),
		Tris:     append([]Tri(nil), mesh.Tris...),
	}
}

func cloneIndexBuffer(order IndexBuffer) IndexBuffer {
	if order == nil {
		return nil
	}
	clone := make(IndexBuffer, len(order))
	copy(clone, order)
	return clone
}
