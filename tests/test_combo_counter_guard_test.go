package task

import (
	"errors"
	"sync"
	"testing"

	impl "gsb/batch48-go-game-a-25"
)

func newGuard() *impl.ComboCounterGuard {
	return impl.NewComboCounterGuard(impl.Options{
		WindowMs:     500,
		MaxCombo:     5,
		BonusStepPct: 10,
		MaxBonusPct:  25,
	})
}

func TestHitRejectsEmptyPlayer(t *testing.T) {
	g := newGuard()
	if _, err := g.Hit("", "boss", 0); !errors.Is(err, impl.ErrInvalidRequest) {
		t.Fatalf("empty player error = %v, want ErrInvalidRequest", err)
	}
}

func TestHitRejectsEmptyTarget(t *testing.T) {
	g := newGuard()
	if _, err := g.Hit("p", "", 0); !errors.Is(err, impl.ErrInvalidRequest) {
		t.Fatalf("empty target error = %v, want ErrInvalidRequest", err)
	}
}

func TestHitIncrementsComboCount(t *testing.T) {
	g := newGuard()
	for i, at := range []int64{100, 200, 300} {
		res, err := g.Hit("p", "boss", at)
		if err != nil {
			t.Fatalf("hit %d: %v", i, err)
		}
		if res.Count != i+1 {
			t.Fatalf("combo count after hit %d = %d, want %d", i, res.Count, i+1)
		}
		if res.Target != "boss" {
			t.Fatalf("combo target after hit %d = %q, want boss", i, res.Target)
		}
	}
	if got := g.Count("p", 300); got != 3 {
		t.Fatalf("stored combo count = %d, want 3", got)
	}
}

func TestHitCapsAtMaxCombo(t *testing.T) {
	g := newGuard()
	var last impl.Combo
	for i := 0; i < 7; i++ {
		res, err := g.Hit("p", "boss", int64(100+i*100))
		if err != nil {
			t.Fatalf("hit %d: %v", i, err)
		}
		last = res
	}
	if last.Count != 5 {
		t.Fatalf("combo count after seven hits = %d, want the cap 5", last.Count)
	}
}

func TestBonusGrowsWithCountAndCaps(t *testing.T) {
	g := newGuard()
	for i, at := range []int64{100, 200, 300} {
		if _, err := g.Hit("p", "boss", at); err != nil {
			t.Fatalf("hit %d: %v", i, err)
		}
		want := 10 * (i + 1)
		if want > 25 {
			want = 25
		}
		if got := g.Bonus("p", at); got != want {
			t.Fatalf("bonus at combo %d = %d, want %d", i+1, got, want)
		}
	}
}

func TestBonusZeroAfterWindow(t *testing.T) {
	g := newGuard()
	if _, err := g.Hit("p", "boss", 0); err != nil {
		t.Fatalf("hit: %v", err)
	}
	if got := g.Bonus("p", 400); got != 10 {
		t.Fatalf("bonus inside the window = %d, want 10", got)
	}
	if got := g.Bonus("p", 501); got != 0 {
		t.Fatalf("bonus after the window = %d, want 0", got)
	}
}

func TestHitAfterExpiryRestartsCount(t *testing.T) {
	g := newGuard()
	if _, err := g.Hit("p", "boss", 0); err != nil {
		t.Fatalf("first hit: %v", err)
	}
	res, err := g.Hit("p", "boss", 1000)
	if err != nil {
		t.Fatalf("second hit: %v", err)
	}
	if res.Count != 1 {
		t.Fatalf("count after a hit past the window = %d, want 1", res.Count)
	}
	if got := g.Count("p", 1000); got != 1 {
		t.Fatalf("stored count after a hit past the window = %d, want 1", got)
	}
}

func TestBreakResetsCount(t *testing.T) {
	g := newGuard()
	for i, at := range []int64{100, 200, 300} {
		if _, err := g.Hit("p", "boss", at); err != nil {
			t.Fatalf("hit %d: %v", i, err)
		}
	}
	if err := g.Break("p", "damage", 400); err != nil {
		t.Fatalf("break: %v", err)
	}
	if got := g.Count("p", 400); got != 0 {
		t.Fatalf("count after break = %d, want 0", got)
	}
	if got := g.Bonus("p", 400); got != 0 {
		t.Fatalf("bonus after break = %d, want 0", got)
	}
}

func TestBreakRejectsUnknownReason(t *testing.T) {
	g := newGuard()
	if _, err := g.Hit("p", "boss", 100); err != nil {
		t.Fatalf("hit: %v", err)
	}
	if err := g.Break("p", "teleport", 100); !errors.Is(err, impl.ErrInvalidReason) {
		t.Fatalf("unknown break reason error = %v, want ErrInvalidReason", err)
	}
	if got := g.Count("p", 100); got != 1 {
		t.Fatalf("count after a rejected break = %d, want 1", got)
	}
}

func TestExtendPushesWindow(t *testing.T) {
	g := newGuard()
	if _, err := g.Hit("p", "boss", 0); err != nil {
		t.Fatalf("hit: %v", err)
	}
	if err := g.Extend("p", 200); err != nil {
		t.Fatalf("extend: %v", err)
	}
	if got := g.Bonus("p", 600); got != 10 {
		t.Fatalf("bonus inside the extended window = %d, want 10", got)
	}
	if got := g.Bonus("p", 800); got != 0 {
		t.Fatalf("bonus after the extended window = %d, want 0", got)
	}
}

func TestExtendRejectsNonPositive(t *testing.T) {
	g := newGuard()
	if _, err := g.Hit("p", "boss", 0); err != nil {
		t.Fatalf("hit: %v", err)
	}
	if err := g.Extend("p", 0); !errors.Is(err, impl.ErrInvalidExtend) {
		t.Fatalf("zero extend error = %v, want ErrInvalidExtend", err)
	}
	if err := g.Extend("p", -5); !errors.Is(err, impl.ErrInvalidExtend) {
		t.Fatalf("negative extend error = %v, want ErrInvalidExtend", err)
	}
}

func TestTransferRejectsUnknownPlayer(t *testing.T) {
	g := newGuard()
	if err := g.Transfer("ghost", "boss", "add"); !errors.Is(err, impl.ErrUnknownPlayer) {
		t.Fatalf("unknown player transfer error = %v, want ErrUnknownPlayer", err)
	}
}

func TestCountAndExpiryReport(t *testing.T) {
	g := newGuard()
	if _, err := g.Hit("p", "boss", 100); err != nil {
		t.Fatalf("hit: %v", err)
	}
	if got := g.Count("p", 200); got != 1 {
		t.Fatalf("count inside the window = %d, want 1", got)
	}
	if got := g.ExpiresAt("p", 200); got != 600 {
		t.Fatalf("expiry of a hit at 100ms with a 500ms window = %d, want 600", got)
	}
	if got := g.Count("p", 600); got != 1 {
		t.Fatalf("count at the expiry instant = %d, want 1", got)
	}
	if got := g.Count("p", 601); got != 0 {
		t.Fatalf("count after the expiry instant = %d, want 0", got)
	}
}

func TestSnapshotRestorePreservesCombo(t *testing.T) {
	g := newGuard()
	if _, err := g.Hit("p", "boss", 100); err != nil {
		t.Fatalf("first hit: %v", err)
	}
	if _, err := g.Hit("p", "boss", 200); err != nil {
		t.Fatalf("second hit: %v", err)
	}
	payload, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	other := newGuard()
	if err := other.Restore(payload); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := other.Count("p", 200); got != 2 {
		t.Fatalf("restored count = %d, want 2", got)
	}
	if got := other.Bonus("p", 200); got != 20 {
		t.Fatalf("restored bonus = %d, want 20", got)
	}
	res, err := other.Hit("p", "boss", 300)
	if err != nil {
		t.Fatalf("hit after restore: %v", err)
	}
	if res.Count != 3 || res.Target != "boss" {
		t.Fatalf("combo after restore = %+v, want count 3 on boss", res)
	}
}

func TestConcurrentReadersSeeStableCombo(t *testing.T) {
	g := impl.NewComboCounterGuard(impl.Options{
		WindowMs:     1000000,
		MaxCombo:     100,
		BonusStepPct: 10,
		MaxBonusPct:  25,
	})
	var wg sync.WaitGroup
	var mu sync.Mutex
	hitErrors := 0
	stop := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = g.Count("p", 1000)
				_ = g.Bonus("p", 1000)
				_ = g.ExpiresAt("p", 1000)
			}
		}()
	}
	for i := 0; i < 50; i++ {
		if _, err := g.Hit("p", "boss", int64(1000+i*10)); err != nil {
			mu.Lock()
			hitErrors++
			mu.Unlock()
		}
	}
	close(stop)
	wg.Wait()
	if hitErrors != 0 {
		t.Fatalf("hits rejected while readers were running = %d, want 0", hitErrors)
	}
	if got := g.Count("p", 1500); got != 50 {
		t.Fatalf("combo count after concurrent reads = %d, want 50", got)
	}
}
