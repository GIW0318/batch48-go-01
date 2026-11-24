package task

import (
	"errors"
	"testing"

	impl "gsb/batch48-go-game-a-32"
)

func dungeonSpec() impl.DungeonDef {
	return impl.DungeonDef{
		Rooms: []string{"r1", "r2"},
		Mechanisms: []impl.MechDef{
			{Name: "plate-a", Room: "r1", Kind: "plate"},
			{Name: "plate-b", Room: "r1", Kind: "plate"},
			{Name: "lever-a", Room: "r1", Kind: "lever", Latched: true},
			{Name: "chest-a", Room: "r1", Kind: "chest", Once: true, Permanent: true, Reward: "key"},
			{Name: "plate-c", Room: "r2", Kind: "plate", Momentary: true},
		},
		Doors: []impl.DoorDef{
			{Name: "gate-all", Room: "r1", Requires: []string{"plate-a", "plate-b"}, Mode: "all"},
			{Name: "gate-any", Room: "r1", Requires: []string{"lever-a", "plate-c"}, Mode: "any"},
			{Name: "gate-count", Room: "r1", Requires: []string{"plate-a", "plate-b", "lever-a"}, Mode: "count", Count: 2},
			{Name: "gate-event", Room: "r1", Requires: []string{"boss-dead"}, Mode: "count", Count: 2},
		},
	}
}

func loadedFSM(t *testing.T) *impl.DungeonMechanismFSM {
	t.Helper()
	f := impl.NewDungeonMechanismFSM(impl.Options{DefaultRoom: "r1"})
	if err := f.Load(dungeonSpec()); err != nil {
		t.Fatalf("load dungeon: unexpected error %v", err)
	}
	return f
}

func hasEffect(effects []impl.Effect, kind, target string) bool {
	for _, e := range effects {
		if e.Kind == kind && e.Target == target {
			return true
		}
	}
	return false
}

func TestLoadAcceptsValidDefinition(t *testing.T) {
	f := loadedFSM(t)
	names := f.Mechanisms()
	if len(names) != 5 {
		t.Fatalf("mechanism count = %d, want 5", len(names))
	}
	if names[0] != "chest-a" {
		t.Fatalf("first mechanism = %q, want chest-a in ascending order", names[0])
	}
	if got := f.State("plate-a"); got != "idle" {
		t.Fatalf("initial plate state = %q, want idle", got)
	}
	doors := f.Doors()
	if len(doors) != 4 {
		t.Fatalf("door count = %d, want 4", len(doors))
	}
	if doors["gate-all"] {
		t.Fatalf("gate-all must start locked")
	}
}

func TestLoadRejectsDuplicateMechanism(t *testing.T) {
	f := impl.NewDungeonMechanismFSM(impl.Options{})
	spec := impl.DungeonDef{
		Rooms: []string{"r1"},
		Mechanisms: []impl.MechDef{
			{Name: "plate-a", Room: "r1", Kind: "plate"},
			{Name: "plate-a", Room: "r1", Kind: "plate"},
		},
	}
	if err := f.Load(spec); !errors.Is(err, impl.ErrDuplicateMechanism) {
		t.Fatalf("duplicate mechanism error = %v, want ErrDuplicateMechanism", err)
	}
}

func TestLoadRejectsUnknownDoorRequirement(t *testing.T) {
	f := impl.NewDungeonMechanismFSM(impl.Options{})
	spec := impl.DungeonDef{
		Rooms:      []string{"r1"},
		Mechanisms: []impl.MechDef{{Name: "plate-a", Room: "r1", Kind: "plate"}},
		Doors:      []impl.DoorDef{{Name: "gate", Room: "r1", Requires: []string{"ghost"}, Mode: "all"}},
	}
	if err := f.Load(spec); !errors.Is(err, impl.ErrUnknownMechanism) {
		t.Fatalf("unknown requirement error = %v, want ErrUnknownMechanism", err)
	}
}

func TestActivateUnknownMechanismRejected(t *testing.T) {
	f := loadedFSM(t)
	if _, err := f.Activate("ghost", "p1", 700); !errors.Is(err, impl.ErrUnknownMechanism) {
		t.Fatalf("unknown mechanism error = %v, want ErrUnknownMechanism", err)
	}
}

func TestActivatePlateEmitsSingleEffect(t *testing.T) {
	f := loadedFSM(t)
	effects, err := f.Activate("plate-a", "p1", 700)
	if err != nil {
		t.Fatalf("activate: unexpected error %v", err)
	}
	if len(effects) != 1 {
		t.Fatalf("effect count = %d, want 1", len(effects))
	}
	if effects[0].Kind != "activate" || effects[0].Target != "plate-a" || effects[0].Room != "r1" || effects[0].At != 700 {
		t.Fatalf("effect = %+v, want activate plate-a in r1 at 700", effects[0])
	}
	if got := f.State("plate-a"); got != "active" {
		t.Fatalf("plate state = %q, want active", got)
	}
}

func TestActivateTwiceEmitsNoSecondEffect(t *testing.T) {
	f := loadedFSM(t)
	if _, err := f.Activate("plate-a", "p1", 700); err != nil {
		t.Fatalf("first activate: unexpected error %v", err)
	}
	effects, err := f.Activate("plate-a", "p1", 800)
	if err != nil {
		t.Fatalf("second activate: unexpected error %v", err)
	}
	if len(effects) != 0 {
		t.Fatalf("second activation effects = %d, want none because the state did not change", len(effects))
	}
}

func TestDeactivatePlateResetsState(t *testing.T) {
	f := loadedFSM(t)
	if _, err := f.Activate("plate-a", "p1", 700); err != nil {
		t.Fatalf("activate: unexpected error %v", err)
	}
	effects, err := f.Deactivate("plate-a", "p1", 900)
	if err != nil {
		t.Fatalf("deactivate: unexpected error %v", err)
	}
	if len(effects) != 1 || effects[0].Kind != "deactivate" || effects[0].Target != "plate-a" {
		t.Fatalf("deactivate effects = %+v, want one deactivate effect for plate-a", effects)
	}
	if got := f.State("plate-a"); got != "idle" {
		t.Fatalf("plate state = %q, want idle", got)
	}
}

func TestAllDoorUnlocksWhenEveryRequirementActive(t *testing.T) {
	f := loadedFSM(t)
	if _, err := f.Activate("plate-a", "p1", 700); err != nil {
		t.Fatalf("activate plate-a: unexpected error %v", err)
	}
	if f.DoorUnlocked("gate-all") {
		t.Fatalf("gate-all must stay locked with a single plate")
	}
	effects, err := f.Activate("plate-b", "p1", 800)
	if err != nil {
		t.Fatalf("activate plate-b: unexpected error %v", err)
	}
	if !f.DoorUnlocked("gate-all") {
		t.Fatalf("gate-all must unlock once both plates are active")
	}
	unlocks := make([]string, 0, 2)
	for _, e := range effects {
		if e.Kind == "unlock" {
			unlocks = append(unlocks, e.Target)
		}
	}
	if len(unlocks) != 2 || unlocks[0] != "gate-all" || unlocks[1] != "gate-count" {
		t.Fatalf("unlock effects = %v, want [gate-all gate-count] in definition order", unlocks)
	}
}

func TestAnyDoorUnlocksOnFirstRequirement(t *testing.T) {
	f := loadedFSM(t)
	effects, err := f.Activate("lever-a", "p1", 700)
	if err != nil {
		t.Fatalf("activate lever: unexpected error %v", err)
	}
	if !f.DoorUnlocked("gate-any") {
		t.Fatalf("gate-any must unlock on either requirement")
	}
	if !hasEffect(effects, "unlock", "gate-any") {
		t.Fatalf("effects = %+v, want an unlock effect for gate-any", effects)
	}
	if f.DoorUnlocked("gate-all") {
		t.Fatalf("gate-all requires both plates and must stay locked")
	}
}

func TestCountDoorWaitsForThreshold(t *testing.T) {
	f := loadedFSM(t)
	if _, err := f.Activate("plate-a", "p1", 700); err != nil {
		t.Fatalf("activate plate-a: unexpected error %v", err)
	}
	if f.DoorUnlocked("gate-count") {
		t.Fatalf("gate-count needs two of three requirements and must stay locked")
	}
	if _, err := f.Activate("plate-b", "p1", 800); err != nil {
		t.Fatalf("activate plate-b: unexpected error %v", err)
	}
	if !f.DoorUnlocked("gate-count") {
		t.Fatalf("gate-count must unlock once two requirements are active")
	}
}

func TestChestRewardsOnlyOnce(t *testing.T) {
	f := loadedFSM(t)
	effects, err := f.Activate("chest-a", "p1", 700)
	if err != nil {
		t.Fatalf("open chest: unexpected error %v", err)
	}
	if len(effects) != 1 || effects[0].Kind != "reward" || effects[0].Target != "chest-a" {
		t.Fatalf("chest effects = %+v, want one reward effect for chest-a", effects)
	}
	if got := f.State("chest-a"); got != "used" {
		t.Fatalf("chest state = %q, want used", got)
	}
	again, err := f.Activate("chest-a", "p1", 800)
	if err != nil {
		t.Fatalf("reopen chest: unexpected error %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second chest activation effects = %d, want none", len(again))
	}
	if _, err := f.Deactivate("chest-a", "p1", 900); !errors.Is(err, impl.ErrNotToggleable) {
		t.Fatalf("deactivate chest error = %v, want ErrNotToggleable", err)
	}
}

func TestTriggerCountsEventsAndUnlocksDoor(t *testing.T) {
	f := loadedFSM(t)
	effects, err := f.Trigger(impl.Event{Kind: "boss-dead", Room: "r1", By: "p1", Count: 1, At: 700}, 700)
	if err != nil {
		t.Fatalf("first trigger: unexpected error %v", err)
	}
	if f.DoorUnlocked("gate-event") {
		t.Fatalf("gate-event needs two events and must stay locked")
	}
	if len(effects) != 0 {
		t.Fatalf("first trigger effects = %d, want none", len(effects))
	}
	if got := f.EventCount("boss-dead"); got != 1 {
		t.Fatalf("event count = %d, want 1", got)
	}
	effects, err = f.Trigger(impl.Event{Kind: "boss-dead", Room: "r1", By: "p1", Count: 1, At: 800}, 800)
	if err != nil {
		t.Fatalf("second trigger: unexpected error %v", err)
	}
	if got := f.EventCount("boss-dead"); got != 2 {
		t.Fatalf("event count = %d, want 2", got)
	}
	if !f.DoorUnlocked("gate-event") || !hasEffect(effects, "unlock", "gate-event") {
		t.Fatalf("gate-event must unlock and report an unlock effect on the second event")
	}
}

func TestDoorsReturnsCopy(t *testing.T) {
	f := loadedFSM(t)
	if _, err := f.Activate("plate-a", "p1", 700); err != nil {
		t.Fatalf("activate plate-a: unexpected error %v", err)
	}
	if _, err := f.Activate("plate-b", "p1", 800); err != nil {
		t.Fatalf("activate plate-b: unexpected error %v", err)
	}
	snapshot := f.Doors()
	snapshot["gate-all"] = false
	if !f.Doors()["gate-all"] {
		t.Fatalf("mutating the returned map must not change the door state")
	}
	if !f.DoorUnlocked("gate-all") {
		t.Fatalf("gate-all must stay unlocked")
	}
}

func TestResetReturnsRoomToIdle(t *testing.T) {
	f := loadedFSM(t)
	if _, err := f.Activate("plate-a", "p1", 700); err != nil {
		t.Fatalf("activate plate-a: unexpected error %v", err)
	}
	if err := f.Reset("r1"); err != nil {
		t.Fatalf("reset room: unexpected error %v", err)
	}
	if got := f.State("plate-a"); got != "idle" {
		t.Fatalf("plate state after reset = %q, want idle", got)
	}
	if err := f.Reset("r9"); !errors.Is(err, impl.ErrUnknownRoom) {
		t.Fatalf("unknown room reset error = %v, want ErrUnknownRoom", err)
	}
}

func TestSnapshotRestoreKeepsMechanismState(t *testing.T) {
	fresh := impl.NewDungeonMechanismFSM(impl.Options{})
	if _, err := fresh.Trigger(impl.Event{Kind: "boss-dead", Room: "r1"}, 100); !errors.Is(err, impl.ErrNotLoaded) {
		t.Fatalf("trigger before load error = %v, want ErrNotLoaded", err)
	}
	f := loadedFSM(t)
	if _, err := f.Activate("plate-a", "p1", 700); err != nil {
		t.Fatalf("activate plate-a: unexpected error %v", err)
	}
	if _, err := f.Activate("lever-a", "p1", 800); err != nil {
		t.Fatalf("activate lever-a: unexpected error %v", err)
	}
	if _, err := f.Activate("chest-a", "p1", 900); err != nil {
		t.Fatalf("activate chest-a: unexpected error %v", err)
	}
	if _, err := f.Trigger(impl.Event{Kind: "boss-dead", Room: "r1", Count: 1, At: 950}, 950); err != nil {
		t.Fatalf("trigger: unexpected error %v", err)
	}
	snap, err := f.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: unexpected error %v", err)
	}
	restored := impl.NewDungeonMechanismFSM(impl.Options{})
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restore: unexpected error %v", err)
	}
	if got := restored.State("plate-a"); got != "active" {
		t.Fatalf("restored plate state = %q, want active", got)
	}
	if got := restored.State("chest-a"); got != "used" {
		t.Fatalf("restored chest state = %q, want used", got)
	}
	if !restored.DoorUnlocked("gate-any") {
		t.Fatalf("gate-any must stay unlocked after restore")
	}
	if got := restored.EventCount("boss-dead"); got != 1 {
		t.Fatalf("restored event count = %d, want 1", got)
	}
	if err := restored.Restore([]byte("broken-bytes")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("garbage restore error = %v, want ErrBadSnapshot", err)
	}
}
