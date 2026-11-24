package task

import (
	"errors"
	"testing"

	impl "gsb/batch48-go-game-a-33"
)

func equipOptions() impl.Options {
	return impl.Options{
		Base:  impl.Stats{Strength: 5, Agility: 3, Stamina: 4, Armor: 5},
		Slots: []string{"head", "chest", "legs", "main", "off"},
	}
}

func registeredGuard(t *testing.T) *impl.EquipmentSlotGuard {
	t.Helper()
	g := impl.NewEquipmentSlotGuard(equipOptions())
	if err := g.Register("c1", impl.Character{Level: 10, Class: "warrior"}); err != nil {
		t.Fatalf("register character: unexpected error %v", err)
	}
	return g
}

func helm(id string) impl.Item {
	return impl.Item{ID: id, Slot: "head", Level: 5, Class: "warrior", Stats: impl.Stats{Armor: 3}}
}

func chestPlate(id string, armor int) impl.Item {
	return impl.Item{ID: id, Slot: "chest", Level: 8, Class: "warrior", Stats: impl.Stats{Armor: armor, Stamina: 2}}
}

func greatsword() impl.Item {
	return impl.Item{ID: "greatsword", Slot: "main", TwoHanded: true, Level: 10, Class: "warrior", Stats: impl.Stats{Strength: 4, Armor: 1}}
}

func TestRegisterStoresBaseStats(t *testing.T) {
	g := registeredGuard(t)
	got := g.Stats("c1")
	want := impl.Stats{Strength: 5, Agility: 3, Stamina: 4, Armor: 5}
	if got != want {
		t.Fatalf("base stats = %+v, want %+v", got, want)
	}
	if slots := g.Slots("c1"); len(slots) != 0 {
		t.Fatalf("registered character has %d equipped items, want 0", len(slots))
	}
}

func TestEquipAddsItemStats(t *testing.T) {
	g := registeredGuard(t)
	change, err := g.Equip("c1", helm("helm"), "head")
	if err != nil {
		t.Fatalf("equip helm: unexpected error %v", err)
	}
	if change.Slot != "head" || change.Old.ID != "" || change.New.ID != "helm" {
		t.Fatalf("change = %+v, want head slot filled by helm", change)
	}
	if got := g.Stats("c1").Armor; got != 8 {
		t.Fatalf("armor = %d, want base 5 plus 3", got)
	}
	item, ok := g.SlotItem("c1", "head")
	if !ok || item.ID != "helm" {
		t.Fatalf("head slot = %+v present=%v, want helm", item, ok)
	}
}

func TestEquipUnknownSlotRejected(t *testing.T) {
	g := registeredGuard(t)
	if _, err := g.Equip("c1", helm("helm"), "tail"); !errors.Is(err, impl.ErrUnknownSlot) {
		t.Fatalf("unknown slot error = %v, want ErrUnknownSlot", err)
	}
}

func TestEquipLevelTooLowRejected(t *testing.T) {
	g := registeredGuard(t)
	heavy := impl.Item{ID: "heavy", Slot: "head", Level: 20, Class: "warrior", Stats: impl.Stats{Armor: 30}}
	if _, err := g.Equip("c1", heavy, "head"); !errors.Is(err, impl.ErrLevelTooLow) {
		t.Fatalf("level error = %v, want ErrLevelTooLow", err)
	}
}

func TestEquipWrongClassRejected(t *testing.T) {
	g := registeredGuard(t)
	robe := impl.Item{ID: "robe", Slot: "chest", Level: 5, Class: "mage", Stats: impl.Stats{Armor: 2}}
	if _, err := g.Equip("c1", robe, "chest"); !errors.Is(err, impl.ErrWrongClass) {
		t.Fatalf("class error = %v, want ErrWrongClass", err)
	}
}

func TestEquipReplaceRecalculatesStats(t *testing.T) {
	g := registeredGuard(t)
	if _, err := g.Equip("c1", helm("helm"), "head"); err != nil {
		t.Fatalf("equip first helm: unexpected error %v", err)
	}
	change, err := g.Equip("c1", impl.Item{ID: "helm2", Slot: "head", Level: 6, Class: "warrior", Stats: impl.Stats{Armor: 7}}, "head")
	if err != nil {
		t.Fatalf("equip replacement: unexpected error %v", err)
	}
	if change.Old.ID != "helm" || change.New.ID != "helm2" {
		t.Fatalf("change = %+v, want helm replaced by helm2", change)
	}
	if got := g.Stats("c1").Armor; got != 12 {
		t.Fatalf("armor after replacement = %d, want base 5 plus 7 without the old 3", got)
	}
	if slots := g.Slots("c1"); len(slots) != 1 {
		t.Fatalf("equipped slots = %d, want 1", len(slots))
	}
}

func TestUnequipRemovesStats(t *testing.T) {
	g := registeredGuard(t)
	if _, err := g.Equip("c1", helm("helm"), "head"); err != nil {
		t.Fatalf("equip: unexpected error %v", err)
	}
	change, err := g.Unequip("c1", "head")
	if err != nil {
		t.Fatalf("unequip: unexpected error %v", err)
	}
	if change.Slot != "head" || change.Old.ID != "helm" || change.New.ID != "" {
		t.Fatalf("change = %+v, want helm removed from head", change)
	}
	if got := g.Stats("c1"); got != equipOptions().Base {
		t.Fatalf("stats after unequip = %+v, want base %+v", got, equipOptions().Base)
	}
	if _, ok := g.SlotItem("c1", "head"); ok {
		t.Fatalf("head slot must be empty after unequip")
	}
}

func TestUnequipEmptySlotRejected(t *testing.T) {
	g := registeredGuard(t)
	if _, err := g.Unequip("c1", "head"); !errors.Is(err, impl.ErrSlotEmpty) {
		t.Fatalf("empty slot error = %v, want ErrSlotEmpty", err)
	}
}

func TestTwoHandedFillsOffhandSlot(t *testing.T) {
	g := registeredGuard(t)
	change, err := g.Equip("c1", greatsword(), "main")
	if err != nil {
		t.Fatalf("equip two-handed: unexpected error %v", err)
	}
	if change.Slot != "main" || change.New.ID != "greatsword" {
		t.Fatalf("change = %+v, want greatsword in main", change)
	}
	off, ok := g.SlotItem("c1", "off")
	if !ok || off.ID != "greatsword" || !off.TwoHanded {
		t.Fatalf("offhand = %+v present=%v, want the two-handed greatsword", off, ok)
	}
	if got := g.Stats("c1").Strength; got != 9 {
		t.Fatalf("strength = %d, want base 5 plus 4", got)
	}
	if got := g.Stats("c1").Armor; got != 6 {
		t.Fatalf("armor = %d, want base 5 plus 1 counted once", got)
	}
}

func TestOffhandBlockedByTwoHanded(t *testing.T) {
	g := registeredGuard(t)
	if _, err := g.Equip("c1", greatsword(), "main"); err != nil {
		t.Fatalf("equip two-handed: unexpected error %v", err)
	}
	shield := impl.Item{ID: "shield", Slot: "off", Level: 5, Class: "warrior", Stats: impl.Stats{Armor: 6}}
	if _, err := g.Equip("c1", shield, "off"); !errors.Is(err, impl.ErrOffhandBlocked) {
		t.Fatalf("offhand error = %v, want ErrOffhandBlocked", err)
	}
}

func TestSetBonusCountsPieces(t *testing.T) {
	g := registeredGuard(t)
	ironHead := helm("iron-head")
	ironHead.SetID = "iron"
	ironChest := chestPlate("iron-chest", 4)
	ironChest.SetID = "iron"
	if _, err := g.Equip("c1", ironHead, "head"); err != nil {
		t.Fatalf("equip set head: unexpected error %v", err)
	}
	if _, err := g.Equip("c1", ironChest, "chest"); err != nil {
		t.Fatalf("equip set chest: unexpected error %v", err)
	}
	bonus := g.SetBonus("c1")
	if len(bonus) != 1 {
		t.Fatalf("set bonus count = %d, want 1", len(bonus))
	}
	if bonus[0].SetID != "iron" || bonus[0].Pieces != 2 {
		t.Fatalf("bonus = %+v, want iron with 2 pieces", bonus[0])
	}
	if bonus[0].Armor != 20 {
		t.Fatalf("bonus armor = %d, want 10 per piece", bonus[0].Armor)
	}
}

func TestSetBonusDropsAfterUnequip(t *testing.T) {
	g := registeredGuard(t)
	ironHead := helm("iron-head")
	ironHead.SetID = "iron"
	ironChest := chestPlate("iron-chest", 4)
	ironChest.SetID = "iron"
	if _, err := g.Equip("c1", ironHead, "head"); err != nil {
		t.Fatalf("equip set head: unexpected error %v", err)
	}
	if _, err := g.Equip("c1", ironChest, "chest"); err != nil {
		t.Fatalf("equip set chest: unexpected error %v", err)
	}
	if _, err := g.Unequip("c1", "chest"); err != nil {
		t.Fatalf("unequip chest: unexpected error %v", err)
	}
	if bonus := g.SetBonus("c1"); len(bonus) != 0 {
		t.Fatalf("set bonus after removing a piece = %+v, want none", bonus)
	}
}

func TestPreviewRejectsInvalidChange(t *testing.T) {
	g := registeredGuard(t)
	changes := []impl.Change{{Slot: "head", New: impl.Item{ID: "heavy", Slot: "head", Level: 40, Class: "warrior"}}}
	if _, err := g.Preview("c1", changes); !errors.Is(err, impl.ErrLevelTooLow) {
		t.Fatalf("preview error = %v, want ErrLevelTooLow", err)
	}
}

func TestRepairRecomputesStats(t *testing.T) {
	g := registeredGuard(t)
	if _, err := g.Equip("c1", helm("helm"), "head"); err != nil {
		t.Fatalf("equip helm: unexpected error %v", err)
	}
	if _, err := g.Equip("c1", chestPlate("plate", 8), "chest"); err != nil {
		t.Fatalf("equip chest: unexpected error %v", err)
	}
	if err := g.Repair("c1"); err != nil {
		t.Fatalf("repair: unexpected error %v", err)
	}
	want := impl.Stats{Strength: 5, Agility: 3, Stamina: 6, Armor: 16}
	if got := g.Stats("c1"); got != want {
		t.Fatalf("stats after repair = %+v, want %+v", got, want)
	}
	if err := g.Repair("c1"); err != nil {
		t.Fatalf("second repair: unexpected error %v", err)
	}
	if got := g.Stats("c1"); got != want {
		t.Fatalf("stats after repeated repair = %+v, want %+v", got, want)
	}
}

func TestSnapshotRestoreKeepsEquipment(t *testing.T) {
	g := registeredGuard(t)
	ironHead := helm("iron-head")
	ironHead.SetID = "iron"
	ironChest := chestPlate("iron-chest", 4)
	ironChest.SetID = "iron"
	if _, err := g.Equip("c1", ironHead, "head"); err != nil {
		t.Fatalf("equip set head: unexpected error %v", err)
	}
	if _, err := g.Equip("c1", ironChest, "chest"); err != nil {
		t.Fatalf("equip set chest: unexpected error %v", err)
	}
	if _, err := g.Equip("c1", greatsword(), "main"); err != nil {
		t.Fatalf("equip two-handed: unexpected error %v", err)
	}
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: unexpected error %v", err)
	}
	restored := impl.NewEquipmentSlotGuard(impl.Options{})
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restore: unexpected error %v", err)
	}
	if got, want := restored.Stats("c1"), g.Stats("c1"); got != want {
		t.Fatalf("restored stats = %+v, want %+v", got, want)
	}
	bonus := restored.SetBonus("c1")
	if len(bonus) != 1 || bonus[0].Pieces != 2 {
		t.Fatalf("restored set bonus = %+v, want one iron bonus with 2 pieces", bonus)
	}
	if item, ok := restored.SlotItem("c1", "off"); !ok || item.ID != "greatsword" {
		t.Fatalf("restored offhand = %+v present=%v, want greatsword", item, ok)
	}
	if err := restored.Restore([]byte("garbage")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("garbage restore error = %v, want ErrBadSnapshot", err)
	}
}
