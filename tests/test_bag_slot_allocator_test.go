package task

import (
	"errors"
	"testing"

	impl "gsb/batch48-go-game-a-34"
)

func bagOptions() impl.Options {
	return impl.Options{DefaultW: 6, DefaultH: 4}
}

func registeredBag(t *testing.T) *impl.BagSlotAllocator {
	t.Helper()
	a := impl.NewBagSlotAllocator(bagOptions())
	if err := a.Register("p1", 6, 4); err != nil {
		t.Fatalf("register bag: unexpected error %v", err)
	}
	return a
}

func TestRegisterCreatesEmptyBag(t *testing.T) {
	a := impl.NewBagSlotAllocator(bagOptions())
	if err := a.Register("p1", 6, 4); err != nil {
		t.Fatalf("register bag: unexpected error %v", err)
	}
	used, total := a.Usage("p1")
	if used != 0 || total != 24 {
		t.Fatalf("usage = %d/%d, want 0/24", used, total)
	}
	if err := a.Register("p1", 6, 4); !errors.Is(err, impl.ErrBagExists) {
		t.Fatalf("duplicate register error = %v, want ErrBagExists", err)
	}
}

func TestInsertPlacesFirstItemAtOrigin(t *testing.T) {
	a := registeredBag(t)
	p, err := a.Insert("p1", impl.Item{ID: "bag-a", W: 2, H: 2})
	if err != nil {
		t.Fatalf("insert: unexpected error %v", err)
	}
	if p.X != 0 || p.Y != 0 || p.W != 2 || p.H != 2 || p.Rotated {
		t.Fatalf("placement = %+v, want 2x2 at the origin", p)
	}
	if used, total := a.Usage("p1"); used != 4 || total != 24 {
		t.Fatalf("usage = %d/%d, want 4/24", used, total)
	}
}

func TestInsertScansRowMajor(t *testing.T) {
	a := registeredBag(t)
	if _, err := a.Insert("p1", impl.Item{ID: "bag-a", W: 2, H: 2}); err != nil {
		t.Fatalf("insert first: unexpected error %v", err)
	}
	p, err := a.Insert("p1", impl.Item{ID: "bag-b", W: 1, H: 2})
	if err != nil {
		t.Fatalf("insert second: unexpected error %v", err)
	}
	if p.X != 2 || p.Y != 0 || p.W != 1 || p.H != 2 {
		t.Fatalf("placement = %+v, want 1x2 at 2,0", p)
	}
}

func TestInsertOversizeRejected(t *testing.T) {
	a := registeredBag(t)
	if _, err := a.Insert("p1", impl.Item{ID: "huge", W: 6, H: 5}); !errors.Is(err, impl.ErrItemTooLarge) {
		t.Fatalf("oversize error = %v, want ErrItemTooLarge", err)
	}
}

func TestInsertDuplicateIDRejected(t *testing.T) {
	a := registeredBag(t)
	if _, err := a.Insert("p1", impl.Item{ID: "bag-a", W: 1, H: 1}); err != nil {
		t.Fatalf("insert: unexpected error %v", err)
	}
	if _, err := a.Insert("p1", impl.Item{ID: "bag-a", W: 1, H: 1}); !errors.Is(err, impl.ErrDuplicateItem) {
		t.Fatalf("duplicate insert error = %v, want ErrDuplicateItem", err)
	}
}

func TestInsertUnknownPlayerRejected(t *testing.T) {
	a := impl.NewBagSlotAllocator(bagOptions())
	if _, err := a.Insert("ghost", impl.Item{ID: "bag-a", W: 1, H: 1}); !errors.Is(err, impl.ErrNoSuchBag) {
		t.Fatalf("unknown player error = %v, want ErrNoSuchBag", err)
	}
}

func TestFindDoesNotReserveCells(t *testing.T) {
	a := registeredBag(t)
	if _, err := a.Insert("p1", impl.Item{ID: "bag-a", W: 2, H: 2}); err != nil {
		t.Fatalf("insert: unexpected error %v", err)
	}
	p, ok := a.Find("p1", 4, 2)
	if !ok {
		t.Fatalf("find must report a 4x2 rectangle right of the first item")
	}
	if p.X != 2 || p.Y != 0 || p.W != 4 || p.H != 2 {
		t.Fatalf("find placement = %+v, want 4x2 at 2,0", p)
	}
	if again, ok := a.Find("p1", 4, 2); !ok || again != p {
		t.Fatalf("second find = %+v present=%v, want the same placement", again, ok)
	}
	if used, _ := a.Usage("p1"); used != 4 {
		t.Fatalf("used cells after find = %d, want 4 without reservation", used)
	}
}

func TestFindReportsNoSpaceWhenFull(t *testing.T) {
	a := registeredBag(t)
	if _, err := a.Insert("p1", impl.Item{ID: "full", W: 6, H: 4}); err != nil {
		t.Fatalf("insert full grid: unexpected error %v", err)
	}
	if _, ok := a.Find("p1", 1, 1); ok {
		t.Fatalf("find must report no space in a full bag")
	}
}

func TestRemoveFreesCells(t *testing.T) {
	a := registeredBag(t)
	if _, err := a.Insert("p1", impl.Item{ID: "bag-a", W: 2, H: 2}); err != nil {
		t.Fatalf("insert first: unexpected error %v", err)
	}
	if _, err := a.Insert("p1", impl.Item{ID: "bag-b", W: 2, H: 2}); err != nil {
		t.Fatalf("insert second: unexpected error %v", err)
	}
	if err := a.Remove("p1", "bag-a"); err != nil {
		t.Fatalf("remove: unexpected error %v", err)
	}
	p, err := a.Insert("p1", impl.Item{ID: "bag-c", W: 2, H: 2})
	if err != nil {
		t.Fatalf("insert after remove: unexpected error %v", err)
	}
	if p.X != 0 || p.Y != 0 {
		t.Fatalf("placement after remove = %+v, want the freed origin cells", p)
	}
}

func TestRemoveUnknownItemRejected(t *testing.T) {
	a := registeredBag(t)
	if err := a.Remove("p1", "ghost"); !errors.Is(err, impl.ErrNoSuchItem) {
		t.Fatalf("unknown item error = %v, want ErrNoSuchItem", err)
	}
}

func TestMoveRejectsOverlap(t *testing.T) {
	a := registeredBag(t)
	if _, err := a.Insert("p1", impl.Item{ID: "bag-a", W: 2, H: 2}); err != nil {
		t.Fatalf("insert first: unexpected error %v", err)
	}
	if _, err := a.Insert("p1", impl.Item{ID: "bag-b", W: 2, H: 2}); err != nil {
		t.Fatalf("insert second: unexpected error %v", err)
	}
	if err := a.Move("p1", "bag-b", impl.Placement{X: 0, Y: 0}); !errors.Is(err, impl.ErrSlotOccupied) {
		t.Fatalf("overlap move error = %v, want ErrSlotOccupied", err)
	}
	p, ok := a.ItemPlacement("p1", "bag-b")
	if !ok || p.X != 2 || p.Y != 0 {
		t.Fatalf("placement after rejected move = %+v present=%v, want unchanged at 2,0", p, ok)
	}
}

func TestMoveToFreeCells(t *testing.T) {
	a := registeredBag(t)
	if _, err := a.Insert("p1", impl.Item{ID: "bag-a", W: 2, H: 2}); err != nil {
		t.Fatalf("insert first: unexpected error %v", err)
	}
	if _, err := a.Insert("p1", impl.Item{ID: "bag-b", W: 2, H: 2}); err != nil {
		t.Fatalf("insert second: unexpected error %v", err)
	}
	if err := a.Move("p1", "bag-b", impl.Placement{X: 0, Y: 2}); err != nil {
		t.Fatalf("move: unexpected error %v", err)
	}
	p, ok := a.ItemPlacement("p1", "bag-b")
	if !ok || p.X != 0 || p.Y != 2 {
		t.Fatalf("placement after move = %+v present=%v, want 2x2 at 0,2", p, ok)
	}
	if used, _ := a.Usage("p1"); used != 8 {
		t.Fatalf("used cells after move = %d, want 8", used)
	}
	if id, ok := a.At("p1", 2, 0); ok {
		t.Fatalf("cell 2,0 still holds %q after the move", id)
	}
}

func TestUsageCountsCells(t *testing.T) {
	a := registeredBag(t)
	if _, err := a.Insert("p1", impl.Item{ID: "bag-a", W: 2, H: 2}); err != nil {
		t.Fatalf("insert 2x2: unexpected error %v", err)
	}
	if _, err := a.Insert("p1", impl.Item{ID: "bag-b", W: 1, H: 2}); err != nil {
		t.Fatalf("insert 1x2: unexpected error %v", err)
	}
	used, total := a.Usage("p1")
	if used != 6 || total != 24 {
		t.Fatalf("usage = %d/%d, want 6/24", used, total)
	}
	if id, ok := a.At("p1", 2, 1); !ok || id != "bag-b" {
		t.Fatalf("cell 2,1 = %q present=%v, want bag-b", id, ok)
	}
}

func TestSnapshotRestoreKeepsBag(t *testing.T) {
	a := registeredBag(t)
	if _, err := a.Insert("p1", impl.Item{ID: "bag-a", W: 2, H: 2}); err != nil {
		t.Fatalf("insert first: unexpected error %v", err)
	}
	if _, err := a.Insert("p1", impl.Item{ID: "bag-b", W: 1, H: 2}); err != nil {
		t.Fatalf("insert second: unexpected error %v", err)
	}
	snap, err := a.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: unexpected error %v", err)
	}
	restored := impl.NewBagSlotAllocator(impl.Options{})
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restore: unexpected error %v", err)
	}
	if used, total := restored.Usage("p1"); used != 6 || total != 24 {
		t.Fatalf("restored usage = %d/%d, want 6/24", used, total)
	}
	p, ok := restored.ItemPlacement("p1", "bag-a")
	if !ok || p.X != 0 || p.Y != 0 || p.W != 2 || p.H != 2 {
		t.Fatalf("restored placement = %+v present=%v, want 2x2 at 0,0", p, ok)
	}
	if err := restored.Remove("p1", "bag-a"); err != nil {
		t.Fatalf("remove after restore: unexpected error %v", err)
	}
	if used, _ := restored.Usage("p1"); used != 2 {
		t.Fatalf("used cells after removing restored item = %d, want 2", used)
	}
	if err := restored.Restore([]byte("not-a-bag")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("garbage restore error = %v, want ErrBadSnapshot", err)
	}
}

func TestCompactPreservesItems(t *testing.T) {
	a := registeredBag(t)
	for i, id := range []string{"bag-a", "bag-b", "bag-c"} {
		if _, err := a.Insert("p1", impl.Item{ID: id, W: 2, H: 2}); err != nil {
			t.Fatalf("insert %d: unexpected error %v", i, err)
		}
	}
	if err := a.Remove("p1", "bag-b"); err != nil {
		t.Fatalf("remove middle: unexpected error %v", err)
	}
	moved, err := a.Compact("p1")
	if err != nil {
		t.Fatalf("compact: unexpected error %v", err)
	}
	if moved < 1 {
		t.Fatalf("compact moved %d items, want at least the item behind the hole", moved)
	}
	items := a.Items("p1")
	if len(items) != 2 || items[0] != "bag-a" || items[1] != "bag-c" {
		t.Fatalf("items after compact = %v, want [bag-a bag-c]", items)
	}
	if used, total := a.Usage("p1"); used != 8 || total != 24 {
		t.Fatalf("usage after compact = %d/%d, want 8/24", used, total)
	}
	for _, id := range items {
		if _, ok := a.ItemPlacement("p1", id); !ok {
			t.Fatalf("item %q lost its placement after compact", id)
		}
	}
}
