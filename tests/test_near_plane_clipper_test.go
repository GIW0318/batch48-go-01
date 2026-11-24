package task

import (
	"errors"
	"sync"
	"testing"

	impl "gsb/batch48-go-game-b-01"
)

func floatNear(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-9
}

func vecNear(a, b impl.Vec3) bool {
	return floatNear(a.X, b.X) && floatNear(a.Y, b.Y) && floatNear(a.Z, b.Z)
}

func mustVec(t *testing.T, got, want impl.Vec3, label string) {
	t.Helper()
	if !vecNear(got, want) {
		t.Fatalf("%s = (%v,%v,%v), want (%v,%v,%v)", label, got.X, got.Y, got.Z, want.X, want.Y, want.Z)
	}
}

// zPlane clips against z >= 0.
func zPlane() impl.Plane {
	return impl.Plane{Normal: impl.Vec3{Z: 1}}
}

// crossingTriangle has one vertex behind the plane and two in front of it.
func crossingTriangle() impl.Triangle {
	return impl.Triangle{V: [3]impl.Vertex{
		{Position: impl.Vec3{X: 0, Y: 0, Z: -1}, Normal: impl.Vec3{Y: 1}, UV: impl.Vec2{U: 0, V: 0}, Color: impl.Color{R: 1, G: 0, B: 0, A: 1}},
		{Position: impl.Vec3{X: 2, Y: 0, Z: 1}, Normal: impl.Vec3{Z: 1}, UV: impl.Vec2{U: 1, V: 0}, Color: impl.Color{R: 0, G: 1, B: 0, A: 1}},
		{Position: impl.Vec3{X: 0, Y: 2, Z: 1}, Normal: impl.Vec3{X: 1}, UV: impl.Vec2{U: 0, V: 1}, Color: impl.Color{R: 0, G: 0, B: 1, A: 1}},
	}}
}

// twoTriangleMesh holds two triangles that each cross the plane.
func twoTriangleMesh() *impl.Mesh {
	m := &impl.Mesh{}
	for _, offset := range []float64{0, 10} {
		base := uint32(len(m.Vertices))
		m.Vertices = append(m.Vertices,
			impl.Vertex{Position: impl.Vec3{X: offset, Y: 0, Z: -1}},
			impl.Vertex{Position: impl.Vec3{X: offset + 2, Y: 0, Z: 1}},
			impl.Vertex{Position: impl.Vec3{X: offset, Y: 2, Z: 1}},
		)
		m.Indices = append(m.Indices, base, base+1, base+2)
	}
	return m
}

func TestClipTriangleKeepsVisibleTriangleUnchanged(t *testing.T) {
	c := impl.NewNearPlaneClipper(impl.Limits{})
	tri := impl.Triangle{V: [3]impl.Vertex{
		{Position: impl.Vec3{X: 0, Z: 1}},
		{Position: impl.Vec3{X: 1, Z: 2}},
		{Position: impl.Vec3{Y: 1, Z: 3}},
	}}
	out, err := c.ClipTriangle(tri, zPlane())
	if err != nil {
		t.Fatalf("clipping a fully visible triangle must succeed, got %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("fully visible triangle produced %d triangles, want 1", len(out))
	}
	for i := 0; i < 3; i++ {
		mustVec(t, out[0].V[i].Position, tri.V[i].Position, "visible vertex position")
	}
	if c.ClipCount() != 0 {
		t.Fatalf("a fully visible triangle must not create vertices, count = %d", c.ClipCount())
	}
	if st := c.Stats(); st.Generated != 0 || st.Output != 1 || st.Dropped != 0 {
		t.Fatalf("stats = %+v, want 0 generated, 1 output, 0 dropped", st)
	}
}

func TestClipTriangleSplitsCrossingTriangleIntoFan(t *testing.T) {
	c := impl.NewNearPlaneClipper(impl.Limits{})
	out, err := c.ClipTriangle(crossingTriangle(), zPlane())
	if err != nil {
		t.Fatalf("clipping a crossing triangle must succeed, got %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("a crossing triangle produced %d triangles, want 2", len(out))
	}
	mustVec(t, out[0].V[0].Position, impl.Vec3{X: 1}, "first fan vertex")
	mustVec(t, out[0].V[1].Position, impl.Vec3{X: 2, Z: 1}, "second fan vertex")
	mustVec(t, out[0].V[2].Position, impl.Vec3{Y: 2, Z: 1}, "third fan vertex")
	mustVec(t, out[1].V[0].Position, impl.Vec3{X: 1}, "second triangle start")
	mustVec(t, out[1].V[1].Position, impl.Vec3{Y: 2, Z: 1}, "second triangle middle")
	mustVec(t, out[1].V[2].Position, impl.Vec3{Y: 1}, "second triangle end")
	if c.ClipCount() != 2 {
		t.Fatalf("clip count = %d, want the 2 vertices created by the split", c.ClipCount())
	}
}

func TestClipInterpolatesEveryVertexAttribute(t *testing.T) {
	c := impl.NewNearPlaneClipper(impl.Limits{})
	out, err := c.ClipTriangle(crossingTriangle(), zPlane())
	if err != nil {
		t.Fatalf("clipping a crossing triangle must succeed, got %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("a crossing triangle produced %d triangles, want 2", len(out))
	}
	first := out[0].V[0]
	mustVec(t, first.Normal, impl.Vec3{Y: 0.5, Z: 0.5}, "interpolated normal")
	if !floatNear(first.UV.U, 0.5) || !floatNear(first.UV.V, 0) {
		t.Fatalf("interpolated uv = (%v,%v), want (0.5,0)", first.UV.U, first.UV.V)
	}
	if !floatNear(first.Color.R, 0.5) || !floatNear(first.Color.G, 0.5) || !floatNear(first.Color.B, 0) || !floatNear(first.Color.A, 1) {
		t.Fatalf("interpolated colour = %+v, want (0.5,0.5,0,1)", first.Color)
	}
	last := out[1].V[2]
	mustVec(t, last.Normal, impl.Vec3{X: 0.5, Y: 0.5}, "closing normal")
	if !floatNear(last.UV.U, 0) || !floatNear(last.UV.V, 0.5) {
		t.Fatalf("closing uv = (%v,%v), want (0,0.5)", last.UV.U, last.UV.V)
	}
	if !floatNear(last.Color.R, 0.5) || !floatNear(last.Color.G, 0) || !floatNear(last.Color.B, 0.5) {
		t.Fatalf("closing colour = %+v, want (0.5,0,0.5,1)", last.Color)
	}
}

func TestClipTriangleRejectsDegenerateInput(t *testing.T) {
	c := impl.NewNearPlaneClipper(impl.Limits{})
	coincident := impl.Triangle{V: [3]impl.Vertex{
		{Position: impl.Vec3{Z: 1}},
		{Position: impl.Vec3{Z: 1}},
		{Position: impl.Vec3{Z: 1}},
	}}
	if _, err := c.ClipTriangle(coincident, zPlane()); !errors.Is(err, impl.ErrDegenerateTriangle) {
		t.Fatalf("coincident vertices must report ErrDegenerateTriangle, got %v", err)
	}
	collinear := impl.Triangle{V: [3]impl.Vertex{
		{Position: impl.Vec3{Z: 1}},
		{Position: impl.Vec3{X: 1, Z: 1}},
		{Position: impl.Vec3{X: 2, Z: 1}},
	}}
	if _, err := c.ClipTriangle(collinear, zPlane()); !errors.Is(err, impl.ErrDegenerateTriangle) {
		t.Fatalf("collinear vertices must report ErrDegenerateTriangle, got %v", err)
	}
	if c.ClipCount() != 0 {
		t.Fatalf("rejected triangles must not change the count, got %d", c.ClipCount())
	}
}

func TestClipDropsZeroAreaOutputPeacefully(t *testing.T) {
	c := impl.NewNearPlaneClipper(impl.Limits{})
	touching := impl.Triangle{V: [3]impl.Vertex{
		{Position: impl.Vec3{}},
		{Position: impl.Vec3{X: 1}},
		{Position: impl.Vec3{Y: 1, Z: -1}},
	}}
	out, err := c.ClipTriangle(touching, zPlane())
	if err != nil {
		t.Fatalf("a triangle that only touches the plane must not raise, got %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("the touching triangle left %d triangles, want 0", len(out))
	}
}

func TestClipMeshStopsWhenVertexBudgetIsExceeded(t *testing.T) {
	c := impl.NewNearPlaneClipper(impl.Limits{})
	if err := c.Budget(3); err != nil {
		t.Fatalf("setting a positive budget must succeed, got %v", err)
	}
	m, err := c.ClipMesh(twoTriangleMesh(), zPlane())
	if !errors.Is(err, impl.ErrBudgetExceeded) {
		t.Fatalf("clipping over budget must report ErrBudgetExceeded, got %v", err)
	}
	if m != nil {
		t.Fatalf("a budget failure must not return a partial mesh, got %d vertices", len(m.Vertices))
	}
	if c.ClipCount() != 0 {
		t.Fatalf("a budget failure must not consume the counter, got %d", c.ClipCount())
	}
	if err := c.Budget(4); err != nil {
		t.Fatalf("raising the budget must succeed, got %v", err)
	}
	full, err := c.ClipMesh(twoTriangleMesh(), zPlane())
	if err != nil {
		t.Fatalf("clipping at the exact budget must succeed, got %v", err)
	}
	if len(full.Vertices) != 8 || len(full.Indices) != 12 {
		t.Fatalf("clipped mesh = %d vertices / %d indices, want 8 / 12", len(full.Vertices), len(full.Indices))
	}
	if c.ClipCount() != 4 {
		t.Fatalf("clip count = %d, want the 4 vertices created by both triangles", c.ClipCount())
	}
}

func TestClipMeshRenumbersIndicesDensely(t *testing.T) {
	c := impl.NewNearPlaneClipper(impl.Limits{})
	out, err := c.ClipMesh(twoTriangleMesh(), zPlane())
	if err != nil {
		t.Fatalf("clipping the mesh must succeed, got %v", err)
	}
	if len(out.Indices) != 12 {
		t.Fatalf("clipped mesh has %d indices, want 12 for four triangles", len(out.Indices))
	}
	if len(out.Vertices) != 8 {
		t.Fatalf("clipped mesh has %d vertices, want 8", len(out.Vertices))
	}
	seen := make([]bool, len(out.Vertices))
	for i, idx := range out.Indices {
		if int(idx) >= len(out.Vertices) {
			t.Fatalf("index %d at position %d points past %d vertices", idx, i, len(out.Vertices))
		}
		seen[idx] = true
	}
	for i, used := range seen {
		if !used {
			t.Fatalf("vertex %d is not referenced, indices must be renumbered densely", i)
		}
	}
	for i, v := range out.Vertices {
		if v.Position.Z < -1e-9 {
			t.Fatalf("vertex %d sits behind the plane at z=%v", i, v.Position.Z)
		}
	}
}

func TestRemapIndexCompactsIntoFirstSeenOrder(t *testing.T) {
	c := impl.NewNearPlaneClipper(impl.Limits{})
	out, err := c.RemapIndex([]uint32{7, 3, 7, 9, 3})
	if err != nil {
		t.Fatalf("remapping a valid index run must succeed, got %v", err)
	}
	want := []uint32{0, 1, 0, 2, 1}
	if len(out) != len(want) {
		t.Fatalf("remapped run = %v, want %v", out, want)
	}
	for i := range want {
		if out[i] != want[i] {
			t.Fatalf("remapped run = %v, want %v", out, want)
		}
	}
	empty, err := c.RemapIndex(nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("an empty run must map to an empty run, got %v err %v", empty, err)
	}
	tooLong := make([]uint32, impl.MaxIndicesPerMesh+1)
	if _, err := c.RemapIndex(tooLong); !errors.Is(err, impl.ErrIndexLimit) {
		t.Fatalf("an over-long run must report ErrIndexLimit, got %v", err)
	}
}

func TestClipCountAccumulatesAndResetClearsIt(t *testing.T) {
	c := impl.NewNearPlaneClipper(impl.Limits{})
	for i := 0; i < 2; i++ {
		if _, err := c.ClipTriangle(crossingTriangle(), zPlane()); err != nil {
			t.Fatalf("clip %d must succeed, got %v", i, err)
		}
	}
	if c.ClipCount() != 4 {
		t.Fatalf("clip count after two splits = %d, want 4", c.ClipCount())
	}
	if st := c.Stats(); st.Generated != 4 || st.Output != 4 || st.Dropped != 0 {
		t.Fatalf("stats = %+v, want 4 generated, 4 output, 0 dropped", st)
	}
	if err := c.Reset(); err != nil {
		t.Fatalf("reset must succeed, got %v", err)
	}
	if c.ClipCount() != 0 {
		t.Fatalf("clip count after reset = %d, want 0", c.ClipCount())
	}
	if st := c.Stats(); st != (impl.Stats{}) {
		t.Fatalf("stats after reset = %+v, want the zero value", st)
	}
}

func TestBudgetRejectsNonPositiveLimits(t *testing.T) {
	c := impl.NewNearPlaneClipper(impl.Limits{})
	if err := c.Budget(0); !errors.Is(err, impl.ErrBadBudget) {
		t.Fatalf("a zero budget must report ErrBadBudget, got %v", err)
	}
	if err := c.Budget(-5); !errors.Is(err, impl.ErrBadBudget) {
		t.Fatalf("a negative budget must report ErrBadBudget, got %v", err)
	}
	if err := c.Budget(1); err != nil {
		t.Fatalf("a positive budget must be accepted, got %v", err)
	}
	if _, err := c.ClipMesh(twoTriangleMesh(), zPlane()); !errors.Is(err, impl.ErrBudgetExceeded) {
		t.Fatalf("the accepted budget must be enforced, got %v", err)
	}
}

func TestClipMeshValidatesItsInput(t *testing.T) {
	c := impl.NewNearPlaneClipper(impl.Limits{})
	if _, err := c.ClipMesh(nil, zPlane()); !errors.Is(err, impl.ErrNilMesh) {
		t.Fatalf("a nil mesh must report ErrNilMesh, got %v", err)
	}
	empty := &impl.Mesh{}
	out, err := c.ClipMesh(empty, zPlane())
	if err != nil {
		t.Fatalf("an empty mesh must clip cleanly, got %v", err)
	}
	if out == nil || len(out.Vertices) != 0 || len(out.Indices) != 0 {
		t.Fatalf("an empty mesh must come back empty, got %+v", out)
	}
	if _, err := c.ClipMesh(twoTriangleMesh(), impl.Plane{}); !errors.Is(err, impl.ErrBadPlane) {
		t.Fatalf("a zero normal must report ErrBadPlane, got %v", err)
	}
	broken := &impl.Mesh{
		Vertices: []impl.Vertex{{Position: impl.Vec3{Z: 1}}},
		Indices:  []uint32{0, 1, 2},
	}
	if _, err := c.ClipMesh(broken, zPlane()); !errors.Is(err, impl.ErrIndexOutOfRange) {
		t.Fatalf("an out of range index must report ErrIndexOutOfRange, got %v", err)
	}
}

func TestClipMeshLeavesStateIntactWhenOneTriangleIsInvalid(t *testing.T) {
	c := impl.NewNearPlaneClipper(impl.Limits{})
	bad := twoTriangleMesh()
	bad.Indices = append(bad.Indices, 90)
	if _, err := c.ClipMesh(bad, zPlane()); !errors.Is(err, impl.ErrIndexOutOfRange) {
		t.Fatalf("the bad index must report ErrIndexOutOfRange, got %v", err)
	}
	if c.ClipCount() != 0 {
		t.Fatalf("a rejected mesh must not consume the counter, got %d", c.ClipCount())
	}
	if st := c.Stats(); st != (impl.Stats{}) {
		t.Fatalf("a rejected mesh must not change the stats, got %+v", st)
	}
}

func TestConcurrentClipMeshKeepsEveryResultIsolated(t *testing.T) {
	c := impl.NewNearPlaneClipper(impl.Limits{})
	const workers = 8
	var wg sync.WaitGroup
	results := make([][]int, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			out, err := c.ClipMesh(twoTriangleMesh(), zPlane())
			if err != nil {
				results[slot] = []int{-1}
				return
			}
			results[slot] = []int{len(out.Vertices), len(out.Indices)}
		}(i)
	}
	wg.Wait()
	for slot, got := range results {
		if len(got) != 2 {
			t.Fatalf("worker %d failed to clip: %v", slot, got)
		}
		if got[0] != 8 || got[1] != 12 {
			t.Fatalf("worker %d produced %d vertices / %d indices, want 8 / 12", slot, got[0], got[1])
		}
	}
	if c.ClipCount() != workers*4 {
		t.Fatalf("clip count = %d, want %d vertices from %d workers", c.ClipCount(), workers*4, workers)
	}
}

func TestSnapshotRestoreKeepsCountersAndBudget(t *testing.T) {
	c := impl.NewNearPlaneClipper(impl.Limits{})
	if _, err := c.ClipTriangle(crossingTriangle(), zPlane()); err != nil {
		t.Fatalf("clip must succeed, got %v", err)
	}
	if err := c.Budget(3); err != nil {
		t.Fatalf("setting the budget must succeed, got %v", err)
	}
	data, err := c.Snapshot()
	if err != nil {
		t.Fatalf("snapshot must succeed, got %v", err)
	}
	restored := impl.NewNearPlaneClipper(impl.Limits{})
	if err := restored.Restore(data); err != nil {
		t.Fatalf("restore must succeed, got %v", err)
	}
	if restored.ClipCount() != 2 {
		t.Fatalf("restored clip count = %d, want 2", restored.ClipCount())
	}
	if _, err := restored.ClipMesh(twoTriangleMesh(), zPlane()); !errors.Is(err, impl.ErrBudgetExceeded) {
		t.Fatalf("the restored budget must still be 3, got %v", err)
	}
	if err := restored.Restore([]byte("{")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("a broken snapshot must report ErrBadSnapshot, got %v", err)
	}
	if restored.ClipCount() != 2 {
		t.Fatalf("a rejected snapshot must leave the state alone, count = %d", restored.ClipCount())
	}
}

func TestGeometryHelpersReportExactValues(t *testing.T) {
	c := impl.NewNearPlaneClipper(impl.Limits{})
	if got := c.SignedDistance(impl.Vec3{Z: -2}, zPlane()); !floatNear(got, -2) {
		t.Fatalf("signed distance = %v, want -2", got)
	}
	if got := c.SignedDistance(impl.Vec3{Z: 3}, zPlane()); !floatNear(got, 3) {
		t.Fatalf("signed distance = %v, want 3", got)
	}
	tri := impl.Triangle{V: [3]impl.Vertex{
		{Position: impl.Vec3{Z: 1}},
		{Position: impl.Vec3{X: 1, Z: 1}},
		{Position: impl.Vec3{Y: 1, Z: 1}},
	}}
	if got := c.Area(tri); !floatNear(got, 0.5) {
		t.Fatalf("area = %v, want 0.5", got)
	}
	plane := c.PlaneFromPointNormal(impl.Vec3{Z: 5}, impl.Vec3{Z: 2})
	mustVec(t, plane.Normal, impl.Vec3{Z: 1}, "normalised plane normal")
	if !floatNear(plane.Offset, -5) {
		t.Fatalf("plane offset = %v, want -5", plane.Offset)
	}
}
