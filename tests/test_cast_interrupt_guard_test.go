package task

import (
	"errors"
	"sync"
	"testing"

	impl "gsb/batch48-go-game-a-22"
)

func newGuard() *impl.CastInterruptGuard {
	return impl.NewCastInterruptGuard(impl.Options{MaxCastMs: 5000, MaxTickCount: 8})
}

func TestBeginRejectsEmptySpell(t *testing.T) {
	g := newGuard()
	if _, err := g.Begin(impl.CastRequest{Player: "mage", Spell: "", CastMs: 1000}, 0); !errors.Is(err, impl.ErrInvalidRequest) {
		t.Fatalf("empty spell error = %v, want ErrInvalidRequest", err)
	}
}

func TestBeginRejectsNegativeCastTime(t *testing.T) {
	g := newGuard()
	if _, err := g.Begin(impl.CastRequest{Player: "mage", Spell: "fireball", CastMs: -1}, 0); !errors.Is(err, impl.ErrInvalidRequest) {
		t.Fatalf("negative cast time error = %v, want ErrInvalidRequest", err)
	}
}

func TestBeginRejectsChannelWithoutTicks(t *testing.T) {
	g := newGuard()
	req := impl.CastRequest{Player: "mage", Spell: "beam", CastMs: 1000, Channel: true, Ticks: 0, TickMs: 100}
	if _, err := g.Begin(req, 0); !errors.Is(err, impl.ErrInvalidRequest) {
		t.Fatalf("channel without ticks error = %v, want ErrInvalidRequest", err)
	}
}

func TestFinishBeforeEndIsRejected(t *testing.T) {
	g := newGuard()
	if _, err := g.Begin(impl.CastRequest{Player: "mage", Spell: "fireball", CastMs: 1000}, 0); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := g.Finish(999); !errors.Is(err, impl.ErrCastIncomplete) {
		t.Fatalf("finish before the cast ends error = %v, want ErrCastIncomplete", err)
	}
	if _, ok := g.Active(); !ok {
		t.Fatalf("cast disappeared after a rejected finish")
	}
}

func TestFinishAtEndCompletesCast(t *testing.T) {
	g := newGuard()
	if _, err := g.Begin(impl.CastRequest{Player: "mage", Spell: "fireball", CastMs: 1000}, 0); err != nil {
		t.Fatalf("begin: %v", err)
	}
	res, err := g.Finish(1000)
	if err != nil {
		t.Fatalf("finish: %v", err)
	}
	if !res.Completed || res.EndedAt != 1000 || res.Ticks != 0 || res.Spell != "fireball" {
		t.Fatalf("finish result = %+v, want a completed 1000ms cast with no ticks", res)
	}
}

func TestInstantCastCompletesAtStart(t *testing.T) {
	g := newGuard()
	cast, err := g.Begin(impl.CastRequest{Player: "mage", Spell: "blink", CastMs: 0}, 300)
	if err != nil {
		t.Fatalf("begin instant cast: %v", err)
	}
	if cast.EndsAt != 300 {
		t.Fatalf("instant cast ends at %d, want 300", cast.EndsAt)
	}
	if _, ok := g.Active(); ok {
		t.Fatalf("instant cast left a running cast behind")
	}
	res, err := g.Finish(500)
	if err != nil {
		t.Fatalf("finish instant cast: %v", err)
	}
	if !res.Completed || res.EndedAt != 300 {
		t.Fatalf("instant result = %+v, want completed at 300", res)
	}
}

func TestAdvanceEmitsChannelTicks(t *testing.T) {
	g := newGuard()
	req := impl.CastRequest{Player: "mage", Spell: "beam", CastMs: 3000, Channel: true, Ticks: 3, TickMs: 1000}
	if _, err := g.Begin(req, 0); err != nil {
		t.Fatalf("begin channel: %v", err)
	}
	ticks, err := g.Advance(2500)
	if err != nil {
		t.Fatalf("advance: %v", err)
	}
	if len(ticks) != 2 {
		t.Fatalf("ticks due at 2500ms = %d, want 2", len(ticks))
	}
	if ticks[0].Index != 0 || ticks[0].At != 1000 {
		t.Fatalf("first tick = %+v, want index 0 at 1000", ticks[0])
	}
	if ticks[1].Index != 1 || ticks[1].At != 2000 {
		t.Fatalf("second tick = %+v, want index 1 at 2000", ticks[1])
	}
}

func TestAdvanceTwiceAtSameTimeEmitsNothingNew(t *testing.T) {
	g := newGuard()
	req := impl.CastRequest{Player: "mage", Spell: "beam", CastMs: 2000, Channel: true, Ticks: 4, TickMs: 500}
	if _, err := g.Begin(req, 0); err != nil {
		t.Fatalf("begin channel: %v", err)
	}
	first, err := g.Advance(1000)
	if err != nil {
		t.Fatalf("first advance: %v", err)
	}
	if len(first) != 2 {
		t.Fatalf("first advance ticks = %d, want 2", len(first))
	}
	second, err := g.Advance(1000)
	if err != nil {
		t.Fatalf("second advance: %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("repeated advance at the same millisecond emitted %d ticks, want 0", len(second))
	}
}

func TestFinishReportsChannelTickCount(t *testing.T) {
	g := newGuard()
	req := impl.CastRequest{Player: "mage", Spell: "beam", CastMs: 3000, Channel: true, Ticks: 3, TickMs: 1000}
	if _, err := g.Begin(req, 0); err != nil {
		t.Fatalf("begin channel: %v", err)
	}
	ticks, err := g.Advance(3000)
	if err != nil {
		t.Fatalf("advance: %v", err)
	}
	if len(ticks) != 3 {
		t.Fatalf("ticks due at 3000ms = %d, want 3", len(ticks))
	}
	res, err := g.Finish(3000)
	if err != nil {
		t.Fatalf("finish channel: %v", err)
	}
	if !res.Completed || res.Ticks != 3 {
		t.Fatalf("channel result = %+v, want completed with 3 ticks", res)
	}
}

func TestInterruptWithoutCastIsRejected(t *testing.T) {
	g := newGuard()
	if err := g.Interrupt("stun", 100); !errors.Is(err, impl.ErrNoActiveCast) {
		t.Fatalf("interrupt without cast error = %v, want ErrNoActiveCast", err)
	}
}

func TestInterruptCancelsActiveCast(t *testing.T) {
	g := newGuard()
	if _, err := g.Begin(impl.CastRequest{Player: "mage", Spell: "fireball", CastMs: 2000}, 0); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := g.Interrupt("stun", 500); err != nil {
		t.Fatalf("interrupt: %v", err)
	}
	if _, ok := g.Active(); ok {
		t.Fatalf("interrupted cast is still active")
	}
	res, ok := g.LastResult()
	if !ok {
		t.Fatalf("no last result after interrupt")
	}
	if res.Completed || res.Reason != "stun" || res.EndedAt != 500 {
		t.Fatalf("interrupted result = %+v, want incomplete with reason stun at 500", res)
	}
}

func TestSilenceBlocksBegin(t *testing.T) {
	g := newGuard()
	if err := g.Silence(5000); err != nil {
		t.Fatalf("silence: %v", err)
	}
	if got := g.SilenceRemaining(2000); got != 3000 {
		t.Fatalf("silence remaining at 2000ms = %d, want 3000", got)
	}
	req := impl.CastRequest{Player: "mage", Spell: "fireball", CastMs: 500}
	if _, err := g.Begin(req, 1000); !errors.Is(err, impl.ErrSilenced) {
		t.Fatalf("begin while silenced error = %v, want ErrSilenced", err)
	}
	if _, err := g.Begin(req, 5000); err != nil {
		t.Fatalf("begin at the silence boundary: %v", err)
	}
}

func TestPushBackExtendsCompletion(t *testing.T) {
	g := newGuard()
	if _, err := g.Begin(impl.CastRequest{Player: "mage", Spell: "pyroblast", CastMs: 1000}, 0); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := g.PushBack(500, 200); err != nil {
		t.Fatalf("push back: %v", err)
	}
	if _, err := g.Finish(1200); !errors.Is(err, impl.ErrCastIncomplete) {
		t.Fatalf("finish at the original end error = %v, want ErrCastIncomplete", err)
	}
	res, err := g.Finish(1500)
	if err != nil {
		t.Fatalf("finish after push back: %v", err)
	}
	if !res.Completed || res.EndedAt != 1500 {
		t.Fatalf("pushed back result = %+v, want completed at 1500", res)
	}
}

func TestSnapshotRestorePreservesActiveCast(t *testing.T) {
	g := newGuard()
	req := impl.CastRequest{Player: "mage", Spell: "beam", CastMs: 2000, Channel: true, Ticks: 4, TickMs: 500}
	if _, err := g.Begin(req, 0); err != nil {
		t.Fatalf("begin channel: %v", err)
	}
	if ticks, err := g.Advance(1000); err != nil || len(ticks) != 2 {
		t.Fatalf("advance before snapshot: ticks=%d err=%v", len(ticks), err)
	}
	payload, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	other := newGuard()
	if err := other.Restore(payload); err != nil {
		t.Fatalf("restore: %v", err)
	}
	ticks, err := other.Advance(2500)
	if err != nil {
		t.Fatalf("advance after restore: %v", err)
	}
	if len(ticks) != 2 {
		t.Fatalf("ticks after restore = %d, want the remaining 2", len(ticks))
	}
	if ticks[0].Index != 2 || ticks[0].At != 1500 {
		t.Fatalf("first restored tick = %+v, want index 2 at 1500", ticks[0])
	}
	res, err := other.Finish(2000)
	if err != nil {
		t.Fatalf("finish after restore: %v", err)
	}
	if res.Ticks != 4 {
		t.Fatalf("restored tick count = %d, want 4", res.Ticks)
	}
}

func TestConcurrentAdvanceEmitsEachTickOnce(t *testing.T) {
	g := newGuard()
	req := impl.CastRequest{Player: "mage", Spell: "beam", CastMs: 1000, Channel: true, Ticks: 10, TickMs: 100}
	if _, err := g.Begin(req, 0); err != nil {
		t.Fatalf("begin channel: %v", err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := make(map[int]int)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ticks, err := g.Advance(1000)
			if err != nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, tick := range ticks {
				seen[tick.Index]++
			}
		}()
	}
	wg.Wait()
	if len(seen) != 10 {
		t.Fatalf("distinct ticks emitted = %d, want 10", len(seen))
	}
	for index, count := range seen {
		if count != 1 {
			t.Fatalf("tick %d emitted %d times, want exactly 1", index, count)
		}
	}
}
