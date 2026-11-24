package task

import (
	"errors"
	"sync"
	"testing"

	impl "gsb/batch48-go-game-a-27"
)

func newPatrol() *impl.PatrolAlertGuard {
	return impl.NewPatrolAlertGuard(impl.Options{
		Speed:       4,
		SightRange:  10,
		SoundRange:  15,
		HoldTicks:   5,
		CombatTicks: 20,
		DecayTicks:  []int64{30, 20, 10},
		GroupOf: map[string]string{
			"guard-a": "alpha",
			"guard-b": "alpha",
			"guard-c": "alpha",
			"scout":   "beta",
		},
	})
}

func assign(t *testing.T, g *impl.PatrolAlertGuard, npc string, route []impl.Point) {
	t.Helper()
	if err := g.Assign(npc, route); err != nil {
		t.Fatalf("Assign(%s): %v", npc, err)
	}
}

func TestAssignRejectsBadInput(t *testing.T) {
	g := newPatrol()
	if err := g.Assign("", []impl.Point{{X: 0, Y: 0}}); !errors.Is(err, impl.ErrInvalidNPC) {
		t.Fatalf("empty npc error = %v, want ErrInvalidNPC", err)
	}
	if err := g.Assign("guard-a", nil); !errors.Is(err, impl.ErrEmptyRoute) {
		t.Fatalf("empty route error = %v, want ErrEmptyRoute", err)
	}
	route := []impl.Point{{X: 0, Y: 0}, {X: 4, Y: 0}}
	assign(t, g, "guard-a", route)
	if err := g.Assign("guard-a", route); !errors.Is(err, impl.ErrDuplicateNPC) {
		t.Fatalf("second assign error = %v, want ErrDuplicateNPC", err)
	}
}

func TestStepRejectsUnknownNPC(t *testing.T) {
	g := newPatrol()
	if _, err := g.Step("ghost", 0); !errors.Is(err, impl.ErrUnknownNPC) {
		t.Fatalf("step of unknown npc error = %v, want ErrUnknownNPC", err)
	}
}

func TestStepMovesBySpeed(t *testing.T) {
	g := newPatrol()
	assign(t, g, "scout", []impl.Point{{X: 0, Y: 0}, {X: 10, Y: 0}, {X: 10, Y: 10}})
	mv, err := g.Step("scout", 0)
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	if mv.From != (impl.Point{X: 0, Y: 0}) {
		t.Fatalf("move from = %+v, want 0,0", mv.From)
	}
	if mv.To != (impl.Point{X: 4, Y: 0}) {
		t.Fatalf("move to = %+v, want 4,0", mv.To)
	}
	if mv.Waypoint != 1 {
		t.Fatalf("waypoint = %d, want 1", mv.Waypoint)
	}
	if mv.Holding {
		t.Fatal("a step that did not reach the waypoint must not hold")
	}
}

func TestStepHoldsThenAdvances(t *testing.T) {
	g := newPatrol()
	assign(t, g, "guard-a", []impl.Point{{X: 0, Y: 0}, {X: 3, Y: 0}, {X: 3, Y: 3}})
	mv, err := g.Step("guard-a", 0)
	if err != nil {
		t.Fatalf("first Step: %v", err)
	}
	if mv.To != (impl.Point{X: 3, Y: 0}) || !mv.Holding || mv.Waypoint != 1 {
		t.Fatalf("arrival move = %+v, want to 3,0 holding at waypoint 1", mv)
	}
	mv, err = g.Step("guard-a", 4)
	if err != nil {
		t.Fatalf("held Step: %v", err)
	}
	if mv.To != (impl.Point{X: 3, Y: 0}) || !mv.Holding || mv.Waypoint != 1 {
		t.Fatalf("move inside the hold window = %+v, want to stay at 3,0", mv)
	}
	mv, err = g.Step("guard-a", 5)
	if err != nil {
		t.Fatalf("resumed Step: %v", err)
	}
	if mv.To != (impl.Point{X: 3, Y: 3}) || mv.Waypoint != 2 {
		t.Fatalf("move after the hold window = %+v, want to 3,3 at waypoint 2", mv)
	}
}

func TestStepFreezesDuringCombat(t *testing.T) {
	g := newPatrol()
	assign(t, g, "guard-a", []impl.Point{{X: 0, Y: 0}, {X: 10, Y: 0}})
	for i := 0; i < 3; i++ {
		if _, err := g.Sense("guard-a", impl.SenseEvent{Kind: "sight", Target: "hero", Distance: 5}, int64(i)); err != nil {
			t.Fatalf("Sense %d: %v", i, err)
		}
	}
	mv, err := g.Step("guard-a", 100)
	if err != nil {
		t.Fatalf("Step in combat: %v", err)
	}
	if mv.From != mv.To {
		t.Fatalf("combat move changed position from %+v to %+v", mv.From, mv.To)
	}
	if !mv.Holding {
		t.Fatal("a npc frozen by combat must report holding")
	}
}

func TestSenseSightRaisesAlert(t *testing.T) {
	g := newPatrol()
	assign(t, g, "guard-a", []impl.Point{{X: 0, Y: 0}, {X: 4, Y: 0}})
	al, err := g.Sense("guard-a", impl.SenseEvent{Kind: "sight", Target: "hero", Distance: 8}, 10)
	if err != nil {
		t.Fatalf("Sense: %v", err)
	}
	if al.NPC != "guard-a" || al.Group != "alpha" {
		t.Fatalf("alert identity = %+v, want guard-a of alpha", al)
	}
	if al.Level != 1 {
		t.Fatalf("alert level = %d, want 1", al.Level)
	}
	if al.Since != 10 || al.Target != "hero" {
		t.Fatalf("alert detail = %+v, want since 10 target hero", al)
	}
	if got := g.AlertOf("guard-a"); got.Level != 1 {
		t.Fatalf("stored alert level = %d, want 1", got.Level)
	}
}

func TestSenseOutOfRangeKeepsAlert(t *testing.T) {
	g := newPatrol()
	assign(t, g, "guard-a", []impl.Point{{X: 0, Y: 0}, {X: 4, Y: 0}})
	al, err := g.Sense("guard-a", impl.SenseEvent{Kind: "sight", Target: "hero", Distance: 11}, 10)
	if err != nil {
		t.Fatalf("out of range sense must not fail: %v", err)
	}
	if al.Level != 0 {
		t.Fatalf("alert level after an out of range sight = %d, want 0", al.Level)
	}
	if al.Target != "" {
		t.Fatalf("out of range sense stored target %q, want none", al.Target)
	}
}

func TestSenseSoundCapsAtSearch(t *testing.T) {
	g := newPatrol()
	assign(t, g, "guard-a", []impl.Point{{X: 0, Y: 0}, {X: 4, Y: 0}})
	for i := 0; i < 3; i++ {
		if _, err := g.Sense("guard-a", impl.SenseEvent{Kind: "sound", Target: "steps", Distance: 5}, int64(i)); err != nil {
			t.Fatalf("sound sense %d: %v", i, err)
		}
	}
	if got := g.AlertOf("guard-a"); got.Level != 2 {
		t.Fatalf("alert level after three sounds = %d, want the sound cap 2", got.Level)
	}
}

func TestSenseSightReachesCombat(t *testing.T) {
	g := newPatrol()
	assign(t, g, "guard-a", []impl.Point{{X: 0, Y: 0}, {X: 4, Y: 0}})
	for i := 0; i < 4; i++ {
		if _, err := g.Sense("guard-a", impl.SenseEvent{Kind: "sight", Target: "hero", Distance: 5}, int64(i)); err != nil {
			t.Fatalf("sight sense %d: %v", i, err)
		}
	}
	if got := g.AlertOf("guard-a"); got.Level != 3 {
		t.Fatalf("alert level after four sights = %d, want the combat cap 3", got.Level)
	}
}

func TestSenseRejectsBadEvent(t *testing.T) {
	g := newPatrol()
	assign(t, g, "guard-a", []impl.Point{{X: 0, Y: 0}, {X: 4, Y: 0}})
	if _, err := g.Sense("ghost", impl.SenseEvent{Kind: "sight", Target: "hero", Distance: 1}, 0); !errors.Is(err, impl.ErrUnknownNPC) {
		t.Fatalf("sense of unknown npc error = %v, want ErrUnknownNPC", err)
	}
	if _, err := g.Sense("guard-a", impl.SenseEvent{Kind: "smell", Target: "hero", Distance: 1}, 0); !errors.Is(err, impl.ErrInvalidEvent) {
		t.Fatalf("unknown event kind error = %v, want ErrInvalidEvent", err)
	}
	if _, err := g.Sense("guard-a", impl.SenseEvent{Kind: "sight", Target: "hero", Distance: -1}, 0); !errors.Is(err, impl.ErrInvalidEvent) {
		t.Fatalf("negative distance error = %v, want ErrInvalidEvent", err)
	}
}

func TestCallHelpReturnsSortedPeers(t *testing.T) {
	g := newPatrol()
	route := []impl.Point{{X: 0, Y: 0}, {X: 4, Y: 0}}
	assign(t, g, "guard-a", route)
	assign(t, g, "guard-b", route)
	assign(t, g, "guard-c", route)
	assign(t, g, "scout", route)
	for i := 0; i < 3; i++ {
		if _, err := g.Sense("guard-a", impl.SenseEvent{Kind: "sight", Target: "hero", Distance: 5}, int64(i)); err != nil {
			t.Fatalf("sight sense %d: %v", i, err)
		}
	}
	helpers, err := g.CallHelp("guard-a")
	if err != nil {
		t.Fatalf("CallHelp: %v", err)
	}
	if len(helpers) != 2 || helpers[0] != "guard-b" || helpers[1] != "guard-c" {
		t.Fatalf("helpers = %v, want [guard-b guard-c]", helpers)
	}
	for _, peer := range helpers {
		if got := g.AlertOf(peer).Level; got < 2 {
			t.Fatalf("peer %s level = %d, want at least 2 after the call", peer, got)
		}
	}
}

func TestCallHelpOutsideCombat(t *testing.T) {
	g := newPatrol()
	assign(t, g, "guard-a", []impl.Point{{X: 0, Y: 0}, {X: 4, Y: 0}})
	if _, err := g.CallHelp("guard-a"); !errors.Is(err, impl.ErrNotInCombat) {
		t.Fatalf("call help at patrol level error = %v, want ErrNotInCombat", err)
	}
	if _, err := g.CallHelp("ghost"); !errors.Is(err, impl.ErrUnknownNPC) {
		t.Fatalf("call help of unknown npc error = %v, want ErrUnknownNPC", err)
	}
}

func TestDecayKeepsIdlePatrol(t *testing.T) {
	g := newPatrol()
	assign(t, g, "guard-a", []impl.Point{{X: 0, Y: 0}, {X: 4, Y: 0}})
	al := g.Decay("guard-a", 500)
	if al.NPC != "guard-a" || al.Group != "alpha" {
		t.Fatalf("decayed alert identity = %+v, want guard-a of alpha", al)
	}
	if al.Level != 0 {
		t.Fatalf("idle patrol level after decay = %d, want 0", al.Level)
	}
	if ghost := g.Decay("ghost", 1); ghost.NPC != "" || ghost.Level != 0 {
		t.Fatalf("decay of unknown npc = %+v, want the zero alert", ghost)
	}
}

func TestSnapshotKeepsWaypointAndAlert(t *testing.T) {
	g := newPatrol()
	assign(t, g, "guard-a", []impl.Point{{X: 0, Y: 0}, {X: 10, Y: 0}, {X: 10, Y: 10}})
	if _, err := g.Step("guard-a", 0); err != nil {
		t.Fatalf("Step before snapshot: %v", err)
	}
	if _, err := g.Sense("guard-a", impl.SenseEvent{Kind: "sight", Target: "hero", Distance: 5}, 0); err != nil {
		t.Fatalf("Sense before snapshot: %v", err)
	}
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if _, err := g.Step("guard-a", 10); err != nil {
		t.Fatalf("Step after snapshot: %v", err)
	}
	if _, err := g.Sense("guard-a", impl.SenseEvent{Kind: "sight", Target: "hero", Distance: 5}, 10); err != nil {
		t.Fatalf("Sense after snapshot: %v", err)
	}
	if err := g.Restore(snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := g.AlertOf("guard-a"); got.Level != 1 {
		t.Fatalf("alert level after restore = %d, want 1", got.Level)
	}
	pos, ok := g.Position("guard-a")
	if !ok || pos != (impl.Point{X: 4, Y: 0}) {
		t.Fatalf("position after restore = %+v (ok %v), want 4,0", pos, ok)
	}
	mv, err := g.Step("guard-a", 20)
	if err != nil {
		t.Fatalf("Step after restore: %v", err)
	}
	if mv.To != (impl.Point{X: 8, Y: 0}) || mv.Waypoint != 1 {
		t.Fatalf("move after restore = %+v, want 8,0 at waypoint 1", mv)
	}
	if err := g.Restore([]byte("broken")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("bad snapshot error = %v, want ErrBadSnapshot", err)
	}
	if got := g.AlertOf("guard-a"); got.Level != 1 {
		t.Fatalf("alert level after a rejected restore = %d, want 1", got.Level)
	}
}

func TestConcurrentSenseCapsAtCombat(t *testing.T) {
	g := newPatrol()
	assign(t, g, "guard-a", []impl.Point{{X: 0, Y: 0}, {X: 4, Y: 0}})
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := g.Sense("guard-a", impl.SenseEvent{Kind: "sight", Target: "hero", Distance: 5}, int64(i)); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent Sense: %v", err)
	}
	if got := g.AlertOf("guard-a"); got.Level != 3 {
		t.Fatalf("alert level after eight concurrent sights = %d, want 3", got.Level)
	}
}
