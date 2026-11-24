package task

import (
	"errors"
	"reflect"
	"testing"

	impl "gsb/batch48-go-game-a-30"
)

func newLoot(pityLimit int) *impl.LootTableRoller {
	return impl.NewLootTableRoller(impl.Options{LuckGroup: "rare", PityLimit: pityLimit})
}

func coinEntries() []impl.Entry {
	return []impl.Entry{{ID: "coin", Weight: 10, Min: 1, Max: 3, Group: "common"}}
}

func loadTable(t *testing.T, g *impl.LootTableRoller, id string, entries []impl.Entry) {
	t.Helper()
	if err := g.LoadTable(id, entries); err != nil {
		t.Fatalf("LoadTable(%s): %v", id, err)
	}
}

func TestLoadTableRejectsBadID(t *testing.T) {
	g := newLoot(5)
	if err := g.LoadTable("", coinEntries()); !errors.Is(err, impl.ErrInvalidTable) {
		t.Fatalf("empty table id error = %v, want ErrInvalidTable", err)
	}
}

func TestLoadTableRejectsEmptyTable(t *testing.T) {
	g := newLoot(5)
	if err := g.LoadTable("t1", nil); !errors.Is(err, impl.ErrEmptyTable) {
		t.Fatalf("empty entry list error = %v, want ErrEmptyTable", err)
	}
}

func TestLoadTableRejectsBadEntry(t *testing.T) {
	g := newLoot(5)
	cases := [][]impl.Entry{
		{{ID: "", Weight: 1, Min: 1, Max: 1}},
		{{ID: "coin", Weight: -1, Min: 1, Max: 1}},
		{{ID: "coin", Weight: 1, Min: 0, Max: 1}},
		{{ID: "coin", Weight: 1, Min: 3, Max: 2}},
		{{ID: "coin", Weight: 0, Min: 1, Max: 1}},
	}
	for i, entries := range cases {
		if err := g.LoadTable("t1", entries); !errors.Is(err, impl.ErrInvalidEntry) {
			t.Fatalf("case %d error = %v, want ErrInvalidEntry", i, err)
		}
	}
}

func TestLoadTableRejectsDuplicateEntry(t *testing.T) {
	g := newLoot(5)
	entries := []impl.Entry{
		{ID: "coin", Weight: 1, Min: 1, Max: 1},
		{ID: "coin", Weight: 2, Min: 1, Max: 2},
	}
	if err := g.LoadTable("t1", entries); !errors.Is(err, impl.ErrDuplicateEntry) {
		t.Fatalf("duplicate entry error = %v, want ErrDuplicateEntry", err)
	}
}

func TestRollRejectsUnknownTable(t *testing.T) {
	g := newLoot(5)
	if _, err := g.Roll(impl.RollRequest{Key: "k1", Player: "p1", Table: "missing"}); !errors.Is(err, impl.ErrUnknownTable) {
		t.Fatalf("roll on unknown table error = %v, want ErrUnknownTable", err)
	}
}

func TestRollRejectsBadRequest(t *testing.T) {
	g := newLoot(5)
	loadTable(t, g, "t1", coinEntries())
	if _, err := g.Roll(impl.RollRequest{Key: "", Player: "p1", Table: "t1"}); !errors.Is(err, impl.ErrInvalidRequest) {
		t.Fatalf("empty request key error = %v, want ErrInvalidRequest", err)
	}
	if _, err := g.Roll(impl.RollRequest{Key: "k1", Player: "", Table: "t1"}); !errors.Is(err, impl.ErrInvalidRequest) {
		t.Fatalf("empty player error = %v, want ErrInvalidRequest", err)
	}
}

func TestRollProducesItemInRange(t *testing.T) {
	g := newLoot(5)
	loadTable(t, g, "t1", coinEntries())
	drop, err := g.Roll(impl.RollRequest{Key: "kill-1", Player: "p1", Table: "t1"})
	if err != nil {
		t.Fatalf("Roll: %v", err)
	}
	if drop.Key != "kill-1" {
		t.Fatalf("drop key = %q, want kill-1", drop.Key)
	}
	if len(drop.Items) != 1 {
		t.Fatalf("drop items = %d, want 1", len(drop.Items))
	}
	item := drop.Items[0]
	if item.ID != "coin" {
		t.Fatalf("item id = %q, want coin", item.ID)
	}
	if item.Count < 1 || item.Count > 3 {
		t.Fatalf("item count = %d, want a value in 1..3", item.Count)
	}
}

func TestRollIsDeterministicPerKey(t *testing.T) {
	a := newLoot(5)
	b := newLoot(5)
	loadTable(t, a, "t1", coinEntries())
	loadTable(t, b, "t1", coinEntries())
	req := impl.RollRequest{Key: "kill-42", Player: "p1", Table: "t1"}
	first, err := a.Roll(req)
	if err != nil {
		t.Fatalf("first Roll: %v", err)
	}
	second, err := b.Roll(req)
	if err != nil {
		t.Fatalf("second Roll: %v", err)
	}
	if !reflect.DeepEqual(first.Items, second.Items) {
		t.Fatalf("same key produced %+v and %+v on two rollers", first.Items, second.Items)
	}
	again, err := a.Roll(req)
	if err != nil {
		t.Fatalf("repeated Roll: %v", err)
	}
	if !reflect.DeepEqual(first.Items, again.Items) {
		t.Fatalf("same key produced %+v then %+v on one roller", first.Items, again.Items)
	}
}

func TestZeroWeightEntryNeverDrawn(t *testing.T) {
	g := newLoot(0)
	entries := []impl.Entry{
		{ID: "ghost", Weight: 0, Min: 1, Max: 1},
		{ID: "coin", Weight: 10, Min: 1, Max: 1},
	}
	loadTable(t, g, "t1", entries)
	for i := 0; i < 20; i++ {
		drop, err := g.Roll(impl.RollRequest{Key: "k" + string(rune('a'+i)), Player: "p1", Table: "t1"})
		if err != nil {
			t.Fatalf("Roll %d: %v", i, err)
		}
		for _, item := range drop.Items {
			if item.ID == "ghost" {
				t.Fatalf("zero weight entry was drawn on request %d", i)
			}
		}
	}
}

func TestLuckOnlyAffectsConfiguredGroup(t *testing.T) {
	g := newLoot(5)
	entries := []impl.Entry{
		{ID: "common-drop", Weight: 50, Min: 1, Max: 1, Group: "common"},
		{ID: "rare-drop", Weight: 50, Min: 1, Max: 1, Group: "rare"},
	}
	loadTable(t, g, "t1", entries)
	weights, err := g.EffectiveWeights("t1", 100)
	if err != nil {
		t.Fatalf("EffectiveWeights: %v", err)
	}
	if weights["common-drop"] != 50 {
		t.Fatalf("common weight = %d, want the untouched 50", weights["common-drop"])
	}
	if weights["rare-drop"] != 100 {
		t.Fatalf("rare weight with luck 100 = %d, want 100", weights["rare-drop"])
	}
	weights, err = g.EffectiveWeights("t1", -50)
	if err != nil {
		t.Fatalf("EffectiveWeights with negative luck: %v", err)
	}
	if weights["rare-drop"] != 50 {
		t.Fatalf("rare weight with negative luck = %d, want the base 50", weights["rare-drop"])
	}
	if _, err := g.EffectiveWeights("missing", 0); !errors.Is(err, impl.ErrUnknownTable) {
		t.Fatalf("weights of unknown table error = %v, want ErrUnknownTable", err)
	}
}

func TestPityForcesEntryAfterLimit(t *testing.T) {
	g := newLoot(3)
	entries := []impl.Entry{
		{ID: "relic", Weight: 0, Min: 1, Max: 1, Pity: true},
		{ID: "coin", Weight: 10, Min: 1, Max: 1},
	}
	loadTable(t, g, "t1", entries)
	for i := 0; i < 3; i++ {
		drop, err := g.Roll(impl.RollRequest{Key: "p" + string(rune('a'+i)), Player: "p1", Table: "t1"})
		if err != nil {
			t.Fatalf("Roll %d: %v", i, err)
		}
		if len(drop.Items) != 1 || drop.Items[0].ID != "coin" {
			t.Fatalf("roll %d produced %+v, want coin before the pity trigger", i, drop.Items)
		}
	}
	forced, err := g.Roll(impl.RollRequest{Key: "p-forced", Player: "p1", Table: "t1"})
	if err != nil {
		t.Fatalf("forced Roll: %v", err)
	}
	if len(forced.Items) != 1 || forced.Items[0].ID != "relic" {
		t.Fatalf("fourth roll produced %+v, want the pity entry relic", forced.Items)
	}
}

func TestPityAccountingAndReset(t *testing.T) {
	g := newLoot(10)
	loadTable(t, g, "t1", coinEntries())
	for i := 0; i < 2; i++ {
		if _, err := g.Roll(impl.RollRequest{Key: "a" + string(rune('a'+i)), Player: "p1", Table: "t1"}); err != nil {
			t.Fatalf("Roll %d: %v", i, err)
		}
	}
	if got, err := g.Pity("p1", "t1"); err != nil || got != 2 {
		t.Fatalf("pity after two rolls = %d, %v, want 2, nil", got, err)
	}
	if err := g.ResetPity("p1", "t1"); err != nil {
		t.Fatalf("ResetPity: %v", err)
	}
	if got, err := g.Pity("p1", "t1"); err != nil || got != 0 {
		t.Fatalf("pity after reset = %d, %v, want 0, nil", got, err)
	}
	if _, err := g.Roll(impl.RollRequest{Key: "b1", Player: "p1", Table: "t1"}); err != nil {
		t.Fatalf("Roll after reset: %v", err)
	}
	if got, err := g.Pity("p1", "t1"); err != nil || got != 1 {
		t.Fatalf("pity after one more roll = %d, %v, want 1, nil", got, err)
	}
}

func TestPityIsPerPlayerAndTable(t *testing.T) {
	g := newLoot(10)
	loadTable(t, g, "t1", coinEntries())
	loadTable(t, g, "t2", coinEntries())
	for i := 0; i < 2; i++ {
		if _, err := g.Roll(impl.RollRequest{Key: "c" + string(rune('a'+i)), Player: "p1", Table: "t1"}); err != nil {
			t.Fatalf("Roll %d: %v", i, err)
		}
	}
	if got, err := g.Pity("p1", "t1"); err != nil || got != 2 {
		t.Fatalf("pity of the rolled player = %d, %v, want 2, nil", got, err)
	}
	if got, err := g.Pity("p2", "t1"); err != nil || got != 0 {
		t.Fatalf("pity of another player = %d, %v, want 0, nil", got, err)
	}
	if got, err := g.Pity("p1", "t2"); err != nil || got != 0 {
		t.Fatalf("pity of another table = %d, %v, want 0, nil", got, err)
	}
	if _, err := g.Pity("p1", "missing"); !errors.Is(err, impl.ErrUnknownTable) {
		t.Fatalf("pity of unknown table error = %v, want ErrUnknownTable", err)
	}
}

func TestBatchKeepsOrderAndRejectsUnknownTable(t *testing.T) {
	g := newLoot(5)
	loadTable(t, g, "t1", coinEntries())
	reqs := []impl.RollRequest{
		{Key: "b1", Player: "p1", Table: "t1"},
		{Key: "b2", Player: "p1", Table: "t1"},
		{Key: "b3", Player: "p2", Table: "t1"},
	}
	drops, err := g.Batch(reqs)
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if len(drops) != 3 {
		t.Fatalf("batch drops = %d, want 3", len(drops))
	}
	for i, want := range []string{"b1", "b2", "b3"} {
		if drops[i].Key != want {
			t.Fatalf("drop %d key = %q, want %q", i, drops[i].Key, want)
		}
		if len(drops[i].Items) != 1 {
			t.Fatalf("drop %d items = %d, want 1", i, len(drops[i].Items))
		}
	}
	if _, err := g.Batch([]impl.RollRequest{{Key: "b4", Player: "p1", Table: "missing"}}); !errors.Is(err, impl.ErrUnknownTable) {
		t.Fatalf("batch with unknown table error = %v, want ErrUnknownTable", err)
	}
	if _, err := g.Batch([]impl.RollRequest{{Key: "", Player: "p1", Table: "t1"}}); !errors.Is(err, impl.ErrInvalidRequest) {
		t.Fatalf("batch with empty key error = %v, want ErrInvalidRequest", err)
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	g := newLoot(5)
	loadTable(t, g, "t1", coinEntries())
	for i := 0; i < 2; i++ {
		if _, err := g.Roll(impl.RollRequest{Key: "s" + string(rune('a'+i)), Player: "p1", Table: "t1"}); err != nil {
			t.Fatalf("Roll %d: %v", i, err)
		}
	}
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if _, err := g.Roll(impl.RollRequest{Key: "s-extra", Player: "p1", Table: "t1"}); err != nil {
		t.Fatalf("Roll after snapshot: %v", err)
	}
	if got, err := g.Pity("p1", "t1"); err != nil || got != 3 {
		t.Fatalf("pity before restore = %d, %v, want 3, nil", got, err)
	}
	if err := g.Restore(snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got, err := g.Pity("p1", "t1"); err != nil || got != 2 {
		t.Fatalf("pity after restore = %d, %v, want 2, nil", got, err)
	}
	if _, err := g.Roll(impl.RollRequest{Key: "s-restored", Player: "p1", Table: "t1"}); err != nil {
		t.Fatalf("Roll after restore: %v", err)
	}
	if err := g.Restore([]byte("broken")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("bad snapshot error = %v, want ErrBadSnapshot", err)
	}
	if got, err := g.Pity("p1", "t1"); err != nil || got != 3 {
		t.Fatalf("pity after a rejected restore = %d, %v, want 3, nil", got, err)
	}
}
