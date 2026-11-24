package task

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	impl "gsb/batch48-go-game-b-07"
)

func point(x, y, z float64) impl.Vec3 {
	return impl.Vec3{X: x, Y: y, Z: z}
}

func cube(at impl.Vec3, half float64) impl.Box {
	return impl.Box{
		Min: impl.Vec3{X: at.X - half, Y: at.Y - half, Z: at.Z - half},
		Max: impl.Vec3{X: at.X + half, Y: at.Y + half, Z: at.Z + half},
	}
}

func instance(id string, at impl.Vec3) impl.Instance {
	return impl.Instance{ID: id, Bounds: cube(at, 0.5)}
}

func newBuilder(t *testing.T) *impl.HLODClusterBuilder {
	t.Helper()
	b := impl.NewHLODClusterBuilder(100)
	if err := b.SetLimits(4, 4); err != nil {
		t.Fatalf("SetLimits(4,4) returned unexpected error: %v", err)
	}
	return b
}

func TestAddRejectsDuplicateAndBlankID(t *testing.T) {
	b := newBuilder(t)
	if _, err := b.Add(instance("rock", point(1, 1, 1))); err != nil {
		t.Fatalf("first Add returned unexpected error: %v", err)
	}
	if _, err := b.Add(instance("rock", point(2, 2, 2))); !errors.Is(err, impl.ErrDuplicateInstance) {
		t.Fatalf("duplicate Add: got %v, want ErrDuplicateInstance", err)
	}
	if _, err := b.Add(impl.Instance{ID: "", Bounds: cube(point(3, 3, 3), 0.5)}); !errors.Is(err, impl.ErrBadInstance) {
		t.Fatalf("blank id Add: got %v, want ErrBadInstance", err)
	}
	if got := b.InstanceCount(); got != 1 {
		t.Fatalf("InstanceCount = %d, want 1", got)
	}
}

func TestAddRejectsInvertedBounds(t *testing.T) {
	b := newBuilder(t)
	bad := impl.Instance{
		ID:     "wall",
		Bounds: impl.Box{Min: point(5, 0, 0), Max: point(4, 1, 1)},
	}
	if _, err := b.Add(bad); !errors.Is(err, impl.ErrBadBounds) {
		t.Fatalf("inverted bounds Add: got %v, want ErrBadBounds", err)
	}
	if got := b.InstanceCount(); got != 0 {
		t.Fatalf("InstanceCount = %d after rejected add, want 0", got)
	}
}

func TestAddEnforcesInstanceLimit(t *testing.T) {
	b := newBuilder(t)
	if err := b.SetInstanceLimit(2); err != nil {
		t.Fatalf("SetInstanceLimit(2) returned unexpected error: %v", err)
	}
	if _, err := b.Add(instance("a", point(1, 1, 1))); err != nil {
		t.Fatalf("Add(a) returned unexpected error: %v", err)
	}
	if _, err := b.Add(instance("b", point(2, 2, 2))); err != nil {
		t.Fatalf("Add(b) returned unexpected error: %v", err)
	}
	if _, err := b.Add(instance("c", point(3, 3, 3))); !errors.Is(err, impl.ErrInstanceLimit) {
		t.Fatalf("third Add: got %v, want ErrInstanceLimit", err)
	}
	if got := b.InstanceCount(); got != 2 {
		t.Fatalf("InstanceCount = %d, want 2 after rejected add", got)
	}
}

func TestSetLimitsValidatesAndReports(t *testing.T) {
	b := impl.NewHLODClusterBuilder(100)
	if err := b.SetLimits(0, 4); !errors.Is(err, impl.ErrBadLimit) {
		t.Fatalf("SetLimits(0,4): got %v, want ErrBadLimit", err)
	}
	if err := b.SetLimits(4, -1); !errors.Is(err, impl.ErrBadLimit) {
		t.Fatalf("SetLimits(4,-1): got %v, want ErrBadLimit", err)
	}
	if err := b.SetLimits(3, 2); err != nil {
		t.Fatalf("SetLimits(3,2) returned unexpected error: %v", err)
	}
	per, depth := b.Limits()
	if per != 3 || depth != 2 {
		t.Fatalf("Limits() = %d,%d, want 3,2", per, depth)
	}
	if err := b.SetInstanceLimit(0); !errors.Is(err, impl.ErrBadLimit) {
		t.Fatalf("SetInstanceLimit(0): got %v, want ErrBadLimit", err)
	}
}

func TestSetCellSizeValidates(t *testing.T) {
	b := impl.NewHLODClusterBuilder(100)
	if err := b.SetCellSize(0); !errors.Is(err, impl.ErrBadCellSize) {
		t.Fatalf("SetCellSize(0): got %v, want ErrBadCellSize", err)
	}
	if err := b.SetCellSize(-4); !errors.Is(err, impl.ErrBadCellSize) {
		t.Fatalf("SetCellSize(-4): got %v, want ErrBadCellSize", err)
	}
	if err := b.SetCellSize(32); err != nil {
		t.Fatalf("SetCellSize(32) returned unexpected error: %v", err)
	}
	if got := b.CellSize(); got != 32 {
		t.Fatalf("CellSize = %v, want 32", got)
	}
}

func TestRebuildRequiresLimits(t *testing.T) {
	b := impl.NewHLODClusterBuilder(100)
	if _, err := b.Add(instance("a", point(1, 1, 1))); err != nil {
		t.Fatalf("Add(a) returned unexpected error: %v", err)
	}
	if _, err := b.Rebuild(); !errors.Is(err, impl.ErrBadLimit) {
		t.Fatalf("Rebuild without limits: got %v, want ErrBadLimit", err)
	}
	if err := b.SetLimits(4, 3); err != nil {
		t.Fatalf("SetLimits returned unexpected error: %v", err)
	}
	tree, err := b.Rebuild()
	if err != nil {
		t.Fatalf("Rebuild after SetLimits returned unexpected error: %v", err)
	}
	if tree.Root == "" {
		t.Fatalf("tree root is empty after a successful Rebuild")
	}
}

func TestRebuildGroupsCoLocatedInstancesIntoOneLeaf(t *testing.T) {
	b := newBuilder(t)
	for i, id := range []string{"a", "b", "c"} {
		if _, err := b.Add(instance(id, point(float64(i)+1, 1, 1))); err != nil {
			t.Fatalf("Add(%s) returned unexpected error: %v", id, err)
		}
	}
	tree, err := b.Rebuild()
	if err != nil {
		t.Fatalf("Rebuild returned unexpected error: %v", err)
	}
	if tree.Leaves != 1 {
		t.Fatalf("tree.Leaves = %d, want 1 for one cell", tree.Leaves)
	}
	if tree.Depth != 0 {
		t.Fatalf("tree.Depth = %d, want 0 for a single leaf", tree.Depth)
	}
	if len(tree.Nodes) != 1 {
		t.Fatalf("tree carries %d nodes, want 1", len(tree.Nodes))
	}
	node := tree.Nodes[0]
	if len(node.Instances) != 3 {
		t.Fatalf("leaf holds %d instances, want 3", len(node.Instances))
	}
	if node.ID != tree.Root {
		t.Fatalf("leaf id %q differs from tree root %q for a single leaf", node.ID, tree.Root)
	}
}

func TestRebuildSplitsClusterOverMaxSize(t *testing.T) {
	b := impl.NewHLODClusterBuilder(100)
	if err := b.SetLimits(2, 6); err != nil {
		t.Fatalf("SetLimits(2,6) returned unexpected error: %v", err)
	}
	ids := []string{"i0", "i1", "i2", "i3", "i4"}
	for i, id := range ids {
		if _, err := b.Add(instance(id, point(float64(i)+1, 0, 0))); err != nil {
			t.Fatalf("Add(%s) returned unexpected error: %v", id, err)
		}
	}
	tree, err := b.Rebuild()
	if err != nil {
		t.Fatalf("Rebuild returned unexpected error: %v", err)
	}
	if tree.Leaves < 3 {
		t.Fatalf("tree.Leaves = %d, want at least 3 leaves for 5 instances with max 2", tree.Leaves)
	}
	total := 0
	for _, node := range tree.Nodes {
		if len(node.Instances) > 2 {
			t.Fatalf("leaf %q holds %d instances, limit is 2", node.ID, len(node.Instances))
		}
		total += len(node.Instances)
	}
	if total != len(ids) {
		t.Fatalf("tree covers %d instances, want %d", total, len(ids))
	}
}

func TestRebuildRespectsMaxDepth(t *testing.T) {
	b := impl.NewHLODClusterBuilder(100)
	if err := b.SetLimits(2, 1); err != nil {
		t.Fatalf("SetLimits(2,1) returned unexpected error: %v", err)
	}
	for i := 0; i < 6; i++ {
		if _, err := b.Add(instance("x"+string(rune('0'+i)), point(1, 1, 1))); err != nil {
			t.Fatalf("Add returned unexpected error: %v", err)
		}
	}
	tree, err := b.Rebuild()
	if err != nil {
		t.Fatalf("Rebuild returned unexpected error: %v", err)
	}
	if tree.Depth != 1 {
		t.Fatalf("tree.Depth = %d, want 1 with maxDepth 1", tree.Depth)
	}
	for _, node := range tree.Nodes {
		if node.Level > 1 {
			t.Fatalf("node %q sits at level %d, deeper than maxDepth 1", node.ID, node.Level)
		}
	}
}

func TestRebuildIsRepeatable(t *testing.T) {
	b := newBuilder(t)
	if _, err := b.Add(instance("a", point(1, 1, 1))); err != nil {
		t.Fatalf("Add(a) returned unexpected error: %v", err)
	}
	if _, err := b.Add(instance("b", point(2, 2, 2))); err != nil {
		t.Fatalf("Add(b) returned unexpected error: %v", err)
	}
	first, err := b.Rebuild()
	if err != nil {
		t.Fatalf("first Rebuild returned unexpected error: %v", err)
	}
	second, err := b.Rebuild()
	if err != nil {
		t.Fatalf("second Rebuild returned unexpected error: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("two rebuilds of the same table differ:\nfirst=%+v\nsecond=%+v", first, second)
	}
}

func TestNodeOfIsEmptyBeforeRebuild(t *testing.T) {
	b := newBuilder(t)
	if _, err := b.Add(instance("a", point(1, 1, 1))); err != nil {
		t.Fatalf("Add(a) returned unexpected error: %v", err)
	}
	if id, ok := b.NodeOf("a"); ok || id != "" {
		t.Fatalf("NodeOf before Rebuild = %q,%v, want empty and false", id, ok)
	}
	if id, ok := b.NodeOf("missing"); ok {
		t.Fatalf("NodeOf(missing) reported node %q", id)
	}
	if _, err := b.Rebuild(); err != nil {
		t.Fatalf("Rebuild returned unexpected error: %v", err)
	}
	id, ok := b.NodeOf("a")
	if !ok || id == "" {
		t.Fatalf("NodeOf(a) after Rebuild = %q,%v, want a node id", id, ok)
	}
}

func TestRemoveDropsInstanceFromTree(t *testing.T) {
	b := newBuilder(t)
	if _, err := b.Add(instance("a", point(1, 1, 1))); err != nil {
		t.Fatalf("Add(a) returned unexpected error: %v", err)
	}
	if _, err := b.Add(instance("b", point(2, 2, 2))); err != nil {
		t.Fatalf("Add(b) returned unexpected error: %v", err)
	}
	if _, err := b.Add(instance("c", point(500, 500, 500))); err != nil {
		t.Fatalf("Add(c) returned unexpected error: %v", err)
	}
	before, err := b.Rebuild()
	if err != nil {
		t.Fatalf("first Rebuild returned unexpected error: %v", err)
	}
	if before.Leaves != 2 {
		t.Fatalf("leaves before remove = %d, want 2", before.Leaves)
	}
	if err := b.Remove("c"); err != nil {
		t.Fatalf("Remove(c) returned unexpected error: %v", err)
	}
	after, err := b.Rebuild()
	if err != nil {
		t.Fatalf("second Rebuild returned unexpected error: %v", err)
	}
	if after.Leaves != 1 {
		t.Fatalf("leaves after remove = %d, want 1", after.Leaves)
	}
	if _, ok := b.NodeOf("c"); ok {
		t.Fatalf("NodeOf(c) still reports a node after Remove")
	}
	if _, ok := b.NodeOf("a"); !ok {
		t.Fatalf("NodeOf(a) lost after removing a sibling cell")
	}
	if err := b.Remove("c"); !errors.Is(err, impl.ErrUnknownInstance) {
		t.Fatalf("second Remove(c): got %v, want ErrUnknownInstance", err)
	}
	if err := b.Remove("never-registered"); !errors.Is(err, impl.ErrUnknownInstance) {
		t.Fatalf("Remove(unknown): got %v, want ErrUnknownInstance", err)
	}
}

func TestSnapshotRestoreKeepsTreeAndMapping(t *testing.T) {
	b := newBuilder(t)
	for _, id := range []string{"a", "b", "c"} {
		if _, err := b.Add(instance(id, point(1, 1, 1))); err != nil {
			t.Fatalf("Add(%s) returned unexpected error: %v", id, err)
		}
	}
	if _, err := b.Rebuild(); err != nil {
		t.Fatalf("Rebuild returned unexpected error: %v", err)
	}
	want, ok := b.NodeOf("b")
	if !ok {
		t.Fatalf("NodeOf(b) missing before snapshot")
	}
	payload, err := b.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot returned unexpected error: %v", err)
	}
	fresh := impl.NewHLODClusterBuilder(100)
	if err := fresh.Restore(payload); err != nil {
		t.Fatalf("Restore returned unexpected error: %v", err)
	}
	if got := fresh.InstanceCount(); got != 3 {
		t.Fatalf("InstanceCount after Restore = %d, want 3", got)
	}
	got, ok := fresh.NodeOf("b")
	if !ok || got != want {
		t.Fatalf("NodeOf(b) after Restore = %q,%v, want %q", got, ok, want)
	}
	per, depth := fresh.Limits()
	if per != 4 || depth != 4 {
		t.Fatalf("limits after Restore = %d,%d, want 4,4", per, depth)
	}
}

func TestRestoreRejectsCorruptPayload(t *testing.T) {
	b := newBuilder(t)
	if _, err := b.Add(instance("a", point(1, 1, 1))); err != nil {
		t.Fatalf("Add(a) returned unexpected error: %v", err)
	}
	if err := b.Restore([]byte("broken")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("Restore(garbage): got %v, want ErrBadSnapshot", err)
	}
	if got := b.InstanceCount(); got != 1 {
		t.Fatalf("InstanceCount after rejected Restore = %d, want 1", got)
	}
}

func TestConcurrentAddsKeepCountAndMapping(t *testing.T) {
	b := impl.NewHLODClusterBuilder(100)
	if err := b.SetLimits(8, 6); err != nil {
		t.Fatalf("SetLimits(8,6) returned unexpected error: %v", err)
	}
	if err := b.SetInstanceLimit(256); err != nil {
		t.Fatalf("SetInstanceLimit(256) returned unexpected error: %v", err)
	}
	const workers = 40
	var wg sync.WaitGroup
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "inst-" + string(rune('A'+i%26)) + string(rune('a'+i/26))
			_, errs[i] = b.Add(instance(id, point(float64(i), float64(i%5), float64(i%3))))
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent Add %d returned error: %v", i, err)
		}
	}
	if got := b.InstanceCount(); got != workers {
		t.Fatalf("InstanceCount = %d, want %d", got, workers)
	}
	if _, err := b.Rebuild(); err != nil {
		t.Fatalf("Rebuild returned unexpected error: %v", err)
	}
	for i := 0; i < workers; i++ {
		id := "inst-" + string(rune('A'+i%26)) + string(rune('a'+i/26))
		if _, ok := b.NodeOf(id); !ok {
			t.Fatalf("NodeOf(%q) missing after Rebuild", id)
		}
	}
}
