package task

import (
	"errors"
	"sync"
	"testing"

	impl "gsb/batch48-go-game-a-16"
)

func TestCellOfFloorsNegativeCoordinates(t *testing.T) {
	b := impl.NewInterestGridBroker(10)
	if got := b.CellOf(impl.Vec2{X: 25, Y: 5}); got != (impl.Cell{X: 2, Y: 0}) {
		t.Fatalf("cell of (25,5) = %+v, want (2,0)", got)
	}
	if got := b.CellOf(impl.Vec2{X: -5, Y: -0.5}); got != (impl.Cell{X: -1, Y: -1}) {
		t.Fatalf("cell of (-5,-0.5) = %+v, want (-1,-1)", got)
	}
}

func TestFirstDiffEntersVisibleEntities(t *testing.T) {
	b := impl.NewInterestGridBroker(10)
	if err := b.SetRadius(1, 2); err != nil {
		t.Fatalf("set radius: %v", err)
	}
	if err := b.RegisterEntity(7, impl.Vec2{X: 5, Y: 5}); err != nil {
		t.Fatalf("register near entity: %v", err)
	}
	if err := b.RegisterEntity(8, impl.Vec2{X: 35, Y: 5}); err != nil {
		t.Fatalf("register far entity: %v", err)
	}
	if err := b.RegisterClient("c1", impl.Vec2{X: 5, Y: 5}); err != nil {
		t.Fatalf("register client: %v", err)
	}
	d, err := b.Diff("c1")
	if err != nil {
		t.Fatalf("first diff: %v", err)
	}
	if len(d.Entered) != 1 || d.Entered[0] != 7 {
		t.Fatalf("entered = %v, want only entity 7", d.Entered)
	}
	if len(d.Left) != 0 || len(d.Updated) != 0 {
		t.Fatalf("first diff must not carry left or updated: %+v", d)
	}
}

func TestMoveViewerEntersEntityAcrossEnterRadius(t *testing.T) {
	b := impl.NewInterestGridBroker(10)
	if err := b.SetRadius(1, 2); err != nil {
		t.Fatalf("set radius: %v", err)
	}
	if err := b.RegisterEntity(3, impl.Vec2{X: 25, Y: 5}); err != nil {
		t.Fatalf("register entity: %v", err)
	}
	if err := b.RegisterClient("c1", impl.Vec2{X: 5, Y: 5}); err != nil {
		t.Fatalf("register client: %v", err)
	}
	d, err := b.Diff("c1")
	if err != nil || len(d.Entered) != 0 {
		t.Fatalf("entity two cells away must stay invisible: %+v err=%v", d, err)
	}
	if err := b.MoveViewer("c1", impl.Vec2{X: 15, Y: 5}); err != nil {
		t.Fatalf("move viewer: %v", err)
	}
	d, err = b.Diff("c1")
	if err != nil {
		t.Fatalf("diff after viewer move: %v", err)
	}
	if len(d.Entered) != 1 || d.Entered[0] != 3 {
		t.Fatalf("entered = %v, want entity 3 after the viewer moved next to it", d.Entered)
	}
}

func TestMoveEntityBeyondLeaveRadiusLeaves(t *testing.T) {
	b := impl.NewInterestGridBroker(10)
	if err := b.SetRadius(1, 2); err != nil {
		t.Fatalf("set radius: %v", err)
	}
	if err := b.RegisterEntity(5, impl.Vec2{X: 5, Y: 5}); err != nil {
		t.Fatalf("register entity: %v", err)
	}
	if err := b.RegisterClient("c1", impl.Vec2{X: 5, Y: 5}); err != nil {
		t.Fatalf("register client: %v", err)
	}
	if d, err := b.Diff("c1"); err != nil || len(d.Entered) != 1 {
		t.Fatalf("initial delivery: %+v err=%v", d, err)
	}
	if err := b.MoveEntity(5, impl.Vec2{X: 35, Y: 5}); err != nil {
		t.Fatalf("move entity out: %v", err)
	}
	d, err := b.Diff("c1")
	if err != nil {
		t.Fatalf("diff after move: %v", err)
	}
	if len(d.Left) != 1 || d.Left[0] != 5 {
		t.Fatalf("left = %v, want entity 5", d.Left)
	}
	if len(d.Entered) != 0 || len(d.Updated) != 0 {
		t.Fatalf("leaving entity must not appear in other sets: %+v", d)
	}
}

func TestDiffClearsAfterDelivery(t *testing.T) {
	b := impl.NewInterestGridBroker(10)
	if err := b.SetRadius(1, 2); err != nil {
		t.Fatalf("set radius: %v", err)
	}
	if err := b.RegisterEntity(1, impl.Vec2{X: 5, Y: 5}); err != nil {
		t.Fatalf("register entity: %v", err)
	}
	if err := b.RegisterClient("c1", impl.Vec2{X: 5, Y: 5}); err != nil {
		t.Fatalf("register client: %v", err)
	}
	if d, err := b.Diff("c1"); err != nil || len(d.Entered) != 1 {
		t.Fatalf("first delivery: %+v err=%v", d, err)
	}
	d, err := b.Diff("c1")
	if err != nil {
		t.Fatalf("second diff: %v", err)
	}
	if len(d.Entered)+len(d.Left)+len(d.Updated) != 0 {
		t.Fatalf("second diff must be empty, got %+v", d)
	}
}

func TestEnteringEntityNeverAppearsInUpdatedOrLeft(t *testing.T) {
	b := impl.NewInterestGridBroker(10)
	if err := b.SetRadius(1, 2); err != nil {
		t.Fatalf("set radius: %v", err)
	}
	if err := b.RegisterEntity(2, impl.Vec2{X: 105, Y: 105}); err != nil {
		t.Fatalf("register far entity: %v", err)
	}
	if err := b.RegisterClient("c1", impl.Vec2{X: 5, Y: 5}); err != nil {
		t.Fatalf("register client: %v", err)
	}
	if d, err := b.Diff("c1"); err != nil || len(d.Entered)+len(d.Left)+len(d.Updated) != 0 {
		t.Fatalf("far entity must not be delivered: %+v err=%v", d, err)
	}
	if err := b.MoveEntity(2, impl.Vec2{X: 5, Y: 5}); err != nil {
		t.Fatalf("move entity in: %v", err)
	}
	d, err := b.Diff("c1")
	if err != nil {
		t.Fatalf("diff after move: %v", err)
	}
	if len(d.Entered) != 1 || d.Entered[0] != 2 {
		t.Fatalf("entered = %v, want entity 2", d.Entered)
	}
	if len(d.Updated) != 0 || len(d.Left) != 0 {
		t.Fatalf("entering entity must not also be updated or left: %+v", d)
	}
}

func TestRepeatedEntityMovesCoalesceToFinalPosition(t *testing.T) {
	b := impl.NewInterestGridBroker(10)
	if err := b.SetRadius(1, 2); err != nil {
		t.Fatalf("set radius: %v", err)
	}
	if err := b.RegisterEntity(9, impl.Vec2{X: 5, Y: 5}); err != nil {
		t.Fatalf("register entity: %v", err)
	}
	if err := b.RegisterClient("c1", impl.Vec2{X: 5, Y: 5}); err != nil {
		t.Fatalf("register client: %v", err)
	}
	if d, err := b.Diff("c1"); err != nil || len(d.Entered) != 1 {
		t.Fatalf("initial delivery: %+v err=%v", d, err)
	}
	if err := b.MoveEntity(9, impl.Vec2{X: 6, Y: 6}); err != nil {
		t.Fatalf("first move: %v", err)
	}
	if err := b.MoveEntity(9, impl.Vec2{X: 7, Y: 7}); err != nil {
		t.Fatalf("second move: %v", err)
	}
	d, err := b.Diff("c1")
	if err != nil {
		t.Fatalf("diff after moves: %v", err)
	}
	if len(d.Updated) != 1 {
		t.Fatalf("updated count = %d, want one coalesced entry", len(d.Updated))
	}
	if d.Updated[0].Entity != 9 || d.Updated[0].Pos != (impl.Vec2{X: 7, Y: 7}) {
		t.Fatalf("updated = %+v, want entity 9 at final position (7,7)", d.Updated[0])
	}
	if len(d.Entered) != 0 || len(d.Left) != 0 {
		t.Fatalf("coalesced move must not enter or leave: %+v", d)
	}
}

func TestCellLimitKeepsNearestCellsAndRecordsTrim(t *testing.T) {
	b := impl.NewInterestGridBroker(10)
	if err := b.SetRadius(1, 2); err != nil {
		t.Fatalf("set radius: %v", err)
	}
	if err := b.SetCellLimit(1); err != nil {
		t.Fatalf("set cell limit: %v", err)
	}
	if err := b.RegisterClient("c1", impl.Vec2{X: 5, Y: 5}); err != nil {
		t.Fatalf("register client: %v", err)
	}
	cells, err := b.SubscribedCells("c1")
	if err != nil {
		t.Fatalf("subscribed cells: %v", err)
	}
	if len(cells) != 1 || cells[0] != (impl.Cell{X: 0, Y: 0}) {
		t.Fatalf("subscribed cells = %+v, want only the viewer cell (0,0)", cells)
	}
	if got := b.Stats().TrimmedCells; got != 8 {
		t.Fatalf("trimmed cells = %d, want 8 cells dropped by the limit", got)
	}
}

func TestSetRadiusRejectsNonPositiveAndInverted(t *testing.T) {
	b := impl.NewInterestGridBroker(10)
	if err := b.SetRadius(0, 2); !errors.Is(err, impl.ErrInvalidRadius) {
		t.Fatalf("zero enter radius error = %v, want ErrInvalidRadius", err)
	}
	if err := b.SetRadius(2, 2); !errors.Is(err, impl.ErrInvalidRadius) {
		t.Fatalf("inverted radii error = %v, want ErrInvalidRadius", err)
	}
	if err := b.SetRadius(3, 1); !errors.Is(err, impl.ErrInvalidRadius) {
		t.Fatalf("leave below enter error = %v, want ErrInvalidRadius", err)
	}
	if err := b.SetRadius(1, 2); err != nil {
		t.Fatalf("valid radii rejected: %v", err)
	}
}

func TestSetCellLimitRejectsZero(t *testing.T) {
	b := impl.NewInterestGridBroker(10)
	if err := b.SetCellLimit(0); !errors.Is(err, impl.ErrInvalidCellLimit) {
		t.Fatalf("zero cell limit error = %v, want ErrInvalidCellLimit", err)
	}
	if err := b.SetCellLimit(4); err != nil {
		t.Fatalf("valid cell limit rejected: %v", err)
	}
}

func TestUnknownClientAndEntityAreRejected(t *testing.T) {
	b := impl.NewInterestGridBroker(10)
	if _, err := b.Diff("ghost"); !errors.Is(err, impl.ErrClientUnknown) {
		t.Fatalf("diff unknown client error = %v, want ErrClientUnknown", err)
	}
	if err := b.MoveEntity(123, impl.Vec2{X: 1, Y: 1}); !errors.Is(err, impl.ErrEntityUnknown) {
		t.Fatalf("move unknown entity error = %v, want ErrEntityUnknown", err)
	}
}

func TestDuplicateEntityRegistrationRejected(t *testing.T) {
	b := impl.NewInterestGridBroker(10)
	if err := b.RegisterEntity(1, impl.Vec2{X: 5, Y: 5}); err != nil {
		t.Fatalf("register entity: %v", err)
	}
	if err := b.RegisterEntity(1, impl.Vec2{X: 15, Y: 5}); !errors.Is(err, impl.ErrEntityExists) {
		t.Fatalf("duplicate entity error = %v, want ErrEntityExists", err)
	}
	if got := b.Stats().Entities; got != 1 {
		t.Fatalf("entity count = %d, want 1", got)
	}
}

func TestSnapshotRestorePreservesDeliveredState(t *testing.T) {
	b := impl.NewInterestGridBroker(10)
	if err := b.SetRadius(1, 2); err != nil {
		t.Fatalf("set radius: %v", err)
	}
	if err := b.RegisterEntity(4, impl.Vec2{X: 5, Y: 5}); err != nil {
		t.Fatalf("register entity: %v", err)
	}
	if err := b.RegisterClient("c1", impl.Vec2{X: 5, Y: 5}); err != nil {
		t.Fatalf("register client: %v", err)
	}
	if d, err := b.Diff("c1"); err != nil || len(d.Entered) != 1 {
		t.Fatalf("first diff: entered=%d err=%v, want one entered entity", len(d.Entered), err)
	}
	payload, err := b.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	restored := impl.NewInterestGridBroker(10)
	if err := restored.Restore(payload); err != nil {
		t.Fatalf("restore: %v", err)
	}
	d, err := restored.Diff("c1")
	if err != nil {
		t.Fatalf("diff after restore: %v", err)
	}
	if len(d.Entered)+len(d.Left)+len(d.Updated) != 0 {
		t.Fatalf("restored diff repeated delivery: %+v", d)
	}
	if got := restored.Stats().Entities; got != 1 {
		t.Fatalf("restored entity count = %d, want 1", got)
	}
	if err := restored.Restore(nil); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("restore empty payload error = %v, want ErrBadSnapshot", err)
	}
}

func TestRemoveClientDropsSubscription(t *testing.T) {
	b := impl.NewInterestGridBroker(10)
	if err := b.SetRadius(1, 2); err != nil {
		t.Fatalf("set radius: %v", err)
	}
	if err := b.RegisterEntity(4, impl.Vec2{X: 5, Y: 5}); err != nil {
		t.Fatalf("register entity: %v", err)
	}
	if err := b.RegisterClient("c1", impl.Vec2{X: 5, Y: 5}); err != nil {
		t.Fatalf("register first client: %v", err)
	}
	if err := b.RegisterClient("c2", impl.Vec2{X: 5, Y: 5}); err != nil {
		t.Fatalf("register second client: %v", err)
	}
	if err := b.RemoveClient("c1"); err != nil {
		t.Fatalf("remove client: %v", err)
	}
	if _, err := b.Diff("c1"); !errors.Is(err, impl.ErrClientUnknown) {
		t.Fatalf("diff removed client error = %v, want ErrClientUnknown", err)
	}
	if got := b.Stats().Clients; got != 1 {
		t.Fatalf("client count = %d, want 1", got)
	}
}

func TestConcurrentMovesKeepSingleFinalState(t *testing.T) {
	b := impl.NewInterestGridBroker(10)
	if err := b.SetRadius(1, 2); err != nil {
		t.Fatalf("set radius: %v", err)
	}
	if err := b.RegisterEntity(6, impl.Vec2{X: 1, Y: 1}); err != nil {
		t.Fatalf("register entity: %v", err)
	}
	if err := b.RegisterClient("c1", impl.Vec2{X: 5, Y: 5}); err != nil {
		t.Fatalf("register client: %v", err)
	}
	if d, err := b.Diff("c1"); err != nil || len(d.Entered) != 1 {
		t.Fatalf("initial delivery: %+v err=%v", d, err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if err := b.MoveEntity(6, impl.Vec2{X: float64(n), Y: float64(n)}); err != nil {
				t.Errorf("concurrent move %d failed: %v", n, err)
			}
		}(i)
	}
	wg.Wait()
	d, err := b.Diff("c1")
	if err != nil {
		t.Fatalf("diff after concurrent moves: %v", err)
	}
	if len(d.Updated) != 1 || d.Updated[0].Entity != 6 {
		t.Fatalf("updated = %+v, want exactly one update for entity 6", d.Updated)
	}
	if len(d.Entered) != 0 || len(d.Left) != 0 {
		t.Fatalf("concurrent moves must not change subscription: %+v", d)
	}
}
