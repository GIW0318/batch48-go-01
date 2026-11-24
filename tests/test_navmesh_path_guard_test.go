package task

import (
	"errors"
	"testing"

	impl "gsb/batch48-go-game-a-26"
)

func navGrid(width, height int) impl.Grid {
	tier := make([]int, width*height)
	for i := range tier {
		tier[i] = 1
	}
	return impl.Grid{Width: width, Height: height, Tier: tier}
}

func navOptions() impl.Options {
	return impl.Options{BaseCost: 10, DiagonalNum: 14, DiagonalDen: 10, MaxExpanded: 100000}
}

func mustNav(t *testing.T, width, height int) *impl.NavmeshPathGuard {
	t.Helper()
	g, err := impl.NewNavmeshPathGuard(navGrid(width, height), navOptions())
	if err != nil {
		t.Fatalf("NewNavmeshPathGuard: %v", err)
	}
	return g
}

func TestNewRejectsBrokenGrid(t *testing.T) {
	short := impl.Grid{Width: 3, Height: 3, Tier: []int{1, 1, 1}}
	if _, err := impl.NewNavmeshPathGuard(short, navOptions()); !errors.Is(err, impl.ErrInvalidGrid) {
		t.Fatalf("grid with missing tiers error = %v, want ErrInvalidGrid", err)
	}
	badTier := navGrid(3, 3)
	badTier.Tier[4] = 7
	if _, err := impl.NewNavmeshPathGuard(badTier, navOptions()); !errors.Is(err, impl.ErrInvalidGrid) {
		t.Fatalf("grid with tier 7 error = %v, want ErrInvalidGrid", err)
	}
	if _, err := impl.NewNavmeshPathGuard(navGrid(0, 4), navOptions()); !errors.Is(err, impl.ErrInvalidGrid) {
		t.Fatalf("grid with zero width error = %v, want ErrInvalidGrid", err)
	}
}

func TestNewRejectsBrokenOptions(t *testing.T) {
	opts := navOptions()
	opts.DiagonalDen = 0
	if _, err := impl.NewNavmeshPathGuard(navGrid(3, 3), opts); !errors.Is(err, impl.ErrInvalidOptions) {
		t.Fatalf("zero diagonal denominator error = %v, want ErrInvalidOptions", err)
	}
	opts = navOptions()
	opts.MaxExpanded = 0
	if _, err := impl.NewNavmeshPathGuard(navGrid(3, 3), opts); !errors.Is(err, impl.ErrInvalidOptions) {
		t.Fatalf("zero expansion ceiling error = %v, want ErrInvalidOptions", err)
	}
}

func TestFindRejectsInvalidArgs(t *testing.T) {
	g := mustNav(t, 4, 4)
	if _, err := g.Find(impl.Point{X: -1, Y: 0}, impl.Point{X: 2, Y: 2}, 50); !errors.Is(err, impl.ErrOutOfRange) {
		t.Fatalf("negative start error = %v, want ErrOutOfRange", err)
	}
	if _, err := g.Find(impl.Point{X: 0, Y: 0}, impl.Point{X: 4, Y: 1}, 50); !errors.Is(err, impl.ErrOutOfRange) {
		t.Fatalf("target past the width error = %v, want ErrOutOfRange", err)
	}
	for _, budget := range []int{0, -3} {
		if _, err := g.Find(impl.Point{X: 0, Y: 0}, impl.Point{X: 2, Y: 2}, budget); !errors.Is(err, impl.ErrInvalidBudget) {
			t.Fatalf("budget %d error = %v, want ErrInvalidBudget", budget, err)
		}
	}
}

func TestFindRejectsSolidStart(t *testing.T) {
	grid := navGrid(3, 3)
	grid.Tier[0] = 0
	g, err := impl.NewNavmeshPathGuard(grid, navOptions())
	if err != nil {
		t.Fatalf("NewNavmeshPathGuard: %v", err)
	}
	if _, err := g.Find(impl.Point{X: 0, Y: 0}, impl.Point{X: 2, Y: 2}, 50); !errors.Is(err, impl.ErrWallStart) {
		t.Fatalf("solid start error = %v, want ErrWallStart", err)
	}
}

func TestFindStraightOrthogonalCost(t *testing.T) {
	g := mustNav(t, 4, 1)
	path, err := g.Find(impl.Point{X: 0, Y: 0}, impl.Point{X: 3, Y: 0}, 50)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(path.Points) != 4 {
		t.Fatalf("path points = %d, want 4", len(path.Points))
	}
	if path.Cost != 30 {
		t.Fatalf("path cost = %d, want 30", path.Cost)
	}
	if path.Partial {
		t.Fatal("a reachable target must not be reported as a partial path")
	}
	if path.Start != (impl.Point{X: 0, Y: 0}) {
		t.Fatalf("path start = %+v, want the requested start", path.Start)
	}
}

func TestFindChargesTerrainTier(t *testing.T) {
	grid := impl.Grid{Width: 1, Height: 3, Tier: []int{1, 2, 3}}
	g, err := impl.NewNavmeshPathGuard(grid, navOptions())
	if err != nil {
		t.Fatalf("NewNavmeshPathGuard: %v", err)
	}
	path, err := g.Find(impl.Point{X: 0, Y: 0}, impl.Point{X: 0, Y: 2}, 50)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if path.Cost != 50 {
		t.Fatalf("path cost over tier 2 then tier 3 = %d, want 50", path.Cost)
	}
	if len(path.Points) != 3 {
		t.Fatalf("path points = %d, want 3", len(path.Points))
	}
}

func TestFindDiagonalUsesRealDistance(t *testing.T) {
	g := mustNav(t, 2, 2)
	path, err := g.Find(impl.Point{X: 0, Y: 0}, impl.Point{X: 1, Y: 1}, 50)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(path.Points) != 2 {
		t.Fatalf("diagonal path points = %d, want 2", len(path.Points))
	}
	if path.Cost != 14 {
		t.Fatalf("diagonal step cost = %d, want 14", path.Cost)
	}
}

func TestFindBudgetExhausted(t *testing.T) {
	g := mustNav(t, 20, 20)
	_, err := g.Find(impl.Point{X: 0, Y: 0}, impl.Point{X: 19, Y: 19}, 5)
	if !errors.Is(err, impl.ErrBudgetExhausted) {
		t.Fatalf("tiny budget error = %v, want ErrBudgetExhausted", err)
	}
}

func TestFindPartialPathToNearestCell(t *testing.T) {
	grid := navGrid(5, 5)
	for y := 0; y < 5; y++ {
		grid.Tier[4*5+y] = 0
	}
	g, err := impl.NewNavmeshPathGuard(grid, navOptions())
	if err != nil {
		t.Fatalf("NewNavmeshPathGuard: %v", err)
	}
	path, err := g.Find(impl.Point{X: 0, Y: 2}, impl.Point{X: 4, Y: 2}, 200)
	if err != nil {
		t.Fatalf("partial path must not fail: %v", err)
	}
	if !path.Partial {
		t.Fatal("path to a walled target must be marked partial")
	}
	if len(path.Points) == 0 {
		t.Fatal("partial path has no points")
	}
	last := path.Points[len(path.Points)-1]
	if last != (impl.Point{X: 3, Y: 2}) {
		t.Fatalf("partial path ends at %+v, want the nearest reachable cell 3,2", last)
	}
	if path.Cost != 30 {
		t.Fatalf("partial path cost = %d, want 30", path.Cost)
	}
}

func TestSmoothCollapsesCollinearPoints(t *testing.T) {
	g := mustNav(t, 4, 4)
	raw := impl.Path{
		Points: []impl.Point{{X: 0, Y: 0}, {X: 1, Y: 0}, {X: 2, Y: 0}, {X: 2, Y: 1}},
		Cost:   30,
	}
	got := g.Smooth(raw)
	if len(got.Points) != 3 {
		t.Fatalf("smoothed points = %d, want 3", len(got.Points))
	}
	if got.Cost != 30 {
		t.Fatalf("smoothed cost = %d, want 30", got.Cost)
	}
	if got.Points[1] != (impl.Point{X: 2, Y: 0}) {
		t.Fatalf("smoothed middle point = %+v, want 2,0", got.Points[1])
	}
	if len(raw.Points) != 4 {
		t.Fatalf("input path was modified: %d points left, want 4", len(raw.Points))
	}
}

func TestFindFollowsForcedCorridor(t *testing.T) {
	grid := navGrid(5, 5)
	for x := 0; x <= 3; x++ {
		grid.Tier[x] = 0
	}
	for x := 1; x <= 4; x++ {
		grid.Tier[3*5+x] = 0
	}
	g, err := impl.NewNavmeshPathGuard(grid, navOptions())
	if err != nil {
		t.Fatalf("NewNavmeshPathGuard: %v", err)
	}
	path, err := g.Find(impl.Point{X: 0, Y: 0}, impl.Point{X: 4, Y: 4}, 500)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(path.Points) != 17 {
		t.Fatalf("corridor path points = %d, want 17", len(path.Points))
	}
	if path.Cost != 160 {
		t.Fatalf("corridor path cost = %d, want 160", path.Cost)
	}
}

func TestBlockIsAtomicAndBlocksQuery(t *testing.T) {
	g := mustNav(t, 3, 3)
	if err := g.Block([]impl.Cell{{X: 1, Y: 0}, {X: 1, Y: 1}}, 500); err != nil {
		t.Fatalf("Block: %v", err)
	}
	if !g.Blocked(impl.Point{X: 1, Y: 0}, 499) {
		t.Fatal("cell blocked before its until instant was reported free")
	}
	if g.Blocked(impl.Point{X: 1, Y: 0}, 500) {
		t.Fatal("cell still blocked at its until instant")
	}
	if err := g.Block([]impl.Cell{{X: 2, Y: 2}, {X: 5, Y: 0}}, 600); !errors.Is(err, impl.ErrOutOfRange) {
		t.Fatalf("block set with an outside cell error = %v, want ErrOutOfRange", err)
	}
	if g.Blocked(impl.Point{X: 2, Y: 2}, 100) {
		t.Fatal("a rejected block set was applied partially")
	}
	path, err := g.Find(impl.Point{X: 0, Y: 0}, impl.Point{X: 2, Y: 0}, 100)
	if err != nil {
		t.Fatalf("Find around blocks: %v", err)
	}
	if path.Cost != 60 {
		t.Fatalf("detour cost = %d, want 60", path.Cost)
	}
	for _, p := range path.Points {
		if p == (impl.Point{X: 1, Y: 0}) || p == (impl.Point{X: 1, Y: 1}) {
			t.Fatalf("path crosses blocked cell %+v", p)
		}
	}
}

func TestClearExpiredAdvancesClock(t *testing.T) {
	g := mustNav(t, 3, 3)
	cells := []impl.Cell{{X: 0, Y: 0}, {X: 1, Y: 1}, {X: 2, Y: 2}}
	if err := g.Block(cells, 100); err != nil {
		t.Fatalf("Block: %v", err)
	}
	if got := g.ClearExpired(99); got != 0 {
		t.Fatalf("cleared %d blocks before expiry, want 0", got)
	}
	if got := g.ClearExpired(100); got != 3 {
		t.Fatalf("cleared %d expired blocks, want 3", got)
	}
	if got := g.Clock(); got != 100 {
		t.Fatalf("clock = %d, want 100", got)
	}
	if g.Blocked(impl.Point{X: 0, Y: 0}, 50) {
		t.Fatal("expired block is still reported as blocking")
	}
}

func TestCachedQueryAndInvalidate(t *testing.T) {
	g := mustNav(t, 5, 5)
	from := impl.Point{X: 0, Y: 0}
	to := impl.Point{X: 3, Y: 0}
	if _, err := g.Find(from, to, 100); err != nil {
		t.Fatalf("first Find: %v", err)
	}
	if _, err := g.Find(from, to, 100); err != nil {
		t.Fatalf("second Find: %v", err)
	}
	stats := g.Stats()
	if stats.Queries != 2 {
		t.Fatalf("query counter = %d, want 2", stats.Queries)
	}
	if stats.CacheHits != 1 {
		t.Fatalf("cache hits = %d, want 1", stats.CacheHits)
	}
	if err := g.Invalidate(impl.Region{Min: impl.Point{X: 0, Y: 0}, Max: impl.Point{X: 4, Y: 4}}); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if _, err := g.Find(from, to, 100); err != nil {
		t.Fatalf("third Find: %v", err)
	}
	stats = g.Stats()
	if stats.Queries != 3 {
		t.Fatalf("query counter after invalidate = %d, want 3", stats.Queries)
	}
	if stats.CacheHits != 1 {
		t.Fatalf("cache hits after invalidate = %d, want 1", stats.CacheHits)
	}
}

func TestSnapshotRoundTripKeepsBlocks(t *testing.T) {
	g := mustNav(t, 3, 3)
	if err := g.Block([]impl.Cell{{X: 1, Y: 1}}, 900); err != nil {
		t.Fatalf("Block: %v", err)
	}
	if got := g.ClearExpired(50); got != 0 {
		t.Fatalf("cleared %d blocks, want 0", got)
	}
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if err := g.Block([]impl.Cell{{X: 0, Y: 0}}, 900); err != nil {
		t.Fatalf("Block after snapshot: %v", err)
	}
	if err := g.Restore(snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if !g.Blocked(impl.Point{X: 1, Y: 1}, 60) {
		t.Fatal("restored block is missing")
	}
	if g.Blocked(impl.Point{X: 0, Y: 0}, 60) {
		t.Fatal("block added after the snapshot survived restore")
	}
	if got := g.Clock(); got != 50 {
		t.Fatalf("clock after restore = %d, want 50", got)
	}
	if err := g.Restore([]byte("broken")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("bad snapshot error = %v, want ErrBadSnapshot", err)
	}
	if !g.Blocked(impl.Point{X: 1, Y: 1}, 60) {
		t.Fatal("state was damaged by a rejected restore")
	}
}
