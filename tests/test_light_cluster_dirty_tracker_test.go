package task

import (
	"errors"
	"sync"
	"testing"

	impl "gsb/batch48-go-game-b-11"
)

func newTracker() *impl.LightClusterTracker {
	return impl.NewLightClusterTracker(impl.TrackerConfig{
		CellSize:        2,
		ClusterCapacity: 16,
		DirtyBudget:     32,
	})
}

func addLight(t *testing.T, tr *impl.LightClusterTracker, id int, x, y, z, radius float64) int {
	t.Helper()
	got, err := tr.AddLight(impl.Light{ID: id, Center: impl.Vec3{X: x, Y: y, Z: z}, Radius: radius})
	if err != nil {
		t.Fatalf("AddLight(id=%d) returned unexpected error: %v", id, err)
	}
	return got
}

func TestAddLightAutoAssignsSmallestFreeID(t *testing.T) {
	tr := newTracker()
	first, err := tr.AddLight(impl.Light{Center: impl.Vec3{X: 1, Y: 1, Z: 1}, Radius: 0.2})
	if err != nil {
		t.Fatalf("first auto AddLight returned unexpected error: %v", err)
	}
	second, err := tr.AddLight(impl.Light{Center: impl.Vec3{X: 2, Y: 2, Z: 2}, Radius: 0.2})
	if err != nil {
		t.Fatalf("second auto AddLight returned unexpected error: %v", err)
	}
	if first != 1 || second != 2 {
		t.Fatalf("auto assigned ids = (%d,%d), want (1,2)", first, second)
	}
	if got := tr.Stats().Lights; got != 2 {
		t.Fatalf("Stats().Lights = %d, want 2", got)
	}
}

func TestAddLightRejectsDuplicateAndBadRadius(t *testing.T) {
	tr := newTracker()
	addLight(t, tr, 5, 1, 1, 1, 0.2)
	if _, err := tr.AddLight(impl.Light{ID: 5, Center: impl.Vec3{X: 3, Y: 3, Z: 3}, Radius: 0.2}); !errors.Is(err, impl.ErrLightExists) {
		t.Fatalf("duplicate AddLight: got %v, want ErrLightExists", err)
	}
	if _, err := tr.AddLight(impl.Light{ID: 6, Center: impl.Vec3{X: 3, Y: 3, Z: 3}, Radius: 0}); !errors.Is(err, impl.ErrBadRadius) {
		t.Fatalf("AddLight with zero radius: got %v, want ErrBadRadius", err)
	}
	if got := tr.Stats().Lights; got != 1 {
		t.Fatalf("Stats().Lights after rejected adds = %d, want 1", got)
	}
}

func TestClusterAtReturnsSortedUniqueIDs(t *testing.T) {
	tr := newTracker()
	addLight(t, tr, 3, 0.5, 0.5, 0.5, 0.2)
	addLight(t, tr, 1, 1.5, 1.5, 1.5, 0.2)
	addLight(t, tr, 2, 0.8, 0.8, 0.8, 0.2)
	key := impl.ClusterKey{X: 0, Y: 0, Z: 0}
	ids, gen := tr.ClusterAt(key)
	if len(ids) != 3 {
		t.Fatalf("ClusterAt(%v) holds %v, want three ids", key, ids)
	}
	want := []int{1, 2, 3}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("ClusterAt(%v) = %v, want sorted %v", key, ids, want)
		}
	}
	if gen == 0 {
		t.Fatalf("ClusterAt(%v) generation = 0, want a generation after registration", key)
	}
}

func TestMoveLightMarksOnlyAffectedClustersDirty(t *testing.T) {
	tr := newTracker()
	addLight(t, tr, 1, 1, 1, 1, 0.2)
	addLight(t, tr, 2, 21, 1, 1, 0.2)
	if err := tr.ClearDirty(); err != nil {
		t.Fatalf("ClearDirty returned unexpected error: %v", err)
	}
	if err := tr.MoveLight(1, impl.Vec3{X: 11, Y: 1, Z: 1}, 0.2); err != nil {
		t.Fatalf("MoveLight returned unexpected error: %v", err)
	}
	dirty := tr.DirtyClusters()
	want := []impl.ClusterKey{{X: 0, Y: 0, Z: 0}, {X: 5, Y: 0, Z: 0}}
	if len(dirty) != len(want) {
		t.Fatalf("DirtyClusters = %v, want the old and new cluster %v", dirty, want)
	}
	for i := range want {
		if dirty[i] != want[i] {
			t.Fatalf("DirtyClusters = %v, want sorted %v", dirty, want)
		}
	}
}

func TestMoveLightKeepsUnrelatedIndexAndGeneration(t *testing.T) {
	tr := newTracker()
	addLight(t, tr, 1, 1, 1, 1, 0.2)
	addLight(t, tr, 2, 21, 1, 1, 0.2)
	keyA := impl.ClusterKey{X: 0, Y: 0, Z: 0}
	idsBefore, genBefore := tr.ClusterAt(keyA)
	if err := tr.MoveLight(2, impl.Vec3{X: 31, Y: 1, Z: 1}, 0.2); err != nil {
		t.Fatalf("MoveLight returned unexpected error: %v", err)
	}
	idsAfter, genAfter := tr.ClusterAt(keyA)
	if len(idsBefore) != 1 || len(idsAfter) != 1 || idsAfter[0] != 1 {
		t.Fatalf("unaffected cluster ids before=%v after=%v, want [1] both times", idsBefore, idsAfter)
	}
	if genAfter != genBefore {
		t.Fatalf("unaffected cluster generation moved from %d to %d", genBefore, genAfter)
	}
}

func TestMoveLightRejectsUnknownIDAndBadRadius(t *testing.T) {
	tr := newTracker()
	addLight(t, tr, 1, 1, 1, 1, 0.2)
	if err := tr.MoveLight(99, impl.Vec3{X: 5, Y: 5, Z: 5}, 0.2); !errors.Is(err, impl.ErrLightMissing) {
		t.Fatalf("MoveLight(99): got %v, want ErrLightMissing", err)
	}
	if err := tr.MoveLight(1, impl.Vec3{X: 5, Y: 5, Z: 5}, -0.1); !errors.Is(err, impl.ErrBadRadius) {
		t.Fatalf("MoveLight with negative radius: got %v, want ErrBadRadius", err)
	}
}

func TestRemoveLightDropsItFromEveryCluster(t *testing.T) {
	tr := newTracker()
	addLight(t, tr, 7, 1, 1, 1, 1.5)
	for _, key := range []impl.ClusterKey{{X: -1, Y: 0, Z: 0}, {X: 0, Y: 0, Z: 0}, {X: 1, Y: 1, Z: 1}} {
		ids, _ := tr.ClusterAt(key)
		found := false
		for _, id := range ids {
			if id == 7 {
				found = true
			}
		}
		if !found {
			t.Fatalf("spanning light is missing from cluster %v: %v", key, ids)
		}
	}
	if err := tr.RemoveLight(7); err != nil {
		t.Fatalf("RemoveLight returned unexpected error: %v", err)
	}
	if got := tr.Stats().Lights; got != 0 {
		t.Fatalf("Stats().Lights after RemoveLight = %d, want 0", got)
	}
	for _, key := range []impl.ClusterKey{{X: -1, Y: 0, Z: 0}, {X: 0, Y: 0, Z: 0}, {X: 1, Y: 1, Z: 1}} {
		if ids, _ := tr.ClusterAt(key); len(ids) != 0 {
			t.Fatalf("cluster %v still holds %v after RemoveLight", key, ids)
		}
	}
}

func TestRemoveLightRejectsUnknownID(t *testing.T) {
	tr := newTracker()
	if err := tr.RemoveLight(42); !errors.Is(err, impl.ErrLightMissing) {
		t.Fatalf("RemoveLight(42): got %v, want ErrLightMissing", err)
	}
}

func TestDirtyBudgetOverflowDegradesToRebuild(t *testing.T) {
	tr := newTracker()
	if err := tr.SetDirtyBudget(2); err != nil {
		t.Fatalf("SetDirtyBudget(2) returned unexpected error: %v", err)
	}
	_, err := tr.AddLight(impl.Light{ID: 1, Center: impl.Vec3{X: 1, Y: 1, Z: 1}, Radius: 3})
	if err != nil {
		t.Fatalf("AddLight spanning more than the budget returned unexpected error: %v", err)
	}
	stats := tr.Stats()
	if !stats.Degraded {
		t.Fatalf("Stats().Degraded = false after exceeding the dirty budget")
	}
	if stats.Rebuilds != 1 {
		t.Fatalf("Stats().Rebuilds = %d after one degraded rebuild, want 1", stats.Rebuilds)
	}
}

func TestSetDirtyBudgetRejectsNonPositiveValue(t *testing.T) {
	tr := newTracker()
	if err := tr.SetDirtyBudget(0); !errors.Is(err, impl.ErrBadBudget) {
		t.Fatalf("SetDirtyBudget(0): got %v, want ErrBadBudget", err)
	}
	if err := tr.SetDirtyBudget(-3); !errors.Is(err, impl.ErrBadBudget) {
		t.Fatalf("SetDirtyBudget(-3): got %v, want ErrBadBudget", err)
	}
}

func TestRebuildAdvancesEveryGeneration(t *testing.T) {
	tr := newTracker()
	addLight(t, tr, 1, 1, 1, 1, 0.2)
	addLight(t, tr, 2, 21, 1, 1, 0.2)
	keyA := impl.ClusterKey{X: 0, Y: 0, Z: 0}
	keyB := impl.ClusterKey{X: 10, Y: 0, Z: 0}
	_, genA := tr.ClusterAt(keyA)
	_, genB := tr.ClusterAt(keyB)
	if err := tr.Rebuild(); err != nil {
		t.Fatalf("Rebuild returned unexpected error: %v", err)
	}
	if got, _ := tr.ClusterAt(keyA); len(got) != 1 {
		t.Fatalf("cluster %v after Rebuild = %v, want [1]", keyA, got)
	}
	if _, gen := tr.ClusterAt(keyA); gen <= genA {
		t.Fatalf("generation of %v did not advance: %d -> %d", keyA, genA, gen)
	}
	if _, gen := tr.ClusterAt(keyB); gen <= genB {
		t.Fatalf("generation of %v did not advance: %d -> %d", keyB, genB, gen)
	}
	if got := tr.Stats().Rebuilds; got != 1 {
		t.Fatalf("Stats().Rebuilds = %d after Rebuild, want 1", got)
	}
}

func TestClusterOfFloorsNegativeCoordinates(t *testing.T) {
	tr := newTracker()
	key, err := tr.ClusterOf(impl.Vec3{X: 1.5, Y: 2.5, Z: 3.5})
	if err != nil {
		t.Fatalf("ClusterOf positive point returned unexpected error: %v", err)
	}
	if key != (impl.ClusterKey{X: 0, Y: 1, Z: 1}) {
		t.Fatalf("ClusterOf(1.5,2.5,3.5) = %v, want {0 1 1}", key)
	}
	key, err = tr.ClusterOf(impl.Vec3{X: -0.5, Y: -2.5, Z: 0})
	if err != nil {
		t.Fatalf("ClusterOf negative point returned unexpected error: %v", err)
	}
	if key != (impl.ClusterKey{X: -1, Y: -2, Z: 0}) {
		t.Fatalf("ClusterOf(-0.5,-2.5,0) = %v, want {-1 -2 0}", key)
	}
}

func TestSnapshotRestoreKeepsLightsAndGenerations(t *testing.T) {
	tr := newTracker()
	addLight(t, tr, 1, 1, 1, 1, 0.2)
	addLight(t, tr, 2, 1.2, 1.2, 1.2, 0.2)
	key := impl.ClusterKey{X: 0, Y: 0, Z: 0}
	idsBefore, genBefore := tr.ClusterAt(key)
	payload, err := tr.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot returned unexpected error: %v", err)
	}
	fresh := newTracker()
	if err := fresh.Restore(payload); err != nil {
		t.Fatalf("Restore returned unexpected error: %v", err)
	}
	idsAfter, genAfter := fresh.ClusterAt(key)
	if len(idsAfter) != len(idsBefore) {
		t.Fatalf("ids after Restore = %v, want %v", idsAfter, idsBefore)
	}
	for i := range idsBefore {
		if idsAfter[i] != idsBefore[i] {
			t.Fatalf("ids after Restore = %v, want %v", idsAfter, idsBefore)
		}
	}
	if genAfter != genBefore {
		t.Fatalf("generation after Restore = %d, want %d", genAfter, genBefore)
	}
	if err := fresh.Restore([]byte("garbage")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("Restore(garbage): got %v, want ErrBadSnapshot", err)
	}
	if got := fresh.Stats().Lights; got != 2 {
		t.Fatalf("Stats().Lights after rejected Restore = %d, want 2", got)
	}
}

func TestConcurrentMoveAndQuery(t *testing.T) {
	tr := newTracker()
	addLight(t, tr, 1, 1, 1, 1, 0.2)
	addLight(t, tr, 2, 3, 1, 1, 0.2)
	addLight(t, tr, 3, 5, 1, 1, 0.2)
	addLight(t, tr, 4, 7, 1, 1, 0.2)
	var wg sync.WaitGroup
	for id := 1; id <= 4; id++ {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			for step := 0; step < 6; step++ {
				x := float64(id*2 + step*2)
				if err := tr.MoveLight(id, impl.Vec3{X: x, Y: 1, Z: 1}, 0.2); err != nil {
					t.Errorf("concurrent MoveLight(%d) returned error: %v", id, err)
					return
				}
				for cx := 0; cx < 12; cx++ {
					tr.ClusterAt(impl.ClusterKey{X: cx, Y: 0, Z: 0})
				}
			}
		}()
	}
	wg.Wait()
	if got := tr.Stats().Lights; got != 4 {
		t.Fatalf("Stats().Lights after concurrent moves = %d, want 4", got)
	}
}

func TestStatsMatchDirtyClusters(t *testing.T) {
	tr := newTracker()
	addLight(t, tr, 1, 1, 1, 1, 0.2)
	addLight(t, tr, 2, 11, 1, 1, 0.2)
	stats := tr.Stats()
	if stats.Lights != 2 || stats.Clusters != 2 {
		t.Fatalf("Stats() = %+v, want two lights in two clusters", stats)
	}
	dirty := tr.DirtyClusters()
	if len(dirty) != 2 {
		t.Fatalf("DirtyClusters = %v, want two clusters", dirty)
	}
	if stats.Dirty != len(dirty) {
		t.Fatalf("Stats().Dirty = %d, want %d", stats.Dirty, len(dirty))
	}
}
