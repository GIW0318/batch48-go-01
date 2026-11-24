package task

import (
	"errors"
	"testing"

	impl "gsb/batch48-go-game-a-14"
)

// budget4800 leaves 240 bytes per tick at 20 ticks per second: header plus
// nine entity records.
const budget4800 = 4800

func newGovernor() *impl.SnapshotGovernor {
	return impl.NewSnapshotGovernor(20)
}

func mustBudget(t *testing.T, g *impl.SnapshotGovernor, clientID string, bps int) {
	t.Helper()
	if err := g.SetBudget(clientID, bps); err != nil {
		t.Fatalf("set budget %d for %s: %v", bps, clientID, err)
	}
}

func mustDirty(t *testing.T, g *impl.SnapshotGovernor, clientID string, entity impl.EntityID, priority int) {
	t.Helper()
	if err := g.MarkDirty(clientID, entity, priority); err != nil {
		t.Fatalf("mark dirty %d with priority %d: %v", entity, priority, err)
	}
}

func idsEqual(got, want []impl.EntityID) bool {
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

func TestSetBudgetRejectsInvalidInput(t *testing.T) {
	g := newGovernor()
	if err := g.SetBudget("c1", 0); !errors.Is(err, impl.ErrInvalidBudget) {
		t.Fatalf("budget 0: got %v, want ErrInvalidBudget", err)
	}
	if err := g.SetBudget("c1", -32); !errors.Is(err, impl.ErrInvalidBudget) {
		t.Fatalf("negative budget: got %v, want ErrInvalidBudget", err)
	}
	if err := g.SetBudget("", 4800); !errors.Is(err, impl.ErrInvalidClient) {
		t.Fatalf("empty client id: got %v, want ErrInvalidClient", err)
	}
}

func TestBuildWithoutDirtyEntitiesSendsHeaderOnly(t *testing.T) {
	g := newGovernor()
	mustBudget(t, g, "c1", budget4800)
	plan, err := g.Build("c1", 1)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if plan.Bytes != impl.PacketHeaderBytes {
		t.Fatalf("bytes = %d, want the %d byte header only", plan.Bytes, impl.PacketHeaderBytes)
	}
	if len(plan.EntityIDs) != 0 || len(plan.DestroyIDs) != 0 {
		t.Fatalf("plan carried entities %v and destroys %v, want none", plan.EntityIDs, plan.DestroyIDs)
	}
	if !plan.Full {
		t.Fatalf("first packet without any acknowledged baseline must be full")
	}
}

func TestBuildSendsDirtyEntitiesInPriorityOrder(t *testing.T) {
	g := newGovernor()
	mustBudget(t, g, "c1", budget4800)
	mustDirty(t, g, "c1", 1, 1)
	mustDirty(t, g, "c1", 2, 9)
	mustDirty(t, g, "c1", 3, 5)
	plan, err := g.Build("c1", 7)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !idsEqual(plan.EntityIDs, []impl.EntityID{2, 3, 1}) {
		t.Fatalf("entity order = %v, want priority order [2 3 1]", plan.EntityIDs)
	}
	if len(plan.Skipped) != 0 {
		t.Fatalf("skipped = %v, want none with 240 bytes per tick", plan.Skipped)
	}
	if plan.Tick != 7 || plan.Bytes != impl.PacketHeaderBytes+3*impl.EntityRecordBytes {
		t.Fatalf("plan tick=%d bytes=%d, want tick 7 and %d bytes", plan.Tick, plan.Bytes, impl.PacketHeaderBytes+3*impl.EntityRecordBytes)
	}
}

func TestBuildTruncatesByBudget(t *testing.T) {
	g := newGovernor()
	// 1280 bytes per second at 20 ticks per second leaves room for exactly
	// two entity records next to the header.
	mustBudget(t, g, "c1", 1280)
	mustDirty(t, g, "c1", 1, 1)
	mustDirty(t, g, "c1", 2, 9)
	mustDirty(t, g, "c1", 3, 5)
	plan, err := g.Build("c1", 2)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !idsEqual(plan.EntityIDs, []impl.EntityID{2, 3}) {
		t.Fatalf("sent entities = %v, want the two highest priorities [2 3]", plan.EntityIDs)
	}
	if !idsEqual(plan.Skipped, []impl.EntityID{1}) {
		t.Fatalf("skipped = %v, want [1]", plan.Skipped)
	}
	if plan.Bytes != 64 {
		t.Fatalf("bytes = %d, want the 64 byte budget consumed exactly", plan.Bytes)
	}
}

func TestBuildCountsBytesExactly(t *testing.T) {
	g := newGovernor()
	mustBudget(t, g, "c1", budget4800)
	mustDirty(t, g, "c1", 4, 5)
	mustDirty(t, g, "c1", 5, 4)
	plan, err := g.Build("c1", 3)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	want := impl.PacketHeaderBytes + 2*impl.EntityRecordBytes
	if plan.Bytes != want {
		t.Fatalf("bytes = %d, want %d for a header plus two entity records", plan.Bytes, want)
	}
	if plan.Bytes > budget4800/20 {
		t.Fatalf("bytes = %d exceeds the 240 byte tick budget", plan.Bytes)
	}
}

func TestBuildRejectsUnknownClient(t *testing.T) {
	g := newGovernor()
	_, err := g.Build("ghost", 1)
	if !errors.Is(err, impl.ErrUnknownClient) {
		t.Fatalf("build for a client without a budget: got %v, want ErrUnknownClient", err)
	}
	if err := g.Ack("ghost", 1); !errors.Is(err, impl.ErrUnknownClient) {
		t.Fatalf("ack for a client without a budget: got %v, want ErrUnknownClient", err)
	}
}

func TestMarkDirtyRejectsInvalidEntity(t *testing.T) {
	g := newGovernor()
	mustBudget(t, g, "c1", budget4800)
	if err := g.MarkDirty("c1", 0, 5); !errors.Is(err, impl.ErrInvalidEntity) {
		t.Fatalf("entity id 0: got %v, want ErrInvalidEntity", err)
	}
	if err := g.MarkDirty("ghost", 3, 5); !errors.Is(err, impl.ErrUnknownClient) {
		t.Fatalf("dirty mark for an unknown client: got %v, want ErrUnknownClient", err)
	}
	if got := g.Pending("c1"); got != 0 {
		t.Fatalf("pending = %d after rejected marks, want 0", got)
	}
}

func TestSkippedEntityIsPromotedNextBuild(t *testing.T) {
	g := newGovernor()
	// 800 bytes per second at 20 ticks per second fits a single entity.
	mustBudget(t, g, "c1", 800)
	mustDirty(t, g, "c1", 1, 10)
	mustDirty(t, g, "c1", 2, 1)
	first, err := g.Build("c1", 1)
	if err != nil {
		t.Fatalf("first build: %v", err)
	}
	if !idsEqual(first.EntityIDs, []impl.EntityID{1}) || !idsEqual(first.Skipped, []impl.EntityID{2}) {
		t.Fatalf("first build sent %v and skipped %v, want [1] and [2]", first.EntityIDs, first.Skipped)
	}
	second, err := g.Build("c1", 2)
	if err != nil {
		t.Fatalf("second build: %v", err)
	}
	if !idsEqual(second.EntityIDs, []impl.EntityID{2}) {
		t.Fatalf("second build sent %v, want the promoted entity [2] to win the slot", second.EntityIDs)
	}
	if !idsEqual(second.Skipped, []impl.EntityID{1}) {
		t.Fatalf("second build skipped %v, want [1]", second.Skipped)
	}
}

func TestBuildDegradesToFullWithoutAck(t *testing.T) {
	g := newGovernor()
	mustBudget(t, g, "c1", budget4800)
	mustDirty(t, g, "c1", 1, 5)
	plan, err := g.Build("c1", 5)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !plan.Full || plan.Baseline != 0 {
		t.Fatalf("first plan full=%v baseline=%d, want full with baseline 0", plan.Full, plan.Baseline)
	}
	if err := g.Ack("c1", 5); err != nil {
		t.Fatalf("ack tick 5: %v", err)
	}
	next, err := g.Build("c1", 6)
	if err != nil {
		t.Fatalf("build after ack: %v", err)
	}
	if next.Full {
		t.Fatalf("plan after a confirmed baseline must be incremental")
	}
	if next.Baseline != 5 {
		t.Fatalf("baseline = %d, want the acknowledged tick 5", next.Baseline)
	}
}

func TestAckNeverMovesBackwards(t *testing.T) {
	g := newGovernor()
	mustBudget(t, g, "c1", budget4800)
	if err := g.Ack("c1", 10); err != nil {
		t.Fatalf("ack tick 10: %v", err)
	}
	if err := g.Ack("c1", 4); err != nil {
		t.Fatalf("late ack of tick 4 must not fail, got %v", err)
	}
	if got := g.Acked("c1"); got != 10 {
		t.Fatalf("acked = %d after a late ack, want it to stay at 10", got)
	}
}

func TestAckPrunesDirtyEntities(t *testing.T) {
	g := newGovernor()
	mustBudget(t, g, "c1", budget4800)
	mustDirty(t, g, "c1", 1, 5)
	plan, err := g.Build("c1", 5)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !idsEqual(plan.EntityIDs, []impl.EntityID{1}) {
		t.Fatalf("plan sent %v, want [1]", plan.EntityIDs)
	}
	if err := g.Ack("c1", 5); err != nil {
		t.Fatalf("ack tick 5: %v", err)
	}
	if got := g.Pending("c1"); got != 0 {
		t.Fatalf("pending after ack = %d, want 0", got)
	}
	next, err := g.Build("c1", 6)
	if err != nil {
		t.Fatalf("build after ack: %v", err)
	}
	if len(next.EntityIDs) != 0 {
		t.Fatalf("build after ack sent %v, want no entity resent before it is dirtied again", next.EntityIDs)
	}
}

func TestBuildOrdersEqualPriorityByID(t *testing.T) {
	g := newGovernor()
	mustBudget(t, g, "c1", budget4800)
	for _, entity := range []impl.EntityID{7, 3, 5} {
		mustDirty(t, g, "c1", entity, 4)
	}
	plan, err := g.Build("c1", 1)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !idsEqual(plan.EntityIDs, []impl.EntityID{3, 5, 7}) {
		t.Fatalf("entity order = %v, want ascending ids [3 5 7] on equal priority", plan.EntityIDs)
	}
}

func TestLeaveEmitsDestroyInSamePacket(t *testing.T) {
	g := newGovernor()
	mustBudget(t, g, "c1", budget4800)
	mustDirty(t, g, "c1", 1, 5)
	mustDirty(t, g, "c1", 2, 1)
	if err := g.Leave("c1", 1); err != nil {
		t.Fatalf("leave entity 1: %v", err)
	}
	plan, err := g.Build("c1", 9)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !idsEqual(plan.DestroyIDs, []impl.EntityID{1}) {
		t.Fatalf("destroy ids = %v, want [1] in the same packet", plan.DestroyIDs)
	}
	if !idsEqual(plan.EntityIDs, []impl.EntityID{2}) {
		t.Fatalf("entity ids = %v, want only the entity that did not leave [2]", plan.EntityIDs)
	}
	want := impl.PacketHeaderBytes + impl.EntityRecordBytes + impl.DestroyRecordBytes
	if plan.Bytes != want {
		t.Fatalf("bytes = %d, want %d for a header, one record and one destroy", plan.Bytes, want)
	}
}

func TestBuildReportsSkippedInAscendingIDOrder(t *testing.T) {
	g := newGovernor()
	mustBudget(t, g, "c1", 800)
	mustDirty(t, g, "c1", 7, 9)
	mustDirty(t, g, "c1", 3, 1)
	mustDirty(t, g, "c1", 5, 1)
	mustDirty(t, g, "c1", 2, 1)
	plan, err := g.Build("c1", 1)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !idsEqual(plan.EntityIDs, []impl.EntityID{7}) {
		t.Fatalf("sent entities = %v, want only [7] inside 40 bytes", plan.EntityIDs)
	}
	if !idsEqual(plan.Skipped, []impl.EntityID{2, 3, 5}) {
		t.Fatalf("skipped = %v, want ascending [2 3 5]", plan.Skipped)
	}
	if plan.Bytes != 40 {
		t.Fatalf("bytes = %d, want the 40 byte tick budget", plan.Bytes)
	}
}

func TestSnapshotRestoreKeepsDirtySet(t *testing.T) {
	g := newGovernor()
	mustBudget(t, g, "c1", budget4800)
	mustDirty(t, g, "c1", 1, 5)
	mustDirty(t, g, "c1", 2, 1)
	data, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	mustDirty(t, g, "c1", 3, 9)
	if err := g.Restore(data); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := g.Pending("c1"); got != 2 {
		t.Fatalf("pending after restore = %d, want the two entities of the snapshot", got)
	}
	if got := g.Budget("c1"); got != budget4800 {
		t.Fatalf("budget after restore = %d, want %d", got, budget4800)
	}
	plan, err := g.Build("c1", 4)
	if err != nil {
		t.Fatalf("build after restore: %v", err)
	}
	if !idsEqual(plan.EntityIDs, []impl.EntityID{1, 2}) {
		t.Fatalf("entities after restore = %v, want [1 2] without the later dirty mark", plan.EntityIDs)
	}
	if err := g.Restore([]byte("broken")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("restore of garbage: got %v, want ErrBadSnapshot", err)
	}
	if got := g.Pending("c1"); got != 2 {
		t.Fatalf("pending after rejected restore = %d, want the previous 2", got)
	}
}
