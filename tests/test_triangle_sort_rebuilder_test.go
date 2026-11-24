package task

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	impl "gsb/batch48-go-game-b-09"
)

func flatTri(z float64) []impl.Vertex {
	return []impl.Vertex{
		{Pos: impl.Vec3{X: 0, Y: 0, Z: z}},
		{Pos: impl.Vec3{X: 1, Y: 0, Z: z}},
		{Pos: impl.Vec3{X: 0, Y: 1, Z: z}},
	}
}

// meshOf builds a mesh whose triangle i sits at depth zs[i].
func meshOf(zs ...float64) impl.Mesh {
	var verts []impl.Vertex
	var tris []impl.Tri
	for _, z := range zs {
		base := uint32(len(verts))
		verts = append(verts, flatTri(z)...)
		tris = append(tris, impl.Tri{A: base, B: base + 1, C: base + 2})
	}
	return impl.Mesh{Vertices: verts, Tris: tris}
}

func viewZ() impl.Vec3 {
	return impl.Vec3{X: 0, Y: 0, Z: 1}
}

func TestSetMeshRejectsInvalidMesh(t *testing.T) {
	r := impl.NewTriangleSortRebuilder(16)
	if err := r.SetMesh(impl.Mesh{}); !errors.Is(err, impl.ErrBadMesh) {
		t.Fatalf("empty mesh: got %v, want ErrBadMesh", err)
	}
	badIndex := impl.Mesh{
		Vertices: flatTri(1),
		Tris:     []impl.Tri{{A: 0, B: 1, C: 9}},
	}
	if err := r.SetMesh(badIndex); !errors.Is(err, impl.ErrBadMesh) {
		t.Fatalf("out of range index: got %v, want ErrBadMesh", err)
	}
	noTris := impl.Mesh{Vertices: flatTri(1)}
	if err := r.SetMesh(noTris); !errors.Is(err, impl.ErrBadMesh) {
		t.Fatalf("mesh without triangles: got %v, want ErrBadMesh", err)
	}
	if got := r.MeshVersion(); got != 0 {
		t.Fatalf("MeshVersion after rejected meshes = %d, want 0", got)
	}
}

func TestSetMeshAssignsIncreasingVersion(t *testing.T) {
	r := impl.NewTriangleSortRebuilder(16)
	if err := r.SetMesh(meshOf(1)); err != nil {
		t.Fatalf("first SetMesh returned unexpected error: %v", err)
	}
	if got := r.MeshVersion(); got != 1 {
		t.Fatalf("MeshVersion = %d, want 1", got)
	}
	if err := r.SetMesh(meshOf(1, 2)); err != nil {
		t.Fatalf("second SetMesh returned unexpected error: %v", err)
	}
	if got := r.MeshVersion(); got != 2 {
		t.Fatalf("MeshVersion = %d, want 2", got)
	}
}

func TestRebuildSortsByCentroidProjection(t *testing.T) {
	r := impl.NewTriangleSortRebuilder(8)
	if err := r.SetMesh(meshOf(3, 1, 2)); err != nil {
		t.Fatalf("SetMesh returned unexpected error: %v", err)
	}
	order, err := r.Rebuild(viewZ())
	if err != nil {
		t.Fatalf("Rebuild returned unexpected error: %v", err)
	}
	want := impl.IndexBuffer{1, 2, 0}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v sorted by ascending centroid depth", order, want)
	}
}

func TestRebuildTieBreaksByLowerTriangleID(t *testing.T) {
	r := impl.NewTriangleSortRebuilder(8)
	if err := r.SetMesh(meshOf(2, 2, 1)); err != nil {
		t.Fatalf("SetMesh returned unexpected error: %v", err)
	}
	order, err := r.Rebuild(viewZ())
	if err != nil {
		t.Fatalf("Rebuild returned unexpected error: %v", err)
	}
	want := impl.IndexBuffer{2, 0, 1}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v with equal depths ordered by triangle id", order, want)
	}
}

func TestRebuildRequiresMesh(t *testing.T) {
	r := impl.NewTriangleSortRebuilder(8)
	if _, err := r.Rebuild(viewZ()); !errors.Is(err, impl.ErrNoMesh) {
		t.Fatalf("Rebuild without mesh: got %v, want ErrNoMesh", err)
	}
}

func TestRebuildRejectsZeroViewDirection(t *testing.T) {
	r := impl.NewTriangleSortRebuilder(8)
	if err := r.SetMesh(meshOf(1)); err != nil {
		t.Fatalf("SetMesh returned unexpected error: %v", err)
	}
	if _, err := r.Rebuild(impl.Vec3{}); !errors.Is(err, impl.ErrBadViewDir) {
		t.Fatalf("Rebuild with zero direction: got %v, want ErrBadViewDir", err)
	}
}

func TestSetIndexBudgetValidatesPositive(t *testing.T) {
	r := impl.NewTriangleSortRebuilder(4)
	if got := r.IndexBudget(); got != 4 {
		t.Fatalf("IndexBudget = %d, want the constructor value 4", got)
	}
	if err := r.SetIndexBudget(0); !errors.Is(err, impl.ErrBadBudget) {
		t.Fatalf("SetIndexBudget(0): got %v, want ErrBadBudget", err)
	}
	if err := r.SetIndexBudget(-2); !errors.Is(err, impl.ErrBadBudget) {
		t.Fatalf("SetIndexBudget(-2): got %v, want ErrBadBudget", err)
	}
	if err := r.SetIndexBudget(9); err != nil {
		t.Fatalf("SetIndexBudget(9) returned unexpected error: %v", err)
	}
	if got := r.IndexBudget(); got != 9 {
		t.Fatalf("IndexBudget = %d, want 9", got)
	}
}

func TestRebuildRejectsOverBudgetAndKeepsLastOrder(t *testing.T) {
	r := impl.NewTriangleSortRebuilder(8)
	if err := r.SetMesh(meshOf(3, 1, 2)); err != nil {
		t.Fatalf("SetMesh returned unexpected error: %v", err)
	}
	first, err := r.Rebuild(viewZ())
	if err != nil {
		t.Fatalf("first Rebuild returned unexpected error: %v", err)
	}
	if err := r.SetIndexBudget(2); err != nil {
		t.Fatalf("SetIndexBudget(2) returned unexpected error: %v", err)
	}
	if _, err := r.Rebuild(viewZ()); !errors.Is(err, impl.ErrBudgetExceeded) {
		t.Fatalf("Rebuild over budget: got %v, want ErrBudgetExceeded", err)
	}
	kept, ok := r.LastOrder()
	if !ok {
		t.Fatalf("LastOrder missing after a rejected rebuild")
	}
	if !reflect.DeepEqual(kept, first) {
		t.Fatalf("LastOrder = %v after rejected rebuild, want the previous %v", kept, first)
	}
}

func TestIndexBufferCoversEveryTriangleOnce(t *testing.T) {
	r := impl.NewTriangleSortRebuilder(16)
	if err := r.SetMesh(meshOf(4, 1, 3, 2)); err != nil {
		t.Fatalf("SetMesh returned unexpected error: %v", err)
	}
	order, err := r.Rebuild(viewZ())
	if err != nil {
		t.Fatalf("Rebuild returned unexpected error: %v", err)
	}
	if len(order) != 4 {
		t.Fatalf("order holds %d triangles, want 4", len(order))
	}
	seen := make(map[uint32]int, 4)
	for _, id := range order {
		seen[id]++
	}
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("triangle %d appears %d times in the order", id, count)
		}
	}
	for id := uint32(0); id < 4; id++ {
		if seen[id] != 1 {
			t.Fatalf("triangle %d is missing from the order", id)
		}
	}
}

func TestOrderStatsReportTriangleCounts(t *testing.T) {
	r := impl.NewTriangleSortRebuilder(8)
	if err := r.SetMesh(meshOf(3, 1, 2)); err != nil {
		t.Fatalf("SetMesh returned unexpected error: %v", err)
	}
	if _, err := r.Rebuild(viewZ()); err != nil {
		t.Fatalf("Rebuild returned unexpected error: %v", err)
	}
	stats := r.OrderStats()
	if stats.Triangles != 3 {
		t.Fatalf("stats.Triangles = %d, want 3", stats.Triangles)
	}
	if stats.Sorted != 3 {
		t.Fatalf("stats.Sorted = %d, want 3", stats.Sorted)
	}
	if stats.Version != 1 {
		t.Fatalf("stats.Version = %d, want 1", stats.Version)
	}
	if stats.Reused {
		t.Fatalf("stats.Reused = true on the first rebuild")
	}
	if got := r.DegenerateCount(); got != 0 {
		t.Fatalf("DegenerateCount = %d on a clean mesh, want 0", got)
	}
}

func TestRebuildWithSameViewReusesOrder(t *testing.T) {
	r := impl.NewTriangleSortRebuilder(8)
	if err := r.SetMesh(meshOf(3, 1, 2)); err != nil {
		t.Fatalf("SetMesh returned unexpected error: %v", err)
	}
	first, err := r.Rebuild(viewZ())
	if err != nil {
		t.Fatalf("first Rebuild returned unexpected error: %v", err)
	}
	second, err := r.Rebuild(viewZ())
	if err != nil {
		t.Fatalf("second Rebuild returned unexpected error: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("second order = %v, want the first %v", second, first)
	}
	if !r.OrderStats().Reused {
		t.Fatalf("stats.Reused = false for an unchanged view direction")
	}
}

func TestSnapshotRestoreKeepsOrderAndVersion(t *testing.T) {
	r := impl.NewTriangleSortRebuilder(8)
	if err := r.SetMesh(meshOf(3, 1, 2)); err != nil {
		t.Fatalf("SetMesh returned unexpected error: %v", err)
	}
	order, err := r.Rebuild(viewZ())
	if err != nil {
		t.Fatalf("Rebuild returned unexpected error: %v", err)
	}
	payload, err := r.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot returned unexpected error: %v", err)
	}
	fresh := impl.NewTriangleSortRebuilder(1)
	if err := fresh.Restore(payload); err != nil {
		t.Fatalf("Restore returned unexpected error: %v", err)
	}
	if got := fresh.MeshVersion(); got != 1 {
		t.Fatalf("MeshVersion after Restore = %d, want 1", got)
	}
	restored, ok := fresh.LastOrder()
	if !ok {
		t.Fatalf("LastOrder missing after Restore")
	}
	if !reflect.DeepEqual(restored, order) {
		t.Fatalf("LastOrder after Restore = %v, want %v", restored, order)
	}
}

func TestRestoreRejectsCorruptPayload(t *testing.T) {
	r := impl.NewTriangleSortRebuilder(8)
	if err := r.SetMesh(meshOf(1)); err != nil {
		t.Fatalf("SetMesh returned unexpected error: %v", err)
	}
	if err := r.Restore([]byte("not a snapshot")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("Restore(garbage): got %v, want ErrBadSnapshot", err)
	}
	if got := r.MeshVersion(); got != 1 {
		t.Fatalf("MeshVersion after rejected Restore = %d, want 1", got)
	}
}

func TestConcurrentRebuildsAgreeOnOrder(t *testing.T) {
	r := impl.NewTriangleSortRebuilder(8)
	if err := r.SetMesh(meshOf(3, 1, 2)); err != nil {
		t.Fatalf("SetMesh returned unexpected error: %v", err)
	}
	want, err := r.Rebuild(viewZ())
	if err != nil {
		t.Fatalf("first Rebuild returned unexpected error: %v", err)
	}
	const workers = 8
	results := make([]impl.IndexBuffer, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = r.Rebuild(viewZ())
		}(i)
	}
	wg.Wait()
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("concurrent Rebuild %d returned error: %v", i, errs[i])
		}
		if !reflect.DeepEqual(results[i], want) {
			t.Fatalf("concurrent Rebuild %d = %v, want %v", i, results[i], want)
		}
	}
}

func TestUpdateRejectsWrongVertexCount(t *testing.T) {
	r := impl.NewTriangleSortRebuilder(8)
	if err := r.SetMesh(meshOf(1)); err != nil {
		t.Fatalf("SetMesh returned unexpected error: %v", err)
	}
	if err := r.Update(flatTri(1)[:2]); !errors.Is(err, impl.ErrBadVertexCount) {
		t.Fatalf("Update with 2 vertices: got %v, want ErrBadVertexCount", err)
	}
	if err := r.Update(flatTri(1)); err != nil {
		t.Fatalf("Update with a matching vertex count returned unexpected error: %v", err)
	}
	order, err := r.Rebuild(viewZ())
	if err != nil {
		t.Fatalf("Rebuild after Update returned unexpected error: %v", err)
	}
	if !reflect.DeepEqual(order, impl.IndexBuffer{0}) {
		t.Fatalf("order after Update = %v, want [0]", order)
	}
}
