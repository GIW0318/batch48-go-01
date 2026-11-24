package task

import (
	"errors"
	"testing"
	"time"

	impl "gsb/batch48-go-game-a-11"
)

func newTestClock() *impl.InterpolationClock {
	return impl.NewInterpolationClock(20, 0)
}

func almost(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-6
}

func entityAt(st impl.State, id uint32) (impl.EntityState, bool) {
	for _, e := range st.Entities {
		if e.EntityID == id {
			return e, true
		}
	}
	return impl.EntityState{}, false
}

// twoTickClock buffers tick 100 (x=0) and tick 110 (x=100) for entity 1.
func twoTickClock(t *testing.T) *impl.InterpolationClock {
	t.Helper()
	c := newTestClock()
	if err := c.Push(impl.Snapshot{Tick: 100, Entities: []impl.EntityState{{EntityID: 1, X: 0, Y: 0}}}); err != nil {
		t.Fatalf("push tick 100: %v", err)
	}
	if err := c.Push(impl.Snapshot{Tick: 110, Entities: []impl.EntityState{{EntityID: 1, X: 100, Y: 0}}}); err != nil {
		t.Fatalf("push tick 110: %v", err)
	}
	return c
}

func TestPushRejectsZeroTick(t *testing.T) {
	c := newTestClock()
	err := c.Push(impl.Snapshot{Tick: 0, Entities: []impl.EntityState{{EntityID: 1, X: 3}}})
	if !errors.Is(err, impl.ErrInvalidTick) {
		t.Fatalf("push with tick 0: got %v, want ErrInvalidTick", err)
	}
	if got := c.Queued(); got != 0 {
		t.Fatalf("queued after rejected push = %d, want 0", got)
	}
}

func TestPushRejectsDuplicateTick(t *testing.T) {
	c := newTestClock()
	if err := c.Push(impl.Snapshot{Tick: 100, Entities: []impl.EntityState{{EntityID: 1, X: 1}}}); err != nil {
		t.Fatalf("first push of tick 100: %v", err)
	}
	err := c.Push(impl.Snapshot{Tick: 100, Entities: []impl.EntityState{{EntityID: 1, X: 9}}})
	if !errors.Is(err, impl.ErrDuplicateTick) {
		t.Fatalf("second push of tick 100: got %v, want ErrDuplicateTick", err)
	}
	if got := c.Queued(); got != 1 {
		t.Fatalf("queued after duplicate = %d, want 1", got)
	}
	if got := c.Dropped(); got != 1 {
		t.Fatalf("dropped after duplicate = %d, want 1", got)
	}
}

func TestPushKeepsSnapshotsInAscendingTickOrder(t *testing.T) {
	c := newTestClock()
	for _, tick := range []uint64{120, 100, 110} {
		if err := c.Push(impl.Snapshot{Tick: tick}); err != nil {
			t.Fatalf("push tick %d: %v", tick, err)
		}
	}
	got := c.Ticks()
	want := []uint64{100, 110, 120}
	if len(got) != len(want) {
		t.Fatalf("ticks = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ticks = %v, want %v", got, want)
		}
	}
	if q := c.Queued(); q != 3 {
		t.Fatalf("queued = %d, want 3", q)
	}
}

func TestTickSpanCoversQueuedSnapshots(t *testing.T) {
	c := twoTickClock(t)
	if got := c.TickSpan(); got != 500*time.Millisecond {
		t.Fatalf("tick span = %v, want 500ms for a 10 tick window at 20 ticks per second", got)
	}
}

func TestPushEvictsOldestBeyondBufferCap(t *testing.T) {
	c := newTestClock()
	for tick := uint64(1); tick <= 40; tick++ {
		if err := c.Push(impl.Snapshot{Tick: tick}); err != nil {
			t.Fatalf("push tick %d: %v", tick, err)
		}
	}
	if got := c.Queued(); got != impl.SnapshotBufferCap {
		t.Fatalf("queued = %d, want the hard cap %d", got, impl.SnapshotBufferCap)
	}
	if got := c.Dropped(); got != 8 {
		t.Fatalf("dropped = %d, want 8 evicted snapshots", got)
	}
	ticks := c.Ticks()
	if len(ticks) == 0 {
		t.Fatalf("ticks is empty after 40 pushes")
	}
	if ticks[0] != 9 {
		t.Fatalf("oldest buffered tick = %d, want 9", ticks[0])
	}
	if ticks[len(ticks)-1] != 40 {
		t.Fatalf("newest buffered tick = %d, want 40", ticks[len(ticks)-1])
	}
}

func TestSampleInterpolatesBetweenSnapshots(t *testing.T) {
	c := twoTickClock(t)
	st, ok := c.Sample(105)
	if !ok {
		t.Fatalf("sample at tick 105 reported no data")
	}
	if st.Tick != 110 {
		t.Fatalf("state tick = %d, want the newer bracketing snapshot tick 110", st.Tick)
	}
	e, found := entityAt(st, 1)
	if !found {
		t.Fatalf("entity 1 missing from sampled state")
	}
	if !almost(e.X, 50) {
		t.Fatalf("interpolated x = %v, want 50 at the middle of the interval", e.X)
	}
	if st.Extrapolated {
		t.Fatalf("state inside the timeline must not be marked extrapolated")
	}
}

func TestSampleRejectsTickBeforeTimeline(t *testing.T) {
	c := twoTickClock(t)
	if _, ok := c.Sample(50); ok {
		t.Fatalf("sample at tick 50 must report no data, the timeline starts at 100")
	}
	if st, ok := c.Sample(0); ok {
		t.Fatalf("sample at tick 0 must report no data, got tick %d", st.Tick)
	}
}

func TestSampleOnNewestTickReturnsSnapshot(t *testing.T) {
	c := twoTickClock(t)
	st, ok := c.Sample(110)
	if !ok {
		t.Fatalf("sample at the newest tick reported no data")
	}
	e, found := entityAt(st, 1)
	if !found {
		t.Fatalf("entity 1 missing from sampled state")
	}
	if !almost(e.X, 100) {
		t.Fatalf("x = %v at the newest tick, want 100", e.X)
	}
	if st.Extrapolated || st.Stale {
		t.Fatalf("exact snapshot sample must not be extrapolated or stale, got extrapolated=%v stale=%v", st.Extrapolated, st.Stale)
	}
}

func TestSampleSnapsAcrossTeleport(t *testing.T) {
	c := twoTickClock(t)
	c.SetTeleportThreshold(5)
	st, ok := c.Sample(105)
	if !ok {
		t.Fatalf("sample at tick 105 reported no data")
	}
	e, found := entityAt(st, 1)
	if !found {
		t.Fatalf("entity 1 missing from sampled state")
	}
	if !almost(e.X, 100) {
		t.Fatalf("x = %v after a jump above the teleport threshold, want 100 from the newer snapshot", e.X)
	}
	if st.Extrapolated {
		t.Fatalf("teleport snap must not be reported as extrapolation")
	}
}

func TestSampleInterpolatesBelowTeleportThreshold(t *testing.T) {
	c := twoTickClock(t)
	c.SetTeleportThreshold(500)
	if got := c.TeleportThreshold(); !almost(got, 500) {
		t.Fatalf("teleport threshold = %v, want 500", got)
	}
	st, ok := c.Sample(105)
	if !ok {
		t.Fatalf("sample at tick 105 reported no data")
	}
	e, found := entityAt(st, 1)
	if !found {
		t.Fatalf("entity 1 missing from sampled state")
	}
	if !almost(e.X, 50) {
		t.Fatalf("x = %v below the teleport threshold, want interpolated 50", e.X)
	}
}

func TestExtrapolationFreezesAtLimit(t *testing.T) {
	c := twoTickClock(t)
	c.SetExtrapolationLimit(50 * time.Millisecond)
	if got := c.ExtrapolationLimit(); got != 50*time.Millisecond {
		t.Fatalf("extrapolation limit = %v, want 50ms", got)
	}
	st, ok := c.Sample(125)
	if !ok {
		t.Fatalf("sample past the newest snapshot reported no data")
	}
	e, found := entityAt(st, 1)
	if !found {
		t.Fatalf("entity 1 missing from sampled state")
	}
	if !almost(e.X, 110) {
		t.Fatalf("x = %v after the extrapolation limit, want frozen 110", e.X)
	}
	if !st.Extrapolated {
		t.Fatalf("state past the newest snapshot must be marked extrapolated")
	}
	if !st.Stale {
		t.Fatalf("state frozen at the extrapolation limit must be marked stale")
	}
	if !c.Stale() {
		t.Fatalf("clock must remember that the last sample went stale")
	}
}

func TestExtrapolationWithinLimitAdvances(t *testing.T) {
	c := twoTickClock(t)
	c.SetExtrapolationLimit(100 * time.Millisecond)
	st, ok := c.Sample(115)
	if !ok {
		t.Fatalf("sample past the newest snapshot reported no data")
	}
	e, found := entityAt(st, 1)
	if !found {
		t.Fatalf("entity 1 missing from sampled state")
	}
	if !almost(e.X, 120) {
		t.Fatalf("x = %v five ticks past the last snapshot, want 120 from 10 units per tick", e.X)
	}
	if !st.Extrapolated {
		t.Fatalf("state past the newest snapshot must be marked extrapolated")
	}
	if st.Stale {
		t.Fatalf("extrapolation inside the limit must not be stale")
	}
}

func TestDelayShiftsPlaybackPosition(t *testing.T) {
	c := twoTickClock(t)
	c.SetDelay(1000 * time.Millisecond)
	if got := c.Delay(); got != 1000*time.Millisecond {
		t.Fatalf("delay = %v, want 1000ms", got)
	}
	st, ok := c.Sample(130)
	if !ok {
		t.Fatalf("sample at tick 130 with a 20 tick delay reported no data")
	}
	e, found := entityAt(st, 1)
	if !found {
		t.Fatalf("entity 1 missing from sampled state")
	}
	if !almost(e.X, 100) {
		t.Fatalf("x = %v after subtracting the delay, want 100 at the newest snapshot", e.X)
	}
	if st.Tick != 110 || st.Extrapolated {
		t.Fatalf("delayed sample got tick=%d extrapolated=%v, want tick 110 and no extrapolation", st.Tick, st.Extrapolated)
	}
}

func TestSampleOrdersEntitiesByID(t *testing.T) {
	c := newTestClock()
	if err := c.Push(impl.Snapshot{Tick: 100, Entities: []impl.EntityState{
		{EntityID: 3, X: 0},
		{EntityID: 1, X: 10},
	}}); err != nil {
		t.Fatalf("push tick 100: %v", err)
	}
	if err := c.Push(impl.Snapshot{Tick: 110, Entities: []impl.EntityState{
		{EntityID: 1, X: 20},
		{EntityID: 3, X: 10},
	}}); err != nil {
		t.Fatalf("push tick 110: %v", err)
	}
	st, ok := c.Sample(105)
	if !ok {
		t.Fatalf("sample at tick 105 reported no data")
	}
	if len(st.Entities) != 2 {
		t.Fatalf("sampled %d entities, want 2", len(st.Entities))
	}
	if st.Entities[0].EntityID != 1 || st.Entities[1].EntityID != 3 {
		t.Fatalf("entity order = [%d %d], want ascending [1 3]", st.Entities[0].EntityID, st.Entities[1].EntityID)
	}
	if !almost(st.Entities[0].X, 15) || !almost(st.Entities[1].X, 5) {
		t.Fatalf("interpolated x = [%v %v], want [15 5]", st.Entities[0].X, st.Entities[1].X)
	}
}

func TestSnapshotRestoreKeepsTimeline(t *testing.T) {
	c := twoTickClock(t)
	c.SetDelay(1000 * time.Millisecond)
	if err := c.Push(impl.Snapshot{Tick: 110}); !errors.Is(err, impl.ErrDuplicateTick) {
		t.Fatalf("duplicate push: got %v, want ErrDuplicateTick", err)
	}
	data, err := c.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if err := c.Push(impl.Snapshot{Tick: 200}); err != nil {
		t.Fatalf("push tick 200 after snapshot: %v", err)
	}
	if err := c.Restore(data); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := c.Queued(); got != 2 {
		t.Fatalf("queued after restore = %d, want 2", got)
	}
	if got := c.TickSpan(); got != 500*time.Millisecond {
		t.Fatalf("tick span after restore = %v, want 500ms", got)
	}
	if got := c.Delay(); got != 1000*time.Millisecond {
		t.Fatalf("delay after restore = %v, want 1000ms", got)
	}
	if got := c.Dropped(); got != 1 {
		t.Fatalf("dropped after restore = %d, want 1", got)
	}
	st, ok := c.Sample(130)
	if !ok {
		t.Fatalf("sample after restore reported no data")
	}
	e, found := entityAt(st, 1)
	if !found || !almost(e.X, 100) {
		t.Fatalf("x after restore = %v (found=%v), want 100", e.X, found)
	}
	if err := c.Restore([]byte("not a clock payload")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("restore of garbage: got %v, want ErrBadSnapshot", err)
	}
	if got := c.Queued(); got != 2 {
		t.Fatalf("queued after rejected restore = %d, want the previous 2", got)
	}
}
