// Package task clips triangles and meshes against one plane.
package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
)

// DroppedIndex marks a vertex that the clip pass removed from a mesh.
const DroppedIndex uint32 = ^uint32(0)

// Hard limits and tuning constants of the clipper.
const (
	MaxVerticesPerMesh  = 65536
	MaxIndicesPerMesh   = 65536
	DefaultVertexBudget = 4096
	MinTriangleArea     = 1e-9

	snapshotVersion = 1
	geometryEpsilon = 1e-12
)

// Errors reported by the clipper.
var (
	ErrNilMesh            = errors.New("mesh is required")
	ErrBadPlane           = errors.New("plane normal must not be zero")
	ErrDegenerateTriangle = errors.New("triangle is degenerate")
	ErrBudgetExceeded     = errors.New("clip budget exceeded")
	ErrBadBudget          = errors.New("vertex budget must be positive")
	ErrIndexOutOfRange    = errors.New("index is out of range")
	ErrIndexLimit         = errors.New("index count is over the limit")
	ErrBadSnapshot        = errors.New("snapshot payload is invalid")
)

// Vec3 is a point or a direction in object space.
type Vec3 struct{ X, Y, Z float64 }

// Vec2 is a texture coordinate.
type Vec2 struct{ U, V float64 }

// Color is a linear vertex colour.
type Color struct{ R, G, B, A float64 }

// Plane is the clipping plane in the form Normal·P + Offset.
type Plane struct {
	Normal Vec3
	Offset float64
}

// Vertex carries every attribute the renderer needs for one vertex.
type Vertex struct {
	Position Vec3
	Normal   Vec3
	UV       Vec2
	Color    Color
	Bones    [4]int
	Weights  [4]float64
}

// Triangle is one triangle of the mesh.
type Triangle struct{ V [3]Vertex }

// Mesh is an indexed triangle list.
type Mesh struct {
	Vertices []Vertex
	Indices  []uint32
}

// Limits bounds the mesh sizes the clipper accepts.
type Limits struct {
	MaxVertices  int
	MaxIndices   int
	VertexBudget int
}

// Stats is the running tally of one clipper instance.
type Stats struct {
	Generated int
	Output    int
	Dropped   int
}

// NearPlaneClipper clips geometry against a plane.
type NearPlaneClipper struct {
	mu        sync.Mutex
	limits    Limits
	generated int
	output    int
	dropped   int
	budget    int
}

type snapshotData struct {
	Version   *int `json:"version"`
	Generated *int `json:"generated"`
	Output    *int `json:"output"`
	Dropped   *int `json:"dropped"`
	Budget    *int `json:"budget"`
}

type clipVertex struct {
	vertex Vertex
	fresh  bool
}

type clippedFace struct {
	vertices  []Vertex
	indices   []uint32
	generated int
	output    int
	dropped   int
}

// NewNearPlaneClipper builds a clipper honouring the given limits.
func NewNearPlaneClipper(limits Limits) *NearPlaneClipper {
	if limits.MaxVertices <= 0 || limits.MaxVertices > MaxVerticesPerMesh {
		limits.MaxVertices = MaxVerticesPerMesh
	}
	if limits.MaxIndices <= 0 || limits.MaxIndices > MaxIndicesPerMesh {
		limits.MaxIndices = MaxIndicesPerMesh
	}
	if limits.VertexBudget <= 0 {
		limits.VertexBudget = DefaultVertexBudget
	}
	return &NearPlaneClipper{limits: limits, budget: limits.VertexBudget}
}

// Add returns the sum of two vectors.
func (v Vec3) Add(o Vec3) Vec3 {
	return Vec3{X: v.X + o.X, Y: v.Y + o.Y, Z: v.Z + o.Z}
}

// Sub returns the difference of two vectors.
func (v Vec3) Sub(o Vec3) Vec3 {
	return Vec3{X: v.X - o.X, Y: v.Y - o.Y, Z: v.Z - o.Z}
}

// Scale multiplies the vector by s.
func (v Vec3) Scale(s float64) Vec3 {
	return Vec3{X: v.X * s, Y: v.Y * s, Z: v.Z * s}
}

// Dot returns the scalar product of two vectors.
func (v Vec3) Dot(o Vec3) float64 {
	return v.X*o.X + v.Y*o.Y + v.Z*o.Z
}

// Cross returns the cross product of two vectors.
func (v Vec3) Cross(o Vec3) Vec3 {
	return Vec3{
		X: v.Y*o.Z - v.Z*o.Y,
		Y: v.Z*o.X - v.X*o.Z,
		Z: v.X*o.Y - v.Y*o.X,
	}
}

// Lerp returns the point at t between v and o.
func (v Vec3) Lerp(o Vec3, t float64) Vec3 {
	return v.Add(o.Sub(v).Scale(t))
}

// ClipTriangle keeps the part of t in front of the plane.
func (c *NearPlaneClipper) ClipTriangle(t Triangle, plane Plane) ([]Triangle, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	face, err := clipTriangleFace(t, plane)
	if err != nil {
		return nil, err
	}
	c.generated += face.generated
	c.output += face.output
	c.dropped += face.dropped

	out := make([]Triangle, 0, len(face.indices)/3)
	for i := 0; i+2 < len(face.indices); i += 3 {
		out = append(out, Triangle{V: [3]Vertex{
			face.vertices[face.indices[i]],
			face.vertices[face.indices[i+1]],
			face.vertices[face.indices[i+2]],
		}})
	}
	return out, nil
}

// ClipMesh clips every triangle of m and renumbers the surviving vertices.
func (c *NearPlaneClipper) ClipMesh(m *Mesh, plane Plane) (*Mesh, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if m == nil {
		return nil, ErrNilMesh
	}
	if isZeroNormal(plane.Normal) {
		return nil, ErrBadPlane
	}
	if len(m.Vertices) > c.limits.MaxVertices {
		return nil, ErrBudgetExceeded
	}
	if len(m.Indices) > c.limits.MaxIndices {
		return nil, ErrIndexLimit
	}
	for _, index := range m.Indices {
		if index >= uint32(len(m.Vertices)) {
			return nil, ErrIndexOutOfRange
		}
	}
	if len(m.Indices)%3 != 0 {
		return nil, errors.New("mesh indices must contain complete triangles")
	}

	draft := &Mesh{Vertices: []Vertex{}, Indices: []uint32{}}
	var generated, outputCount, droppedCount int

	for first := 0; first < len(m.Indices); first += 3 {
		tri := Triangle{V: [3]Vertex{
			m.Vertices[m.Indices[first]],
			m.Vertices[m.Indices[first+1]],
			m.Vertices[m.Indices[first+2]],
		}}
		face, err := clipTriangleFace(tri, plane)
		if err != nil {
			return nil, err
		}

		generated += face.generated
		outputCount += face.output
		droppedCount += face.dropped

		base := uint32(len(draft.Vertices))
		draft.Vertices = append(draft.Vertices, face.vertices...)
		for _, index := range face.indices {
			draft.Indices = append(draft.Indices, base+index)
		}
	}

	if generated > c.budget {
		return nil, ErrBudgetExceeded
	}
	if len(draft.Vertices) > c.limits.MaxVertices {
		return nil, ErrBudgetExceeded
	}
	if len(draft.Indices) > c.limits.MaxIndices {
		return nil, ErrIndexLimit
	}

	c.generated += generated
	c.output += outputCount
	c.dropped += droppedCount
	return draft, nil
}

// ClipCount is the number of vertices created by clipping so far.
func (c *NearPlaneClipper) ClipCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.generated
}

// RemapIndex compacts an index run into first-seen order.
func (c *NearPlaneClipper) RemapIndex(old []uint32) ([]uint32, error) {
	limit := MaxIndicesPerMesh
	if c != nil && c.limits.MaxIndices > 0 && c.limits.MaxIndices < limit {
		limit = c.limits.MaxIndices
	}
	if len(old) > limit {
		return nil, ErrIndexLimit
	}

	seen := make(map[uint32]uint32, len(old))
	out := make([]uint32, len(old))
	var next uint32
	for i, oldIndex := range old {
		newIndex, ok := seen[oldIndex]
		if !ok {
			newIndex = next
			next++
			seen[oldIndex] = newIndex
		}
		out[i] = newIndex
	}
	return out, nil
}

// Budget sets the number of new vertices one ClipMesh call may create.
func (c *NearPlaneClipper) Budget(limit int) error {
	if limit <= 0 {
		return ErrBadBudget
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.budget = limit
	return nil
}

// Snapshot serialises the counters and the budget of the clipper.
func (c *NearPlaneClipper) Snapshot() ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	version := snapshotVersion
	generated := c.generated
	outputCount := c.output
	droppedCount := c.dropped
	budget := c.budget
	return json.Marshal(snapshotData{
		Version:   &version,
		Generated: &generated,
		Output:    &outputCount,
		Dropped:   &droppedCount,
		Budget:    &budget,
	})
}

// Restore replaces the counters and the budget, all or nothing.
func (c *NearPlaneClipper) Restore(data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var snapshot snapshotData
	if len(data) == 0 {
		return fmt.Errorf("%w: empty snapshot", ErrBadSnapshot)
	}
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return fmt.Errorf("%w: %v", ErrBadSnapshot, err)
	}
	if snapshot.Version == nil || *snapshot.Version != snapshotVersion ||
		snapshot.Generated == nil || *snapshot.Generated < 0 ||
		snapshot.Output == nil || *snapshot.Output < 0 ||
		snapshot.Dropped == nil || *snapshot.Dropped < 0 ||
		snapshot.Budget == nil || *snapshot.Budget <= 0 {
		return ErrBadSnapshot
	}

	c.generated = *snapshot.Generated
	c.output = *snapshot.Output
	c.dropped = *snapshot.Dropped
	c.budget = *snapshot.Budget
	return nil
}

// Reset clears the counters and restores the default budget.
func (c *NearPlaneClipper) Reset() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generated = 0
	c.output = 0
	c.dropped = 0
	c.budget = DefaultVertexBudget
	return nil
}

// Stats reports the counters of the clipper.
func (c *NearPlaneClipper) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Stats{Generated: c.generated, Output: c.output, Dropped: c.dropped}
}

// SignedDistance evaluates the plane equation at v.
func (c *NearPlaneClipper) SignedDistance(v Vec3, plane Plane) float64 {
	return plane.Normal.Dot(v) + plane.Offset
}

// PlaneFromPointNormal builds a plane from a point and a direction.
func (c *NearPlaneClipper) PlaneFromPointNormal(point, normal Vec3) Plane {
	length := math.Sqrt(normal.Dot(normal))
	if length == 0 {
		return Plane{}
	}
	unit := normal.Scale(1 / length)
	return Plane{Normal: unit, Offset: -unit.Dot(point)}
}

// Area is the area of one triangle.
func (c *NearPlaneClipper) Area(t Triangle) float64 {
	return triangleArea(t)
}

func clipTriangleFace(t Triangle, plane Plane) (*clippedFace, error) {
	if isZeroNormal(plane.Normal) {
		return nil, ErrBadPlane
	}
	if isDegenerateTriangle(t) {
		return nil, ErrDegenerateTriangle
	}

	distance := [3]float64{
		signedPlaneDistance(t.V[0].Position, plane),
		signedPlaneDistance(t.V[1].Position, plane),
		signedPlaneDistance(t.V[2].Position, plane),
	}

	allInside := true
	for _, d := range distance {
		if d < 0 {
			allInside = false
			break
		}
	}
	if allInside {
		return &clippedFace{
			vertices: []Vertex{t.V[0], t.V[1], t.V[2]},
			indices:  []uint32{0, 1, 2},
			output:   1,
		}, nil
	}

	var candidates []clipVertex
	for i := 0; i < 3; i++ {
		next := (i + 1) % 3
		a, b := t.V[i], t.V[next]
		da, db := distance[i], distance[next]
		insideA, insideB := da >= 0, db >= 0

		switch {
		case insideA && insideB:
			candidates = appendClipVertex(candidates, b, false)
		case insideA && !insideB:
			candidates = appendIntersection(candidates, a, b, da, db, plane)
		case !insideA && insideB:
			candidates = appendIntersection(candidates, a, b, da, db, plane)
			candidates = appendClipVertex(candidates, b, false)
		}
	}

	polygon, generated := mergeClipVertices(candidates)
	face := &clippedFace{vertices: polygon, generated: generated}

	for i := 1; i+1 < len(polygon); i++ {
		fan := Triangle{V: [3]Vertex{polygon[0], polygon[i], polygon[i+1]}}
		if isZeroArea(fan) {
			face.dropped++
			continue
		}
		face.indices = append(face.indices, 0, uint32(i), uint32(i+1))
		face.output++
	}
	if len(polygon) < 3 && face.dropped == 0 {
		face.dropped = 1
	}

	return face.compact(), nil
}

func (f *clippedFace) compact() *clippedFace {
	if len(f.indices) == 0 {
		f.vertices = []Vertex{}
		return f
	}

	remap := make(map[uint32]uint32, len(f.indices))
	vertices := make([]Vertex, 0, len(f.indices))
	indices := make([]uint32, len(f.indices))
	for i, oldIndex := range f.indices {
		newIndex, ok := remap[oldIndex]
		if !ok {
			newIndex = uint32(len(vertices))
			remap[oldIndex] = newIndex
			vertices = append(vertices, f.vertices[oldIndex])
		}
		indices[i] = newIndex
	}
	f.vertices = vertices
	f.indices = indices
	return f
}

func appendIntersection(dst []clipVertex, a, b Vertex, da, db float64, plane Plane) []clipVertex {
	t := da / (da - db)
	if math.IsNaN(t) || math.IsInf(t, 0) {
		t = 0
	}
	t = clampFloat(t, 0, 1)

	fresh := t > geometryEpsilon && t < 1-geometryEpsilon
	v := interpolateVertex(a, b, t)
	if fresh {
		d := signedPlaneDistance(v.Position, plane)
		normalLengthSquared := plane.Normal.Dot(plane.Normal)
		if normalLengthSquared > 0 {
			v.Position = v.Position.Sub(plane.Normal.Scale(d / normalLengthSquared))
		}
	}
	return appendClipVertex(dst, v, fresh)
}

func appendClipVertex(dst []clipVertex, v Vertex, fresh bool) []clipVertex {
	for i := range dst {
		if samePosition(dst[i].vertex.Position, v.Position) {
			if dst[i].fresh && !fresh {
				dst[i].vertex = v
				dst[i].fresh = false
			}
			return dst
		}
	}
	return append(dst, clipVertex{vertex: v, fresh: fresh})
}

func mergeClipVertices(candidates []clipVertex) ([]Vertex, int) {
	vertices := make([]Vertex, len(candidates))
	generated := 0
	for i, candidate := range candidates {
		vertices[i] = candidate.vertex
		if candidate.fresh {
			generated++
		}
	}
	return vertices, generated
}

func interpolateVertex(a, b Vertex, t float64) Vertex {
	if t <= 0 {
		return a
	}
	if t >= 1 {
		return b
	}

	v := Vertex{
		Position: a.Position.Lerp(b.Position, t),
		Normal:   a.Normal.Lerp(b.Normal, t),
		UV: Vec2{
			U: lerpFloat(a.UV.U, b.UV.U, t),
			V: lerpFloat(a.UV.V, b.UV.V, t),
		},
		Color: Color{
			R: lerpFloat(a.Color.R, b.Color.R, t),
			G: lerpFloat(a.Color.G, b.Color.G, t),
			B: lerpFloat(a.Color.B, b.Color.B, t),
			A: lerpFloat(a.Color.A, b.Color.A, t),
		},
	}

	// Bone indices are identifiers. Keep a complete bone/weight pair from one
	// endpoint instead of numerically blending integer indices.
	if t <= 0.5 {
		v.Bones = a.Bones
		v.Weights = a.Weights
	} else {
		v.Bones = b.Bones
		v.Weights = b.Weights
	}
	return v
}

func signedPlaneDistance(p Vec3, plane Plane) float64 {
	return plane.Normal.Dot(p) + plane.Offset
}

func lerpFloat(a, b, t float64) float64 {
	return a + (b-a)*t
}

func clampFloat(v, low, high float64) float64 {
	return math.Max(low, math.Min(high, v))
}

func isZeroNormal(n Vec3) bool {
	return n.X == 0 && n.Y == 0 && n.Z == 0
}

func triangleArea(t Triangle) float64 {
	ab := t.V[1].Position.Sub(t.V[0].Position)
	ac := t.V[2].Position.Sub(t.V[0].Position)
	cross := ab.Cross(ac)
	return 0.5 * math.Sqrt(cross.Dot(cross))
}

func isDegenerateTriangle(t Triangle) bool {
	ab := t.V[1].Position.Sub(t.V[0].Position)
	ac := t.V[2].Position.Sub(t.V[0].Position)
	abLength := math.Sqrt(ab.Dot(ab))
	acLength := math.Sqrt(ac.Dot(ac))
	if abLength == 0 || acLength == 0 {
		return true
	}

	cross := ab.Cross(ac)
	crossLength := math.Sqrt(cross.Dot(cross))
	if crossLength <= geometryEpsilon*abLength*acLength {
		return true
	}
	return triangleArea(t) < MinTriangleArea
}

func isZeroArea(t Triangle) bool {
	ab := t.V[1].Position.Sub(t.V[0].Position)
	ac := t.V[2].Position.Sub(t.V[0].Position)
	abLength := math.Sqrt(ab.Dot(ab))
	acLength := math.Sqrt(ac.Dot(ac))
	if abLength == 0 || acLength == 0 {
		return true
	}

	cross := ab.Cross(ac)
	crossLength := math.Sqrt(cross.Dot(cross))
	return crossLength <= geometryEpsilon*abLength*acLength || triangleArea(t) < MinTriangleArea
}

func samePosition(a, b Vec3) bool {
	scale := 1.0
	for _, value := range []float64{a.X, a.Y, a.Z, b.X, b.Y, b.Z} {
		scale = math.Max(scale, math.Abs(value))
	}
	return math.Abs(a.X-b.X) <= geometryEpsilon*scale &&
		math.Abs(a.Y-b.Y) <= geometryEpsilon*scale &&
		math.Abs(a.Z-b.Z) <= geometryEpsilon*scale
}
