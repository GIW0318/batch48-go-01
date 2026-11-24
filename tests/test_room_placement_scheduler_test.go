package task

import (
	"errors"
	"sync"
	"testing"

	impl "gsb/batch48-go-game-a-05"
)

func TestNewSchedulerRejectsInvalidNodeLimit(t *testing.T) {
	if _, err := impl.NewRoomPlacementScheduler(0); !errors.Is(err, impl.ErrInvalidConfig) {
		t.Fatalf("zero node limit must report ErrInvalidConfig, got %v", err)
	}
	if _, err := impl.NewRoomPlacementScheduler(-2); !errors.Is(err, impl.ErrInvalidConfig) {
		t.Fatalf("negative node limit must report ErrInvalidConfig, got %v", err)
	}
	sched, err := impl.NewRoomPlacementScheduler(4)
	if err != nil {
		t.Fatalf("valid node limit must build a scheduler, got %v", err)
	}
	if _, ok := sched.Node("node-a"); ok {
		t.Fatalf("fresh scheduler must not know any node")
	}
}

func TestAddNodeValidatesAndRejectsDuplicates(t *testing.T) {
	sched, err := impl.NewRoomPlacementScheduler(4)
	if err != nil {
		t.Fatalf("scheduler must build, got %v", err)
	}
	if err := sched.AddNode("", "region-1", 10); !errors.Is(err, impl.ErrInvalidNode) {
		t.Fatalf("empty node id must report ErrInvalidNode, got %v", err)
	}
	if err := sched.AddNode("node-a", "", 10); !errors.Is(err, impl.ErrInvalidRegion) {
		t.Fatalf("empty region must report ErrInvalidRegion, got %v", err)
	}
	if err := sched.AddNode("node-a", "region-1", 0); !errors.Is(err, impl.ErrInvalidCapacity) {
		t.Fatalf("zero capacity must report ErrInvalidCapacity, got %v", err)
	}
	if err := sched.AddNode("node-a", "region-1", 10); err != nil {
		t.Fatalf("first node must be accepted, got %v", err)
	}
	if err := sched.AddNode("node-a", "region-1", 10); !errors.Is(err, impl.ErrNodeExists) {
		t.Fatalf("duplicate node must report ErrNodeExists, got %v", err)
	}
	limited, err := impl.NewRoomPlacementScheduler(1)
	if err != nil {
		t.Fatalf("scheduler must build, got %v", err)
	}
	if err := limited.AddNode("node-a", "region-1", 10); err != nil {
		t.Fatalf("first node must be accepted, got %v", err)
	}
	if err := limited.AddNode("node-b", "region-1", 10); !errors.Is(err, impl.ErrNodeLimit) {
		t.Fatalf("node above the cap must report ErrNodeLimit, got %v", err)
	}
}

func TestPlaceChoosesNodeInRequestedRegion(t *testing.T) {
	sched, err := impl.NewRoomPlacementScheduler(4)
	if err != nil {
		t.Fatalf("scheduler must build, got %v", err)
	}
	if err := sched.AddNode("node-a", "region-1", 10); err != nil {
		t.Fatalf("add node-a must succeed, got %v", err)
	}
	if err := sched.AddNode("node-b", "region-2", 10); err != nil {
		t.Fatalf("add node-b must succeed, got %v", err)
	}
	nodeID, err := sched.Place("room-1", impl.Seats(3), "region-1")
	if err != nil {
		t.Fatalf("place must succeed, got %v", err)
	}
	if nodeID != "node-a" {
		t.Fatalf("room landed on %q, want node-a of the requested region", nodeID)
	}
	placement, ok := sched.Placement("room-1")
	if !ok {
		t.Fatalf("placed room must be readable")
	}
	if placement.Node != "node-a" || placement.Seats != 3 || placement.Region != "region-1" {
		t.Fatalf("placement = %+v, want node-a with 3 seats in region-1", placement)
	}
	node, ok := sched.Node("node-a")
	if !ok || node.Used != 3 {
		t.Fatalf("node-a = %+v, want 3 used seats", node)
	}
	if free, err := sched.FreeSeats("node-a"); err != nil || free != 7 {
		t.Fatalf("free seats of node-a = %d err %v, want 7", free, err)
	}
}

func TestPlaceChoosesTheTightestFit(t *testing.T) {
	sched, err := impl.NewRoomPlacementScheduler(4)
	if err != nil {
		t.Fatalf("scheduler must build, got %v", err)
	}
	if err := sched.AddNode("node-big", "region-1", 10); err != nil {
		t.Fatalf("add node-big must succeed, got %v", err)
	}
	if err := sched.AddNode("node-small", "region-1", 4); err != nil {
		t.Fatalf("add node-small must succeed, got %v", err)
	}
	nodeID, err := sched.Place("room-1", impl.Seats(3), "region-1")
	if err != nil {
		t.Fatalf("place must succeed, got %v", err)
	}
	if nodeID != "node-small" {
		t.Fatalf("room landed on %q, want the tightest fit node-small", nodeID)
	}
	if free, err := sched.FreeSeats("node-small"); err != nil || free != 1 {
		t.Fatalf("free seats of node-small = %d err %v, want 1", free, err)
	}
	if free, err := sched.FreeSeats("node-big"); err != nil || free != 10 {
		t.Fatalf("free seats of node-big = %d err %v, want 10", free, err)
	}
}

func TestPlaceReportsCapacityShortage(t *testing.T) {
	sched, err := impl.NewRoomPlacementScheduler(4)
	if err != nil {
		t.Fatalf("scheduler must build, got %v", err)
	}
	if err := sched.AddNode("node-a", "region-1", 5); err != nil {
		t.Fatalf("add node must succeed, got %v", err)
	}
	if _, err := sched.Place("room-1", impl.Seats(5), "region-1"); err != nil {
		t.Fatalf("filling the node must succeed, got %v", err)
	}
	if _, err := sched.Place("room-2", impl.Seats(1), "region-1"); !errors.Is(err, impl.ErrInsufficientCapacity) {
		t.Fatalf("a full region must report ErrInsufficientCapacity, got %v", err)
	}
	if free, err := sched.FreeSeats("node-a"); err != nil || free != 0 {
		t.Fatalf("free seats after a rejected placement = %d err %v, want 0", free, err)
	}
	node, ok := sched.Node("node-a")
	if !ok || node.Used != 5 {
		t.Fatalf("node-a = %+v, want 5 used seats", node)
	}
	if _, ok := sched.Placement("room-2"); ok {
		t.Fatalf("rejected room must not gain a placement")
	}
}

func TestPlaceRejectsUnknownRegion(t *testing.T) {
	sched, err := impl.NewRoomPlacementScheduler(4)
	if err != nil {
		t.Fatalf("scheduler must build, got %v", err)
	}
	if err := sched.AddNode("node-a", "region-1", 10); err != nil {
		t.Fatalf("add node must succeed, got %v", err)
	}
	if _, err := sched.Place("room-1", impl.Seats(2), "region-9"); !errors.Is(err, impl.ErrNoNodeInRegion) {
		t.Fatalf("unknown region must report ErrNoNodeInRegion, got %v", err)
	}
}

func TestPlaceValidatesSeatsAndDuplicateRoom(t *testing.T) {
	sched, err := impl.NewRoomPlacementScheduler(4)
	if err != nil {
		t.Fatalf("scheduler must build, got %v", err)
	}
	if err := sched.AddNode("node-a", "region-1", 10); err != nil {
		t.Fatalf("add node must succeed, got %v", err)
	}
	if _, err := sched.Place("room-1", impl.Seats(0), "region-1"); !errors.Is(err, impl.ErrInvalidSeats) {
		t.Fatalf("zero seats must report ErrInvalidSeats, got %v", err)
	}
	if _, err := sched.Place("room-1", impl.Seats(-2), "region-1"); !errors.Is(err, impl.ErrInvalidSeats) {
		t.Fatalf("negative seats must report ErrInvalidSeats, got %v", err)
	}
	if _, err := sched.Place("", impl.Seats(2), "region-1"); !errors.Is(err, impl.ErrInvalidRoom) {
		t.Fatalf("empty room id must report ErrInvalidRoom, got %v", err)
	}
	if _, err := sched.Place("room-1", impl.Seats(2), ""); !errors.Is(err, impl.ErrInvalidRegion) {
		t.Fatalf("empty region must report ErrInvalidRegion, got %v", err)
	}
	if _, err := sched.Place("room-1", impl.Seats(2), "region-1"); err != nil {
		t.Fatalf("first placement must succeed, got %v", err)
	}
	if _, err := sched.Place("room-1", impl.Seats(1), "region-1"); !errors.Is(err, impl.ErrRoomExists) {
		t.Fatalf("placing the same room twice must report ErrRoomExists, got %v", err)
	}
}

func TestReleaseRestoresCapacity(t *testing.T) {
	sched, err := impl.NewRoomPlacementScheduler(4)
	if err != nil {
		t.Fatalf("scheduler must build, got %v", err)
	}
	if err := sched.AddNode("node-a", "region-1", 10); err != nil {
		t.Fatalf("add node must succeed, got %v", err)
	}
	if _, err := sched.Place("room-1", impl.Seats(4), "region-1"); err != nil {
		t.Fatalf("place must succeed, got %v", err)
	}
	if free, err := sched.FreeSeats("node-a"); err != nil || free != 6 {
		t.Fatalf("free seats after place = %d err %v, want 6", free, err)
	}
	if err := sched.Release("node-a", "room-1"); err != nil {
		t.Fatalf("release must succeed, got %v", err)
	}
	if free, err := sched.FreeSeats("node-a"); err != nil || free != 10 {
		t.Fatalf("free seats after release = %d err %v, want 10", free, err)
	}
	if _, ok := sched.Placement("room-1"); ok {
		t.Fatalf("released room must not stay placed")
	}
	if err := sched.Release("node-a", "room-1"); !errors.Is(err, impl.ErrPlacementNotFound) {
		t.Fatalf("second release must report ErrPlacementNotFound, got %v", err)
	}
	if err := sched.Release("node-missing", "room-1"); !errors.Is(err, impl.ErrNodeNotFound) {
		t.Fatalf("release on an unknown node must report ErrNodeNotFound, got %v", err)
	}
}

func TestConcurrentPlaceNeverOversells(t *testing.T) {
	sched, err := impl.NewRoomPlacementScheduler(4)
	if err != nil {
		t.Fatalf("scheduler must build, got %v", err)
	}
	if err := sched.AddNode("node-a", "region-1", 10); err != nil {
		t.Fatalf("add node must succeed, got %v", err)
	}
	const callers = 6
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, placeErr := sched.Place("room-"+string(rune('a'+n)), impl.Seats(3), "region-1")
			mu.Lock()
			errs = append(errs, placeErr)
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	placed := 0
	rejected := 0
	for _, placeErr := range errs {
		switch {
		case placeErr == nil:
			placed++
		case errors.Is(placeErr, impl.ErrInsufficientCapacity):
			rejected++
		default:
			t.Fatalf("unexpected place error %v", placeErr)
		}
	}
	if placed != 3 || rejected != callers-3 {
		t.Fatalf("concurrent place produced %d placements and %d rejections, want 3 and %d", placed, rejected, callers-3)
	}
	node, ok := sched.Node("node-a")
	if !ok || node.Used != 9 {
		t.Fatalf("node-a = %+v, want 9 used seats", node)
	}
	if free, err := sched.FreeSeats("node-a"); err != nil || free != 1 {
		t.Fatalf("free seats after concurrent place = %d err %v, want 1", free, err)
	}
}

func TestReserveBooksMigrationCapacity(t *testing.T) {
	sched, err := impl.NewRoomPlacementScheduler(4)
	if err != nil {
		t.Fatalf("scheduler must build, got %v", err)
	}
	if err := sched.AddNode("node-a", "region-1", 10); err != nil {
		t.Fatalf("add node-a must succeed, got %v", err)
	}
	if err := sched.AddNode("node-b", "region-2", 5); err != nil {
		t.Fatalf("add node-b must succeed, got %v", err)
	}
	if _, err := sched.Place("room-1", impl.Seats(4), "region-1"); err != nil {
		t.Fatalf("place must succeed, got %v", err)
	}
	if err := sched.Reserve("node-b", "room-1"); err != nil {
		t.Fatalf("reserve must succeed, got %v", err)
	}
	if free, err := sched.FreeSeats("node-b"); err != nil || free != 1 {
		t.Fatalf("free seats of node-b after reserve = %d err %v, want 1", free, err)
	}
	node, ok := sched.Node("node-b")
	if !ok || node.Reserved != 4 || node.Used != 0 {
		t.Fatalf("node-b = %+v, want 4 reserved and 0 used seats", node)
	}
	if err := sched.Reserve("node-b", "room-1"); !errors.Is(err, impl.ErrAlreadyReserved) {
		t.Fatalf("second reserve must report ErrAlreadyReserved, got %v", err)
	}
	if err := sched.Reserve("node-a", "room-1"); !errors.Is(err, impl.ErrSameNode) {
		t.Fatalf("reserving the current node must report ErrSameNode, got %v", err)
	}
}

func TestReserveRejectsUnknownTargetsAndShortCapacity(t *testing.T) {
	sched, err := impl.NewRoomPlacementScheduler(4)
	if err != nil {
		t.Fatalf("scheduler must build, got %v", err)
	}
	for _, node := range []struct {
		id       impl.NodeID
		capacity int
	}{
		{"node-a", 10},
		{"node-small", 2},
	} {
		if err := sched.AddNode(node.id, "region-1", node.capacity); err != nil {
			t.Fatalf("add %s must succeed, got %v", node.id, err)
		}
	}
	if err := sched.Reserve("node-missing", "room-1"); !errors.Is(err, impl.ErrNodeNotFound) {
		t.Fatalf("reserve on an unknown node must report ErrNodeNotFound, got %v", err)
	}
	if err := sched.Reserve("node-a", "ghost"); !errors.Is(err, impl.ErrRoomNotFound) {
		t.Fatalf("reserve for an unplaced room must report ErrRoomNotFound, got %v", err)
	}
	if _, err := sched.Place("room-1", impl.Seats(4), "region-1"); err != nil {
		t.Fatalf("place must succeed, got %v", err)
	}
	if err := sched.Reserve("node-small", "room-1"); !errors.Is(err, impl.ErrInsufficientCapacity) {
		t.Fatalf("reserve without capacity must report ErrInsufficientCapacity, got %v", err)
	}
	if free, err := sched.FreeSeats("node-small"); err != nil || free != 2 {
		t.Fatalf("free seats after a rejected reserve = %d err %v, want 2", free, err)
	}
}

func TestMigrateMovesSeatsBetweenNodes(t *testing.T) {
	sched, err := impl.NewRoomPlacementScheduler(4)
	if err != nil {
		t.Fatalf("scheduler must build, got %v", err)
	}
	if err := sched.AddNode("node-a", "region-1", 10); err != nil {
		t.Fatalf("add node-a must succeed, got %v", err)
	}
	if err := sched.AddNode("node-b", "region-2", 6); err != nil {
		t.Fatalf("add node-b must succeed, got %v", err)
	}
	if _, err := sched.Place("room-1", impl.Seats(4), "region-1"); err != nil {
		t.Fatalf("place must succeed, got %v", err)
	}
	if err := sched.Migrate("room-1", "node-b"); err != nil {
		t.Fatalf("migrate must succeed, got %v", err)
	}
	placement, ok := sched.Placement("room-1")
	if !ok || placement.Node != "node-b" || placement.Reserved != "" {
		t.Fatalf("placement after migrate = %+v, want node-b without a reservation", placement)
	}
	if free, err := sched.FreeSeats("node-a"); err != nil || free != 10 {
		t.Fatalf("source free seats = %d err %v, want 10", free, err)
	}
	if free, err := sched.FreeSeats("node-b"); err != nil || free != 2 {
		t.Fatalf("target free seats = %d err %v, want 2", free, err)
	}
	node, ok := sched.Node("node-b")
	if !ok || node.Used != 4 {
		t.Fatalf("node-b = %+v, want 4 used seats", node)
	}
	if err := sched.Migrate("room-1", "node-b"); !errors.Is(err, impl.ErrSameNode) {
		t.Fatalf("migrating onto the current node must report ErrSameNode, got %v", err)
	}
	if err := sched.Migrate("room-1", "node-missing"); !errors.Is(err, impl.ErrNodeNotFound) {
		t.Fatalf("migrating to an unknown node must report ErrNodeNotFound, got %v", err)
	}
	if err := sched.Migrate("ghost", "node-b"); !errors.Is(err, impl.ErrRoomNotFound) {
		t.Fatalf("migrating an unknown room must report ErrRoomNotFound, got %v", err)
	}
}

func TestMigrateConsumesTheReservation(t *testing.T) {
	sched, err := impl.NewRoomPlacementScheduler(4)
	if err != nil {
		t.Fatalf("scheduler must build, got %v", err)
	}
	if err := sched.AddNode("node-a", "region-1", 10); err != nil {
		t.Fatalf("add node-a must succeed, got %v", err)
	}
	if err := sched.AddNode("node-b", "region-2", 6); err != nil {
		t.Fatalf("add node-b must succeed, got %v", err)
	}
	if _, err := sched.Place("room-1", impl.Seats(4), "region-1"); err != nil {
		t.Fatalf("place must succeed, got %v", err)
	}
	if err := sched.Reserve("node-b", "room-1"); err != nil {
		t.Fatalf("reserve must succeed, got %v", err)
	}
	if _, err := sched.Place("room-2", impl.Seats(2), "region-2"); err != nil {
		t.Fatalf("filling the rest of node-b must succeed, got %v", err)
	}
	if err := sched.Migrate("room-1", "node-b"); err != nil {
		t.Fatalf("migrate onto the reserved node must succeed, got %v", err)
	}
	node, ok := sched.Node("node-b")
	if !ok || node.Used != 6 || node.Reserved != 0 {
		t.Fatalf("node-b = %+v, want 6 used and 0 reserved seats", node)
	}
	if free, err := sched.FreeSeats("node-b"); err != nil || free != 0 {
		t.Fatalf("free seats of node-b = %d err %v, want 0", free, err)
	}
	if free, err := sched.FreeSeats("node-a"); err != nil || free != 10 {
		t.Fatalf("free seats of node-a = %d err %v, want 10", free, err)
	}
}

func TestDrainMarksTheNode(t *testing.T) {
	sched, err := impl.NewRoomPlacementScheduler(4)
	if err != nil {
		t.Fatalf("scheduler must build, got %v", err)
	}
	if err := sched.AddNode("node-a", "region-1", 10); err != nil {
		t.Fatalf("add node must succeed, got %v", err)
	}
	if err := sched.Drain("node-missing"); !errors.Is(err, impl.ErrNodeNotFound) {
		t.Fatalf("draining an unknown node must report ErrNodeNotFound, got %v", err)
	}
	if err := sched.Drain("node-a"); err != nil {
		t.Fatalf("drain must succeed, got %v", err)
	}
	node, ok := sched.Node("node-a")
	if !ok || !node.Draining {
		t.Fatalf("node-a = %+v, want the draining flag set", node)
	}
	if err := sched.Drain("node-a"); !errors.Is(err, impl.ErrAlreadyDraining) {
		t.Fatalf("draining twice must report ErrAlreadyDraining, got %v", err)
	}
}

func TestSnapshotRestoreKeepsPlacementsAndReservations(t *testing.T) {
	sched, err := impl.NewRoomPlacementScheduler(4)
	if err != nil {
		t.Fatalf("scheduler must build, got %v", err)
	}
	if err := sched.AddNode("node-a", "region-1", 10); err != nil {
		t.Fatalf("add node-a must succeed, got %v", err)
	}
	if err := sched.AddNode("node-b", "region-2", 6); err != nil {
		t.Fatalf("add node-b must succeed, got %v", err)
	}
	if _, err := sched.Place("room-1", impl.Seats(4), "region-1"); err != nil {
		t.Fatalf("place must succeed, got %v", err)
	}
	if err := sched.Reserve("node-b", "room-1"); err != nil {
		t.Fatalf("reserve must succeed, got %v", err)
	}
	data, err := sched.Snapshot()
	if err != nil {
		t.Fatalf("snapshot must succeed, got %v", err)
	}
	restored, err := impl.NewRoomPlacementScheduler(4)
	if err != nil {
		t.Fatalf("scheduler must build, got %v", err)
	}
	if err := restored.Restore(data); err != nil {
		t.Fatalf("restore must succeed, got %v", err)
	}
	nodeA, ok := restored.Node("node-a")
	if !ok || nodeA.Used != 4 {
		t.Fatalf("restored node-a = %+v, want 4 used seats", nodeA)
	}
	nodeB, ok := restored.Node("node-b")
	if !ok || nodeB.Reserved != 4 {
		t.Fatalf("restored node-b = %+v, want 4 reserved seats", nodeB)
	}
	placement, ok := restored.Placement("room-1")
	if !ok || placement.Node != "node-a" || placement.Reserved != "node-b" {
		t.Fatalf("restored placement = %+v, want room-1 on node-a reserved for node-b", placement)
	}
	if free, err := restored.FreeSeats("node-a"); err != nil || free != 6 {
		t.Fatalf("restored free seats of node-a = %d err %v, want 6", free, err)
	}
	if _, err := restored.Place("room-2", impl.Seats(6), "region-1"); err != nil {
		t.Fatalf("placing the exact remaining capacity must succeed, got %v", err)
	}
	if _, err := restored.Place("room-3", impl.Seats(1), "region-1"); !errors.Is(err, impl.ErrInsufficientCapacity) {
		t.Fatalf("a placement past the restored capacity must report ErrInsufficientCapacity, got %v", err)
	}
	if err := restored.Restore([]byte("{")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("a broken snapshot must report ErrBadSnapshot, got %v", err)
	}
}
