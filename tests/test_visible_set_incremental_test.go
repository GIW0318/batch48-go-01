package task

import (
	"errors"
	"sync"
	"testing"

	impl "gsb/batch48-go-game-b-02"
)

func ids(values ...uint32) []impl.ObjectID {
	out := make([]impl.ObjectID, len(values))
	for i, v := range values {
		out[i] = impl.ObjectID(v)
	}
	return out
}

func sameIDs(got []impl.ObjectID, want []impl.ObjectID) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestSubscribeValidatesCellsAndStoresState(t *testing.T) {
	x := impl.NewVisibleSetIndex(impl.Limits{})
	if err := x.Bind("c1", ids(1)); err != nil {
		t.Fatalf("binding c1 must succeed, got %v", err)
	}
	if err := x.Bind("c2", ids(2)); err != nil {
		t.Fatalf("binding c2 must succeed, got %v", err)
	}
	if err := x.Subscribe("s1", []string{"c2", "c1"}); err != nil {
		t.Fatalf("subscribing must succeed, got %v", err)
	}
	st, ok := x.Subscriber("s1")
	if !ok {
		t.Fatalf("the subscriber must be readable")
	}
	if st.ID != "s1" || st.Filter != impl.AllMask || st.Version != 0 {
		t.Fatalf("fresh subscriber = %+v, want id s1, all-bits filter and version 0", st)
	}
	if len(st.Cells) != 2 || st.Cells[0] != "c1" || st.Cells[1] != "c2" {
		t.Fatalf("subscriber cells = %v, want the sorted pair c1, c2", st.Cells)
	}
	if err := x.Subscribe("s1", []string{"c1"}); !errors.Is(err, impl.ErrDuplicateSub) {
		t.Fatalf("a duplicate subscriber must report ErrDuplicateSub, got %v", err)
	}
	if err := x.Subscribe("", []string{"c1"}); !errors.Is(err, impl.ErrBadSub) {
		t.Fatalf("an empty subscriber id must report ErrBadSub, got %v", err)
	}
	if err := x.Subscribe("s2", nil); !errors.Is(err, impl.ErrBadSub) {
		t.Fatalf("a subscriber without cells must report ErrBadSub, got %v", err)
	}
	if err := x.Subscribe("s3", []string{"c9"}); !errors.Is(err, impl.ErrNoSuchCell) {
		t.Fatalf("subscribing to an unbound cell must report ErrNoSuchCell, got %v", err)
	}
	if cells := x.Cells(); len(cells) != 2 || cells[0] != "c1" || cells[1] != "c2" {
		t.Fatalf("cells = %v, want the sorted pair c1, c2", cells)
	}
}

func TestCellAndSubscriberLimitsAreEnforced(t *testing.T) {
	x := impl.NewVisibleSetIndex(impl.Limits{MaxCells: 2, MaxSubs: 2, MaxObjectsPerCell: 4})
	if err := x.Bind("c1", nil); err != nil {
		t.Fatalf("binding c1 must succeed, got %v", err)
	}
	if err := x.Bind("c2", nil); err != nil {
		t.Fatalf("binding c2 must succeed, got %v", err)
	}
	if err := x.Bind("c3", nil); !errors.Is(err, impl.ErrCellLimit) {
		t.Fatalf("binding a third cell must report ErrCellLimit, got %v", err)
	}
	if err := x.Bind("c1", ids(1)); err != nil {
		t.Fatalf("rebinding an existing cell must not be limited, got %v", err)
	}
	if err := x.Subscribe("s1", []string{"c1"}); err != nil {
		t.Fatalf("first subscriber must succeed, got %v", err)
	}
	if err := x.Subscribe("s2", []string{"c2"}); err != nil {
		t.Fatalf("second subscriber must succeed, got %v", err)
	}
	if err := x.Subscribe("s3", []string{"c1"}); !errors.Is(err, impl.ErrSubLimit) {
		t.Fatalf("a third subscriber must report ErrSubLimit, got %v", err)
	}
	if err := x.Unsubscribe("s1"); err != nil {
		t.Fatalf("unsubscribing must succeed, got %v", err)
	}
	if err := x.Subscribe("s3", []string{"c1"}); err != nil {
		t.Fatalf("unsubscribing must free a subscriber slot, got %v", err)
	}
}

func TestBindReplacesCellContents(t *testing.T) {
	x := impl.NewVisibleSetIndex(impl.Limits{})
	if err := x.Bind("c1", ids(5, 3, 1)); err != nil {
		t.Fatalf("binding must succeed, got %v", err)
	}
	if got := x.Objects("c1"); !sameIDs(got, ids(1, 3, 5)) {
		t.Fatalf("objects = %v, want the ascending run 1, 3, 5", got)
	}
	if got := x.Objects("missing"); len(got) != 0 {
		t.Fatalf("an unbound cell must list nothing, got %v", got)
	}
	if err := x.Bind("c1", ids(9, 3)); err != nil {
		t.Fatalf("rebinding must succeed, got %v", err)
	}
	if got := x.Objects("c1"); !sameIDs(got, ids(3, 9)) {
		t.Fatalf("objects after rebind = %v, want 3, 9", got)
	}
	if err := x.Bind("c1", nil); err != nil {
		t.Fatalf("clearing a cell must succeed, got %v", err)
	}
	if got := x.Objects("c1"); len(got) != 0 {
		t.Fatalf("a cleared cell must list nothing, got %v", got)
	}
	if err := x.Bind("", ids(1)); !errors.Is(err, impl.ErrBadCell) {
		t.Fatalf("an empty cell id must report ErrBadCell, got %v", err)
	}
}

func TestObjectLimitAndDuplicateIdsAreHandled(t *testing.T) {
	x := impl.NewVisibleSetIndex(impl.Limits{MaxObjectsPerCell: 3})
	if err := x.Bind("c1", ids(1, 2, 3)); err != nil {
		t.Fatalf("binding at the limit must succeed, got %v", err)
	}
	if err := x.Bind("c1", ids(1, 2, 3, 4)); !errors.Is(err, impl.ErrObjectLimit) {
		t.Fatalf("binding above the limit must report ErrObjectLimit, got %v", err)
	}
	if got := x.Objects("c1"); !sameIDs(got, ids(1, 2, 3)) {
		t.Fatalf("a rejected bind must leave the cell alone, got %v", got)
	}
	if err := x.Bind("c2", ids(7, 7, 8)); err != nil {
		t.Fatalf("duplicate ids inside the limit must be accepted, got %v", err)
	}
	if got := x.Objects("c2"); !sameIDs(got, ids(7, 8)) {
		t.Fatalf("duplicate ids must collapse, got %v", got)
	}
}

func TestDiffReportsAddedObjectsInAscendingOrder(t *testing.T) {
	x := impl.NewVisibleSetIndex(impl.Limits{})
	if err := x.Bind("c1", nil); err != nil {
		t.Fatalf("binding must succeed, got %v", err)
	}
	if err := x.Subscribe("s1", []string{"c1"}); err != nil {
		t.Fatalf("subscribing must succeed, got %v", err)
	}
	if err := x.Bind("c1", ids(3, 1, 2)); err != nil {
		t.Fatalf("binding must succeed, got %v", err)
	}
	added, removed, err := x.Diff("s1")
	if err != nil {
		t.Fatalf("diff must succeed, got %v", err)
	}
	if !sameIDs(added, ids(1, 2, 3)) {
		t.Fatalf("added = %v, want the ascending run 1, 2, 3", added)
	}
	if len(removed) != 0 {
		t.Fatalf("removed = %v, want nothing", removed)
	}
	if got := x.Version("s1"); got != 1 {
		t.Fatalf("version = %d, want 1 after one change", got)
	}
}

func TestDiffIsMeasuredAgainstTheAckedBaseline(t *testing.T) {
	x := impl.NewVisibleSetIndex(impl.Limits{})
	if err := x.Bind("c1", nil); err != nil {
		t.Fatalf("binding must succeed, got %v", err)
	}
	if err := x.Subscribe("s1", []string{"c1"}); err != nil {
		t.Fatalf("subscribing must succeed, got %v", err)
	}
	if err := x.Bind("c1", ids(1, 2)); err != nil {
		t.Fatalf("binding must succeed, got %v", err)
	}
	if _, _, err := x.Diff("s1"); err != nil {
		t.Fatalf("diff must succeed, got %v", err)
	}
	if err := x.Ack("s1", 1); err != nil {
		t.Fatalf("acking the current version must succeed, got %v", err)
	}
	added, removed, err := x.Diff("s1")
	if err != nil {
		t.Fatalf("diff after ack must succeed, got %v", err)
	}
	if len(added) != 0 || len(removed) != 0 {
		t.Fatalf("diff after ack = %v / %v, want nothing", added, removed)
	}
	if err := x.Bind("c1", ids(2, 3)); err != nil {
		t.Fatalf("rebinding must succeed, got %v", err)
	}
	added, removed, err = x.Diff("s1")
	if err != nil {
		t.Fatalf("diff must succeed, got %v", err)
	}
	if !sameIDs(added, ids(3)) || !sameIDs(removed, ids(1)) {
		t.Fatalf("delta = added %v / removed %v, want added 3 and removed 1", added, removed)
	}
	if got := x.Version("s1"); got != 2 {
		t.Fatalf("version = %d, want 2", got)
	}
	if err := x.Ack("s1", 1); !errors.Is(err, impl.ErrStaleVersion) {
		t.Fatalf("acking an old version must report ErrStaleVersion, got %v", err)
	}
	if err := x.Ack("s1", 2); err != nil {
		t.Fatalf("acking the current version must succeed, got %v", err)
	}
}

func TestDiffAccumulatesUntilTheSubscriberAcks(t *testing.T) {
	x := impl.NewVisibleSetIndex(impl.Limits{})
	if err := x.Bind("c1", nil); err != nil {
		t.Fatalf("binding must succeed, got %v", err)
	}
	if err := x.Subscribe("s1", []string{"c1"}); err != nil {
		t.Fatalf("subscribing must succeed, got %v", err)
	}
	if err := x.Bind("c1", ids(1)); err != nil {
		t.Fatalf("binding must succeed, got %v", err)
	}
	added, _, err := x.Diff("s1")
	if err != nil || !sameIDs(added, ids(1)) {
		t.Fatalf("first delta = %v err %v, want added 1", added, err)
	}
	if err := x.Bind("c1", ids(2)); err != nil {
		t.Fatalf("rebinding must succeed, got %v", err)
	}
	added, removed, err := x.Diff("s1")
	if err != nil {
		t.Fatalf("diff must succeed, got %v", err)
	}
	if !sameIDs(added, ids(2)) || !sameIDs(removed, ids(1)) {
		t.Fatalf("delta = added %v / removed %v, want added 2 and removed 1", added, removed)
	}
	if got := x.Version("s1"); got != 2 {
		t.Fatalf("version = %d, want 2 while nothing was acked", got)
	}
	if err := x.Ack("s1", 2); err != nil {
		t.Fatalf("acking must succeed, got %v", err)
	}
	added, removed, err = x.Diff("s1")
	if err != nil || len(added) != 0 || len(removed) != 0 {
		t.Fatalf("delta after ack = %v / %v err %v, want nothing", added, removed, err)
	}
}

func TestDiffAndAckRejectUnknownSubscribers(t *testing.T) {
	x := impl.NewVisibleSetIndex(impl.Limits{})
	if _, _, err := x.Diff("nobody"); !errors.Is(err, impl.ErrNoSuchSub) {
		t.Fatalf("diff for an unknown subscriber must report ErrNoSuchSub, got %v", err)
	}
	if err := x.Ack("nobody", 0); !errors.Is(err, impl.ErrNoSuchSub) {
		t.Fatalf("ack for an unknown subscriber must report ErrNoSuchSub, got %v", err)
	}
	if err := x.Unsubscribe("nobody"); !errors.Is(err, impl.ErrNoSuchSub) {
		t.Fatalf("unsubscribing an unknown id must report ErrNoSuchSub, got %v", err)
	}
	if err := x.Bind("c1", nil); err != nil {
		t.Fatalf("binding must succeed, got %v", err)
	}
	if err := x.Subscribe("s1", []string{"c1"}); err != nil {
		t.Fatalf("subscribing must succeed, got %v", err)
	}
	if err := x.Unsubscribe("s1"); err != nil {
		t.Fatalf("unsubscribing must succeed, got %v", err)
	}
	if _, _, err := x.Diff("s1"); !errors.Is(err, impl.ErrNoSuchSub) {
		t.Fatalf("diff after unsubscribe must report ErrNoSuchSub, got %v", err)
	}
}

func TestFilterIsAppliedWhenObjectsArrive(t *testing.T) {
	x := impl.NewVisibleSetIndex(impl.Limits{})
	if err := x.Bind("c1", nil); err != nil {
		t.Fatalf("binding must succeed, got %v", err)
	}
	if err := x.Subscribe("s1", []string{"c1"}); err != nil {
		t.Fatalf("subscribing must succeed, got %v", err)
	}
	if v, err := x.SetFilter("s1", 0b0010); err != nil || v != 0 {
		t.Fatalf("setting the filter = %d err %v, want version 0 on an empty subscription", v, err)
	}
	if err := x.Classify(1, 0b0001); err != nil {
		t.Fatalf("classifying must succeed, got %v", err)
	}
	if err := x.Classify(2, 0b0010); err != nil {
		t.Fatalf("classifying must succeed, got %v", err)
	}
	if err := x.Bind("c1", ids(1, 2)); err != nil {
		t.Fatalf("binding must succeed, got %v", err)
	}
	added, removed, err := x.Diff("s1")
	if err != nil {
		t.Fatalf("diff must succeed, got %v", err)
	}
	if !sameIDs(added, ids(2)) {
		t.Fatalf("added = %v, want only the object that matches the filter", added)
	}
	if len(removed) != 0 {
		t.Fatalf("removed = %v, want nothing", removed)
	}
}

func TestSetFilterValidatesAndReportsTheVersion(t *testing.T) {
	x := impl.NewVisibleSetIndex(impl.Limits{})
	if err := x.Bind("c1", ids(1)); err != nil {
		t.Fatalf("binding must succeed, got %v", err)
	}
	if err := x.Subscribe("s1", []string{"c1"}); err != nil {
		t.Fatalf("subscribing must succeed, got %v", err)
	}
	if _, err := x.SetFilter("nobody", 1); !errors.Is(err, impl.ErrNoSuchSub) {
		t.Fatalf("setting the filter of an unknown subscriber must report ErrNoSuchSub, got %v", err)
	}
	version, err := x.SetFilter("s1", 0b0010)
	if err != nil {
		t.Fatalf("setting the filter must succeed, got %v", err)
	}
	if got := x.Version("s1"); version != got {
		t.Fatalf("SetFilter returned version %d, current version is %d", version, got)
	}
	again, err := x.SetFilter("s1", 0b0010)
	if err != nil {
		t.Fatalf("repeating the same filter must succeed, got %v", err)
	}
	if again != version {
		t.Fatalf("repeating the same filter moved the version from %d to %d", version, again)
	}
}

func TestMoveKeepsBothCellListsConsistent(t *testing.T) {
	x := impl.NewVisibleSetIndex(impl.Limits{})
	if err := x.Bind("c1", ids(1, 2)); err != nil {
		t.Fatalf("binding c1 must succeed, got %v", err)
	}
	if err := x.Bind("c2", ids(3)); err != nil {
		t.Fatalf("binding c2 must succeed, got %v", err)
	}
	if err := x.Move(2, "c1", "c2"); err != nil {
		t.Fatalf("moving an object must succeed, got %v", err)
	}
	if got := x.Objects("c1"); !sameIDs(got, ids(1)) {
		t.Fatalf("source cell = %v, want 1", got)
	}
	if got := x.Objects("c2"); !sameIDs(got, ids(2, 3)) {
		t.Fatalf("target cell = %v, want 2, 3", got)
	}
	if err := x.Move(2, "c1", "c2"); !errors.Is(err, impl.ErrNotInCell) {
		t.Fatalf("moving an object that left the source must report ErrNotInCell, got %v", err)
	}
	if err := x.Move(99, "c1", "c2"); !errors.Is(err, impl.ErrNoSuchObject) {
		t.Fatalf("moving an unknown object must report ErrNoSuchObject, got %v", err)
	}
	if err := x.Move(1, "c1", "c1"); err != nil {
		t.Fatalf("moving an object inside its own cell must be a no-op, got %v", err)
	}
	if err := x.Move(1, "c1", "c9"); !errors.Is(err, impl.ErrNoSuchCell) {
		t.Fatalf("moving to an unbound cell must report ErrNoSuchCell, got %v", err)
	}
	if got := x.Objects("c1"); !sameIDs(got, ids(1)) {
		t.Fatalf("a rejected move must leave the source alone, got %v", got)
	}
}

func TestMoveStopsAtTheTargetCellLimit(t *testing.T) {
	x := impl.NewVisibleSetIndex(impl.Limits{MaxObjectsPerCell: 2})
	if err := x.Bind("c1", ids(1, 2)); err != nil {
		t.Fatalf("binding c1 must succeed, got %v", err)
	}
	if err := x.Bind("c2", ids(3, 4)); err != nil {
		t.Fatalf("binding c2 must succeed, got %v", err)
	}
	if err := x.Move(1, "c1", "c2"); !errors.Is(err, impl.ErrObjectLimit) {
		t.Fatalf("moving into a full cell must report ErrObjectLimit, got %v", err)
	}
	if got := x.Objects("c1"); !sameIDs(got, ids(1, 2)) {
		t.Fatalf("a rejected move must leave the source alone, got %v", got)
	}
	if got := x.Objects("c2"); !sameIDs(got, ids(3, 4)) {
		t.Fatalf("a rejected move must leave the target alone, got %v", got)
	}
}

func TestClassifyStoresTheObjectMask(t *testing.T) {
	x := impl.NewVisibleSetIndex(impl.Limits{})
	if err := x.Classify(1, 0b0100); err != nil {
		t.Fatalf("classifying must succeed, got %v", err)
	}
	mask, ok := x.Mask(1)
	if !ok || mask != 0b0100 {
		t.Fatalf("mask = %d ok %v, want 0b0100", mask, ok)
	}
	if err := x.Classify(1, 0); err != nil {
		t.Fatalf("clearing the mask must succeed, got %v", err)
	}
	if mask, ok := x.Mask(1); !ok || mask != 0 {
		t.Fatalf("cleared mask = %d ok %v, want 0", mask, ok)
	}
	if _, ok := x.Mask(77); ok {
		t.Fatalf("an unclassified object must not report a mask")
	}
}

func TestSnapshotRestoreKeepsCellsFiltersAndBaselines(t *testing.T) {
	x := impl.NewVisibleSetIndex(impl.Limits{})
	if err := x.Bind("c1", ids(1, 2)); err != nil {
		t.Fatalf("binding must succeed, got %v", err)
	}
	if err := x.Subscribe("s1", []string{"c1"}); err != nil {
		t.Fatalf("subscribing must succeed, got %v", err)
	}
	if err := x.Bind("c1", ids(1, 2, 3)); err != nil {
		t.Fatalf("rebinding must succeed, got %v", err)
	}
	data, err := x.Snapshot()
	if err != nil {
		t.Fatalf("snapshot must succeed, got %v", err)
	}
	restored := impl.NewVisibleSetIndex(impl.Limits{})
	if err := restored.Restore(data); err != nil {
		t.Fatalf("restore must succeed, got %v", err)
	}
	if got := restored.Objects("c1"); !sameIDs(got, ids(1, 2, 3)) {
		t.Fatalf("restored cell = %v, want 1, 2, 3", got)
	}
	st, ok := restored.Subscriber("s1")
	if !ok || st.Version != 1 || st.Filter != impl.AllMask {
		t.Fatalf("restored subscriber = %+v ok %v, want version 1 and the all-bits filter", st, ok)
	}
	added, removed, err := restored.Diff("s1")
	if err != nil {
		t.Fatalf("diff after restore must succeed, got %v", err)
	}
	if !sameIDs(added, ids(3)) || len(removed) != 0 {
		t.Fatalf("restored delta = added %v / removed %v, want the unacked object 3", added, removed)
	}
	if err := restored.Ack("s1", 1); err != nil {
		t.Fatalf("acking after restore must succeed, got %v", err)
	}
	if err := restored.Restore([]byte("{")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("a broken snapshot must report ErrBadSnapshot, got %v", err)
	}
	if got := restored.Objects("c1"); !sameIDs(got, ids(1, 2, 3)) {
		t.Fatalf("a rejected snapshot must leave the index alone, got %v", got)
	}
}

func TestConcurrentBindAndDiffStayConsistent(t *testing.T) {
	x := impl.NewVisibleSetIndex(impl.Limits{})
	cells := []string{"k0", "k1", "k2", "k3"}
	for i, cell := range cells {
		if err := x.Bind(cell, nil); err != nil {
			t.Fatalf("binding %s must succeed, got %v", cell, err)
		}
		if err := x.Subscribe("s"+string(rune('0'+i)), []string{cell}); err != nil {
			t.Fatalf("subscribing to %s must succeed, got %v", cell, err)
		}
	}
	var wg sync.WaitGroup
	for i, cell := range cells {
		wg.Add(1)
		go func(slot int, name string) {
			defer wg.Done()
			batch := make([]impl.ObjectID, 10)
			for j := range batch {
				batch[j] = impl.ObjectID(slot*100 + j)
			}
			if err := x.Bind(name, batch); err != nil {
				t.Errorf("concurrent bind of %s failed: %v", name, err)
			}
		}(i, cell)
	}
	wg.Wait()
	for i := range cells {
		added, removed, err := x.Diff("s" + string(rune('0'+i)))
		if err != nil {
			t.Fatalf("diff must succeed, got %v", err)
		}
		if len(added) != 10 || len(removed) != 0 {
			t.Fatalf("subscriber %d got %d added / %d removed, want 10 / 0", i, len(added), len(removed))
		}
		if added[0] != impl.ObjectID(i*100) || added[9] != impl.ObjectID(i*100+9) {
			t.Fatalf("subscriber %d got %v, want its own ascending batch", i, added)
		}
	}
	var mu sync.Mutex
	var first, second []impl.ObjectID
	wg.Add(2)
	go func() {
		defer wg.Done()
		added, _, _ := x.Diff("s0")
		mu.Lock()
		first = added
		mu.Unlock()
	}()
	go func() {
		defer wg.Done()
		added, _, _ := x.Diff("s0")
		mu.Lock()
		second = added
		mu.Unlock()
	}()
	wg.Wait()
	if !sameIDs(first, second) || len(first) != 10 {
		t.Fatalf("concurrent diffs returned %v and %v, want the same ten objects", first, second)
	}
}
