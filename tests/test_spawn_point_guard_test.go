package task

import (
	"errors"
	"sync"
	"testing"

	impl "gsb/batch48-go-game-a-29"
)

func newSpawns(caps map[string]int) *impl.SpawnPointGuard {
	return impl.NewSpawnPointGuard(impl.Options{RegionCap: caps})
}

func addPoint(t *testing.T, g *impl.SpawnPointGuard, p impl.Point) {
	t.Helper()
	if err := g.AddPoint(p); err != nil {
		t.Fatalf("AddPoint(%s): %v", p.Name, err)
	}
}

func wolfPoint() impl.Point {
	return impl.Point{Name: "wolf", Region: "north", Interval: 100, Capacity: 3}
}

func TestAddPointRejectsBadDefinition(t *testing.T) {
	g := newSpawns(nil)
	cases := []impl.Point{
		{Name: "", Region: "north", Interval: 100, Capacity: 3},
		{Name: "wolf", Region: "north", Interval: 0, Capacity: 3},
		{Name: "wolf", Region: "north", Interval: 100, Capacity: 0},
	}
	for i, p := range cases {
		if err := g.AddPoint(p); !errors.Is(err, impl.ErrInvalidPoint) {
			t.Fatalf("case %d error = %v, want ErrInvalidPoint", i, err)
		}
	}
}

func TestAddPointRejectsDuplicate(t *testing.T) {
	g := newSpawns(nil)
	addPoint(t, g, wolfPoint())
	if err := g.AddPoint(wolfPoint()); !errors.Is(err, impl.ErrDuplicatePoint) {
		t.Fatalf("duplicate point error = %v, want ErrDuplicatePoint", err)
	}
}

func TestKillUnknownPoint(t *testing.T) {
	g := newSpawns(nil)
	if err := g.Kill("ghost", "id-1", 0); !errors.Is(err, impl.ErrUnknownPoint) {
		t.Fatalf("kill on unknown point error = %v, want ErrUnknownPoint", err)
	}
}

func TestKillUnknownSpawn(t *testing.T) {
	g := newSpawns(nil)
	addPoint(t, g, wolfPoint())
	if err := g.Kill("wolf", "missing", 0); !errors.Is(err, impl.ErrUnknownSpawn) {
		t.Fatalf("kill of unknown spawn error = %v, want ErrUnknownSpawn", err)
	}
}

func TestReadyRespectsInterval(t *testing.T) {
	g := newSpawns(nil)
	addPoint(t, g, wolfPoint())
	if n, err := g.Ready("wolf", 50); err != nil || n != 0 {
		t.Fatalf("ready before the interval = %d, %v, want 0, nil", n, err)
	}
	if n, err := g.Ready("wolf", 100); err != nil || n != 3 {
		t.Fatalf("ready at the interval = %d, %v, want 3, nil", n, err)
	}
}

func TestTakeBeforeIntervalFails(t *testing.T) {
	g := newSpawns(nil)
	addPoint(t, g, wolfPoint())
	if _, err := g.Take("wolf", 1, 99); !errors.Is(err, impl.ErrNotReady) {
		t.Fatalf("early take error = %v, want ErrNotReady", err)
	}
}

func TestTakeRejectsBadCount(t *testing.T) {
	g := newSpawns(nil)
	addPoint(t, g, wolfPoint())
	for _, n := range []int{0, -2} {
		if _, err := g.Take("wolf", n, 100); !errors.Is(err, impl.ErrInvalidCount) {
			t.Fatalf("take %d error = %v, want ErrInvalidCount", n, err)
		}
	}
}

func TestTakeReturnsSpawnDetails(t *testing.T) {
	g := newSpawns(nil)
	addPoint(t, g, wolfPoint())
	spawns, err := g.Take("wolf", 3, 100)
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	if len(spawns) != 3 {
		t.Fatalf("spawns = %d, want 3", len(spawns))
	}
	seen := make(map[string]bool)
	for i, s := range spawns {
		if s.Point != "wolf" {
			t.Fatalf("spawn %d point = %q, want wolf", i, s.Point)
		}
		if s.Phase != "default" {
			t.Fatalf("spawn %d phase = %q, want default", i, s.Phase)
		}
		if s.At != 100 {
			t.Fatalf("spawn %d time = %d, want 100", i, s.At)
		}
		if s.ID == "" || seen[s.ID] {
			t.Fatalf("spawn %d has an empty or repeated id %q", i, s.ID)
		}
		seen[s.ID] = true
	}
}

func TestTakeRespectsCapacity(t *testing.T) {
	g := newSpawns(nil)
	addPoint(t, g, impl.Point{Name: "bat", Region: "cave", Interval: 100, Capacity: 2})
	spawns, err := g.Take("bat", 5, 100)
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	if len(spawns) != 2 {
		t.Fatalf("spawns = %d, want the capacity 2", len(spawns))
	}
	if got := g.Density("cave"); got != 2 {
		t.Fatalf("density = %d, want 2", got)
	}
}

func TestTakeRespectsRegionDensity(t *testing.T) {
	g := newSpawns(map[string]int{"alpha": 2})
	addPoint(t, g, impl.Point{Name: "orc", Region: "alpha", Interval: 100, Capacity: 5})
	spawns, err := g.Take("orc", 5, 100)
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	if len(spawns) != 2 {
		t.Fatalf("spawns = %d, want the density ceiling 2", len(spawns))
	}
	if got := g.Density("alpha"); got != 2 {
		t.Fatalf("region density = %d, want 2", got)
	}
	if got := g.Density("void"); got != 0 {
		t.Fatalf("density of an unknown region = %d, want 0", got)
	}
}

func TestKillFreesCapacityAfterInterval(t *testing.T) {
	g := newSpawns(nil)
	addPoint(t, g, impl.Point{Name: "bat", Region: "cave", Interval: 100, Capacity: 2})
	spawns, err := g.Take("bat", 2, 100)
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	if len(spawns) != 2 {
		t.Fatalf("spawns = %d, want 2", len(spawns))
	}
	if err := g.Kill("bat", spawns[0].ID, 150); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if got := g.Density("cave"); got != 1 {
		t.Fatalf("density after kill = %d, want 1", got)
	}
	if n, err := g.Ready("bat", 249); err != nil || n != 0 {
		t.Fatalf("ready before the timer restarts = %d, %v, want 0, nil", n, err)
	}
	if n, err := g.Ready("bat", 250); err != nil || n != 1 {
		t.Fatalf("ready after the timer restarts = %d, %v, want 1, nil", n, err)
	}
}

func TestSetPhaseRejectsEmptyAndTagsSpawns(t *testing.T) {
	g := newSpawns(nil)
	addPoint(t, g, impl.Point{Name: "ghost", Region: "hall", Interval: 100, Capacity: 5})
	if err := g.SetPhase(""); !errors.Is(err, impl.ErrInvalidPhase) {
		t.Fatalf("empty phase error = %v, want ErrInvalidPhase", err)
	}
	if _, err := g.Take("ghost", 1, 100); err != nil {
		t.Fatalf("Take in the default phase: %v", err)
	}
	if err := g.SetPhase("night"); err != nil {
		t.Fatalf("SetPhase: %v", err)
	}
	if got := g.Phase(); got != "night" {
		t.Fatalf("active phase = %q, want night", got)
	}
	spawns, err := g.Take("ghost", 1, 200)
	if err != nil {
		t.Fatalf("Take in the night phase: %v", err)
	}
	if len(spawns) != 1 || spawns[0].Phase != "night" {
		t.Fatalf("night spawns = %+v, want one spawn tagged night", spawns)
	}
}

func TestUnknownPointQueriesFail(t *testing.T) {
	g := newSpawns(nil)
	if _, err := g.Ready("ghost", 0); !errors.Is(err, impl.ErrUnknownPoint) {
		t.Fatalf("ready of unknown point error = %v, want ErrUnknownPoint", err)
	}
	if _, err := g.Take("ghost", 1, 100); !errors.Is(err, impl.ErrUnknownPoint) {
		t.Fatalf("take of unknown point error = %v, want ErrUnknownPoint", err)
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	g := newSpawns(nil)
	addPoint(t, g, impl.Point{Name: "bat", Region: "cave", Interval: 100, Capacity: 4})
	if _, err := g.Take("bat", 2, 100); err != nil {
		t.Fatalf("Take: %v", err)
	}
	if err := g.SetPhase("night"); err != nil {
		t.Fatalf("SetPhase: %v", err)
	}
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if err := g.Kill("bat", "not-a-spawn", 150); !errors.Is(err, impl.ErrUnknownSpawn) {
		t.Fatalf("kill of a made up spawn error = %v, want ErrUnknownSpawn", err)
	}
	spawns, err := g.Take("bat", 1, 200)
	if err != nil {
		t.Fatalf("Take before restore: %v", err)
	}
	if err := g.Kill("bat", spawns[0].ID, 250); err != nil {
		t.Fatalf("Kill before restore: %v", err)
	}
	if err := g.Restore(snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := g.Density("cave"); got != 2 {
		t.Fatalf("density after restore = %d, want 2", got)
	}
	if got := g.Phase(); got != "night" {
		t.Fatalf("phase after restore = %q, want night", got)
	}
	if err := g.Restore([]byte("broken")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("bad snapshot error = %v, want ErrBadSnapshot", err)
	}
	if got := g.Density("cave"); got != 2 {
		t.Fatalf("density after a rejected restore = %d, want 2", got)
	}
}

func TestConcurrentKillOnlyOnce(t *testing.T) {
	g := newSpawns(nil)
	addPoint(t, g, wolfPoint())
	spawns, err := g.Take("wolf", 3, 100)
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	if len(spawns) != 3 {
		t.Fatalf("spawns = %d, want 3", len(spawns))
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	killed, missing := 0, 0
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := g.Kill("wolf", spawns[0].ID, 150)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				killed++
			case errors.Is(err, impl.ErrUnknownSpawn):
				missing++
			}
		}()
	}
	wg.Wait()
	if killed != 1 {
		t.Fatalf("successful kills = %d, want exactly 1", killed)
	}
	if missing != 5 {
		t.Fatalf("unknown spawn errors = %d, want 5", missing)
	}
	if got := g.Density("north"); got != 2 {
		t.Fatalf("density after concurrent kills = %d, want 2", got)
	}
}
