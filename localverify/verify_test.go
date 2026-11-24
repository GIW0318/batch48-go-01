package localverify

import (
	"errors"
	"sync"
	"testing"
	"time"

	impl "gsb/batch48-go-game-a-11"
)

func newC() *impl.InterpolationClock { return impl.NewInterpolationClock(20, 0) }

func push(t *testing.T, c *impl.InterpolationClock, tick uint64, ids ...uint32) {
	t.Helper()
	es := make([]impl.EntityState, 0, len(ids))
	for _, id := range ids {
		es = append(es, impl.EntityState{EntityID: id, X: float64(id)})
	}
	if err := c.Push(impl.Snapshot{Tick: tick, Entities: es}); err != nil {
		t.Fatalf("push %d: %v", tick, err)
	}
}

func TestInvalidEntityRejected(t *testing.T) {
	c := newC()
	if err := c.Push(impl.Snapshot{Tick: 1, Entities: []impl.EntityState{{EntityID: 0}}}); !errors.Is(err, impl.ErrInvalidEntity) {
		t.Fatalf("zero id: %v", err)
	}
	if err := c.Push(impl.Snapshot{Tick: 1, Entities: []impl.EntityState{{EntityID: 1}, {EntityID: 1}}}); !errors.Is(err, impl.ErrInvalidEntity) {
		t.Fatalf("dup id: %v", err)
	}
	if c.Queued() != 0 {
		t.Fatalf("queued=%d want 0", c.Queued())
	}
}

func TestEmptySnapshotHeartbeat(t *testing.T) {
	c := newC()
	if err := c.Push(impl.Snapshot{Tick: 7}); err != nil {
		t.Fatalf("empty push: %v", err)
	}
	if c.Queued() != 1 || len(c.Ticks()) != 1 || c.Ticks()[0] != 7 {
		t.Fatalf("heartbeat not stored: q=%d ticks=%v", c.Queued(), c.Ticks())
	}
	if st, ok := c.Sample(7); !ok || len(st.Entities) != 0 || st.Tick != 7 {
		t.Fatalf("heartbeat sample ok=%v st=%+v", ok, st)
	}
}

func TestEmptyBufferZeros(t *testing.T) {
	c := newC()
	if c.Queued() != 0 || c.TickSpan() != 0 || c.Ticks() == nil || len(c.Ticks()) != 0 {
		t.Fatalf("empty buffer invariants broken")
	}
	if _, ok := c.Sample(100); ok {
		t.Fatalf("sample on empty buffer must be false")
	}
}

func TestHardCapNeverExceeded(t *testing.T) {
	c := newC()
	for tick := uint64(100); tick < 200; tick += 2 {
		if err := c.Push(impl.Snapshot{Tick: tick}); err != nil {
			t.Fatal(err)
		}
	}
	if c.Queued() > impl.SnapshotBufferCap {
		t.Fatalf("queued %d exceeds cap", c.Queued())
	}
	if c.Queued() != impl.SnapshotBufferCap {
		t.Fatalf("queued=%d want %d", c.Queued(), impl.SnapshotBufferCap)
	}
}

func TestUnorderedThenEvictDroppedCount(t *testing.T) {
	c := newC()
	// insert out of order including an older tick after buffer fills
	for _, tick := range []uint64{5, 3, 4, 1, 2} {
		push(t, c, tick, 1)
	}
	got := c.Ticks()
	want := []uint64{1, 2, 3, 4, 5}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ticks=%v want %v", got, want)
		}
	}
}

func TestMissingEntityNoLineAcrossGap(t *testing.T) {
	c := newC()
	push(t, c, 100, 1, 2)
	push(t, c, 110, 2) // entity 1 missing in middle snapshot
	push(t, c, 120, 1, 2)
	st, ok := c.Sample(105)
	if !ok {
		t.Fatal("no state")
	}
	for _, e := range st.Entities {
		if e.EntityID == 1 {
			t.Fatalf("entity 1 must not be interpolated across the gap where it is missing: %+v", e)
		}
	}
	// entity present on both sides of 110..120 interpolates normally
	st2, ok := c.Sample(115)
	if !ok {
		t.Fatal("no state 115")
	}
	var found1 bool
	for _, e := range st2.Entities {
		if e.EntityID == 1 {
			found1 = true
		}
	}
	if !found1 {
		t.Fatalf("entity 1 should reappear in 110..120 interval: %+v", st2.Entities)
	}
}

func TestDelayFloorIsBufferedSpan(t *testing.T) {
	c := newC()
	push(t, c, 100, 1)
	push(t, c, 110, 1) // span 500ms
	c.SetDelay(10 * time.Millisecond)
	if c.Delay() != 500*time.Millisecond {
		t.Fatalf("delay clamped to %v want 500ms", c.Delay())
	}
	c.SetDelay(900 * time.Millisecond)
	if c.Delay() != 900*time.Millisecond {
		t.Fatalf("larger delay must be accepted, got %v", c.Delay())
	}
}

func TestDelayTicksFloorConversion(t *testing.T) {
	c := newC()
	push(t, c, 1, 1)
	// 100ms at 20 ticks/s = 2 ticks exactly; 99ms floors to 1 tick
	c.SetDelay(500 * time.Millisecond) // clamped up to span; use fresh clock instead
	c2 := newC()
	push(t, c2, 100, 1)
	c2.SetDelay(99 * time.Millisecond)
	if d := c2.Delay(); d != 99*time.Millisecond {
		t.Fatalf("delay stored %v", d)
	}
	// sample where delay floors to 1 tick: client 101 -> play 100 exact
	st, ok := c2.Sample(101)
	if !ok || st.Tick != 100 || st.Extrapolated {
		t.Fatalf("floor conversion sample: %+v ok=%v", st, ok)
	}
}

func TestStaleAutoResets(t *testing.T) {
	c := newC()
	push(t, c, 100, 1)
	push(t, c, 110, 1)
	c.SetExtrapolationLimit(50 * time.Millisecond)
	if st, ok := c.Sample(125); !ok || !st.Stale || !c.Stale() {
		t.Fatalf("expected stale first")
	}
	// back inside timeline -> stale resets
	if st, ok := c.Sample(105); !ok || st.Stale || c.Stale() {
		t.Fatalf("stale must reset after normal sample, st=%+v ok=%v", st, ok)
	}
}

func TestNoExtrapolationByDefault(t *testing.T) {
	c := newC()
	push(t, c, 100, 1)
	push(t, c, 110, 1)
	st, ok := c.Sample(111)
	if !ok {
		t.Fatal("expected a state even past newest")
	}
	if !st.Extrapolated || !st.Stale {
		t.Fatalf("no limit configured => frozen+stale, got %+v", st)
	}
}

func TestSnapshotRestoreAtomicAndDeep(t *testing.T) {
	c := newC()
	push(t, c, 100, 1, 2)
	push(t, c, 110, 1)
	c.SetDelay(750 * time.Millisecond) // above the 500ms buffered span, kept as-is
	data, err := c.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	// mutate live clock after snapshot
	push(t, c, 120, 3)
	if err := c.Restore(data); err != nil {
		t.Fatal(err)
	}
	if c.Queued() != 2 {
		t.Fatalf("restored queued=%d want 2", c.Queued())
	}
	if c.Delay() != 750*time.Millisecond {
		t.Fatalf("restored delay=%v", c.Delay())
	}
	// bad payload leaves state untouched
	before := c.Queued()
	if err := c.Restore([]byte("{bad json")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("garbage restore err=%v", err)
	}
	if err := c.Restore([]byte(`{"magic":"nope"}`)); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("wrong magic err=%v", err)
	}
	if c.Queued() != before {
		t.Fatalf("failed restore mutated state: queued %d->%d", before, c.Queued())
	}
	// restored timeline still plays correctly (no wall-clock fast forward);
	// 750ms delay at 20 ticks/s = 15 ticks, so client tick 120 -> play tick 105
	st, ok := c.Sample(120)
	if !ok || st.Tick != 110 || st.Extrapolated {
		t.Fatalf("post-restore sample %+v ok=%v", st, ok)
	}
}

func TestConcurrentPushSample(t *testing.T) {
	c := newC()
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			tick := uint64(1 + g*1000)
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = c.Push(impl.Snapshot{Tick: tick, Entities: []impl.EntityState{{EntityID: 1, X: float64(tick)}}})
				tick++
				if tick > uint64(1+g*1000)+500 {
					tick = uint64(1 + g*1000)
				}
			}
		}(g)
	}
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for tick := uint64(1); tick < 3000; tick++ {
				st, ok := c.Sample(tick)
				if ok {
					for i := 1; i < len(st.Entities); i++ {
						if st.Entities[i-1].EntityID > st.Entities[i].EntityID {
							panic("entities not sorted")
						}
					}
				}
				_ = c.Queued()
				_ = c.Dropped()
				_ = c.Ticks()
			}
		}()
	}
	time.Sleep(120 * time.Millisecond)
	close(stop)
	wg.Wait()
	if c.Queued() > impl.SnapshotBufferCap {
		t.Fatalf("cap violated under concurrency: %d", c.Queued())
	}
}
