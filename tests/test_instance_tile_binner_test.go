package task

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	impl "gsb/batch48-go-game-b-06"
)

func rect(x0, y0, x1, y1 float64) impl.Rect {
	return impl.Rect{MinX: x0, MinY: y0, MaxX: x1, MaxY: y1}
}

func mustAdd(t *testing.T, b *impl.InstanceTileBinner, r impl.Rect) impl.Handle {
	t.Helper()
	h, err := b.Add(impl.Instance{Bounds: r})
	if err != nil {
		t.Fatalf("Add(%v) returned unexpected error: %v", r, err)
	}
	return h
}

func TestAddAssignsSequentialHandles(t *testing.T) {
	b := impl.NewInstanceTileBinner(64, 4)
	h1 := mustAdd(t, b, rect(10, 10, 20, 20))
	h2 := mustAdd(t, b, rect(30, 30, 40, 40))
	if h1 != 1 || h2 != 2 {
		t.Fatalf("expected handles 1 and 2, got %d and %d", h1, h2)
	}
	if got := b.HandleCount(); got != 2 {
		t.Fatalf("HandleCount = %d, want 2", got)
	}
}

func TestAddRejectsDegenerateBounds(t *testing.T) {
	b := impl.NewInstanceTileBinner(64, 4)
	if _, err := b.Add(impl.Instance{Bounds: rect(30, 10, 20, 20)}); !errors.Is(err, impl.ErrBadBounds) {
		t.Fatalf("inverted bounds: got %v, want ErrBadBounds", err)
	}
	if _, err := b.Add(impl.Instance{Bounds: rect(5, 5, 5, 5)}); !errors.Is(err, impl.ErrBadBounds) {
		t.Fatalf("empty bounds: got %v, want ErrBadBounds", err)
	}
	if got := b.HandleCount(); got != 0 {
		t.Fatalf("HandleCount = %d after rejected adds, want 0", got)
	}
}

func TestAddRejectsBeyondInstanceLimit(t *testing.T) {
	b := impl.NewInstanceTileBinner(64, 4)
	if err := b.SetInstanceLimit(2); err != nil {
		t.Fatalf("SetInstanceLimit(2) returned unexpected error: %v", err)
	}
	mustAdd(t, b, rect(1, 1, 2, 2))
	mustAdd(t, b, rect(3, 3, 4, 4))
	if _, err := b.Add(impl.Instance{Bounds: rect(5, 5, 6, 6)}); !errors.Is(err, impl.ErrLimitReached) {
		t.Fatalf("third add: got %v, want ErrLimitReached", err)
	}
	if got := b.HandleCount(); got != 2 {
		t.Fatalf("HandleCount = %d, want 2 after rejected add", got)
	}
}

func TestRemoveDropsHandleAndRejectsUnknown(t *testing.T) {
	b := impl.NewInstanceTileBinner(64, 4)
	h := mustAdd(t, b, rect(1, 1, 8, 8))
	if err := b.Remove(h); err != nil {
		t.Fatalf("Remove(%d) returned unexpected error: %v", h, err)
	}
	if _, ok := b.BoundsOf(h); ok {
		t.Fatalf("BoundsOf(%d) still reports bounds after Remove", h)
	}
	if err := b.Remove(h); !errors.Is(err, impl.ErrUnknownHandle) {
		t.Fatalf("second Remove: got %v, want ErrUnknownHandle", err)
	}
	if err := b.Remove(99); !errors.Is(err, impl.ErrUnknownHandle) {
		t.Fatalf("Remove(99): got %v, want ErrUnknownHandle", err)
	}
}

func TestMoveReplacesBoundsAndValidates(t *testing.T) {
	b := impl.NewInstanceTileBinner(64, 4)
	h := mustAdd(t, b, rect(1, 1, 8, 8))
	if err := b.Move(h, rect(70, 70, 80, 90)); err != nil {
		t.Fatalf("Move(%d) returned unexpected error: %v", h, err)
	}
	got, ok := b.BoundsOf(h)
	if !ok {
		t.Fatalf("BoundsOf(%d) missing after Move", h)
	}
	if got != rect(70, 70, 80, 90) {
		t.Fatalf("bounds after Move = %v, want %v", got, rect(70, 70, 80, 90))
	}
	if err := b.Move(99, rect(0, 0, 1, 1)); !errors.Is(err, impl.ErrUnknownHandle) {
		t.Fatalf("Move(99): got %v, want ErrUnknownHandle", err)
	}
	if err := b.Move(h, rect(9, 9, 8, 8)); !errors.Is(err, impl.ErrBadBounds) {
		t.Fatalf("inverted Move: got %v, want ErrBadBounds", err)
	}
	if got, _ := b.BoundsOf(h); got != rect(70, 70, 80, 90) {
		t.Fatalf("bounds changed by rejected Move: got %v", got)
	}
}

func TestSetTileSizeAndCapacityValidate(t *testing.T) {
	b := impl.NewInstanceTileBinner(64, 4)
	if err := b.SetTileSize(0); !errors.Is(err, impl.ErrBadTileSize) {
		t.Fatalf("SetTileSize(0): got %v, want ErrBadTileSize", err)
	}
	if err := b.SetTileSize(-8); !errors.Is(err, impl.ErrBadTileSize) {
		t.Fatalf("SetTileSize(-8): got %v, want ErrBadTileSize", err)
	}
	if err := b.SetTileSize(32); err != nil {
		t.Fatalf("SetTileSize(32) returned unexpected error: %v", err)
	}
	if got := b.TileSize(); got != 32 {
		t.Fatalf("TileSize = %d, want 32", got)
	}
	if err := b.SetCapacity(0); !errors.Is(err, impl.ErrBadCapacity) {
		t.Fatalf("SetCapacity(0): got %v, want ErrBadCapacity", err)
	}
	if err := b.SetCapacity(8); err != nil {
		t.Fatalf("SetCapacity(8) returned unexpected error: %v", err)
	}
	if got := b.Capacity(); got != 8 {
		t.Fatalf("Capacity = %d, want 8", got)
	}
}

func TestBuildPlacesInstancesInTheirTileAscending(t *testing.T) {
	b := impl.NewInstanceTileBinner(64, 8)
	mustAdd(t, b, rect(5, 5, 15, 15))
	mustAdd(t, b, rect(20, 20, 30, 30))
	mustAdd(t, b, rect(40, 40, 50, 50))
	bins, err := b.Build(impl.Viewport{Width: 128, Height: 64})
	if err != nil {
		t.Fatalf("Build returned unexpected error: %v", err)
	}
	if bins.Cols != 2 || bins.Rows != 1 {
		t.Fatalf("grid = %dx%d, want 2x1", bins.Cols, bins.Rows)
	}
	if bins.TileSize != 64 {
		t.Fatalf("bins.TileSize = %d, want 64", bins.TileSize)
	}
	got := bins.Tiles[impl.TileKey{X: 0, Y: 0}]
	want := []impl.Handle{1, 2, 3}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tile (0,0) handles = %v, want %v", got, want)
	}
}

func TestBuildRoundsGridUpAndSkipsOutsideTiles(t *testing.T) {
	b := impl.NewInstanceTileBinner(64, 8)
	mustAdd(t, b, rect(70, 70, 80, 80))
	bins, err := b.Build(impl.Viewport{Width: 100, Height: 100})
	if err != nil {
		t.Fatalf("Build returned unexpected error: %v", err)
	}
	if bins.Cols != 2 || bins.Rows != 2 {
		t.Fatalf("grid = %dx%d, want 2x2 for a 100x100 viewport", bins.Cols, bins.Rows)
	}
	if len(bins.Tiles) != 1 {
		t.Fatalf("expected exactly one occupied tile, got %d", len(bins.Tiles))
	}
	got := bins.Tiles[impl.TileKey{X: 1, Y: 1}]
	if !reflect.DeepEqual(got, []impl.Handle{1}) {
		t.Fatalf("tile (1,1) handles = %v, want [1]", got)
	}
}

func TestBuildRejectsEmptyViewport(t *testing.T) {
	b := impl.NewInstanceTileBinner(64, 8)
	if _, err := b.Build(impl.Viewport{Width: 0, Height: 64}); !errors.Is(err, impl.ErrBadViewport) {
		t.Fatalf("zero width: got %v, want ErrBadViewport", err)
	}
	if _, err := b.Build(impl.Viewport{Width: 64, Height: -1}); !errors.Is(err, impl.ErrBadViewport) {
		t.Fatalf("negative height: got %v, want ErrBadViewport", err)
	}
}

func TestBuildIsRepeatable(t *testing.T) {
	b := impl.NewInstanceTileBinner(32, 8)
	mustAdd(t, b, rect(1, 1, 10, 10))
	mustAdd(t, b, rect(40, 8, 60, 20))
	view := impl.Viewport{Width: 128, Height: 128}
	first, err := b.Build(view)
	if err != nil {
		t.Fatalf("first Build returned unexpected error: %v", err)
	}
	second, err := b.Build(view)
	if err != nil {
		t.Fatalf("second Build returned unexpected error: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("two builds of the same table differ:\nfirst=%+v\nsecond=%+v", first, second)
	}
}

func TestBucketCapacityOverflowsIntoGlobalTable(t *testing.T) {
	b := impl.NewInstanceTileBinner(64, 2)
	for i := 0; i < 5; i++ {
		mustAdd(t, b, rect(float64(i), float64(i), float64(i)+1, float64(i)+1))
	}
	bins, err := b.Build(impl.Viewport{Width: 64, Height: 64})
	if err != nil {
		t.Fatalf("Build returned unexpected error: %v", err)
	}
	if got := len(bins.Tiles[impl.TileKey{X: 0, Y: 0}]); got != 2 {
		t.Fatalf("bucket (0,0) holds %d handles, want capacity 2", got)
	}
	if got := len(bins.Overflow); got != 3 {
		t.Fatalf("overflow table holds %d entries, want 3", got)
	}
	stats := b.Stats()
	if stats.Instances != 5 || stats.Overflow != 3 {
		t.Fatalf("stats = %+v, want 5 instances and 3 overflow entries", stats)
	}
}

func TestRemoveAfterBuildLeavesNoStaleHandle(t *testing.T) {
	b := impl.NewInstanceTileBinner(64, 4)
	h1 := mustAdd(t, b, rect(5, 5, 15, 15))
	mustAdd(t, b, rect(20, 20, 30, 30))
	if _, err := b.Build(impl.Viewport{Width: 64, Height: 64}); err != nil {
		t.Fatalf("first Build returned unexpected error: %v", err)
	}
	if err := b.Remove(h1); err != nil {
		t.Fatalf("Remove(%d) returned unexpected error: %v", h1, err)
	}
	bins, err := b.Build(impl.Viewport{Width: 64, Height: 64})
	if err != nil {
		t.Fatalf("second Build returned unexpected error: %v", err)
	}
	if got := bins.Tiles[impl.TileKey{X: 0, Y: 0}]; !reflect.DeepEqual(got, []impl.Handle{2}) {
		t.Fatalf("tile (0,0) handles after Remove = %v, want [2]", got)
	}
	if got := b.Stats().Instances; got != 1 {
		t.Fatalf("Stats().Instances = %d, want 1", got)
	}
}

func TestSnapshotRestoreKeepsInstanceTable(t *testing.T) {
	b := impl.NewInstanceTileBinner(48, 6)
	mustAdd(t, b, rect(4, 4, 20, 20))
	mustAdd(t, b, rect(60, 60, 90, 90))
	payload, err := b.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot returned unexpected error: %v", err)
	}
	fresh := impl.NewInstanceTileBinner(16, 1)
	if err := fresh.Restore(payload); err != nil {
		t.Fatalf("Restore returned unexpected error: %v", err)
	}
	if got := fresh.HandleCount(); got != 2 {
		t.Fatalf("HandleCount after Restore = %d, want 2", got)
	}
	if got, ok := fresh.BoundsOf(2); !ok || got != rect(60, 60, 90, 90) {
		t.Fatalf("BoundsOf(2) after Restore = %v ok=%v, want the saved bounds", got, ok)
	}
	freshBins, err := fresh.Build(impl.Viewport{Width: 96, Height: 96})
	if err != nil {
		t.Fatalf("Build after Restore returned unexpected error: %v", err)
	}
	if freshBins.TileSize != 48 {
		t.Fatalf("tile size after Restore = %d, want 48 from the snapshot", freshBins.TileSize)
	}
}

func TestRestoreRejectsCorruptPayload(t *testing.T) {
	b := impl.NewInstanceTileBinner(64, 4)
	mustAdd(t, b, rect(1, 1, 2, 2))
	if err := b.Restore([]byte("not a snapshot")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("Restore(garbage): got %v, want ErrBadSnapshot", err)
	}
	if got := b.HandleCount(); got != 1 {
		t.Fatalf("HandleCount after rejected Restore = %d, want 1", got)
	}
	if _, ok := b.BoundsOf(1); !ok {
		t.Fatalf("BoundsOf(1) lost after rejected Restore")
	}
}

func TestConcurrentAddsUseDistinctHandles(t *testing.T) {
	b := impl.NewInstanceTileBinner(64, 8)
	if err := b.SetInstanceLimit(128); err != nil {
		t.Fatalf("SetInstanceLimit(128) returned unexpected error: %v", err)
	}
	const workers = 32
	handles := make([]impl.Handle, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			handles[i], errs[i] = b.Add(impl.Instance{Bounds: rect(float64(i), float64(i), float64(i)+1, float64(i)+1)})
		}(i)
	}
	wg.Wait()
	seen := make(map[impl.Handle]bool, workers)
	for i, h := range handles {
		if errs[i] != nil {
			t.Fatalf("concurrent Add %d returned error: %v", i, errs[i])
		}
		if seen[h] {
			t.Fatalf("handle %d handed out twice under concurrency", h)
		}
		seen[h] = true
	}
	if got := b.HandleCount(); got != workers {
		t.Fatalf("HandleCount = %d, want %d", got, workers)
	}
}
