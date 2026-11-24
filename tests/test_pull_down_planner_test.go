package task

import (
	"errors"
	"testing"

	impl "gsb/batch48-go-game-c-13"
)

func mustPlanner(t *testing.T, src, dst impl.Rational) *impl.PullDownPlanner {
	t.Helper()
	p, err := impl.NewPullDownPlanner(src, dst)
	if err != nil {
		t.Fatalf("build planner %d/%d -> %d/%d: %v", src.Num, src.Den, dst.Num, dst.Den, err)
	}
	return p
}

func pushRange(t *testing.T, p *impl.PullDownPlanner, last int64) {
	t.Helper()
	for i := int64(0); i <= last; i++ {
		if err := p.PushFrame(i); err != nil {
			t.Fatalf("push frame %d: %v", i, err)
		}
	}
}

func TestRejectsNonPositiveRate(t *testing.T) {
	if _, err := impl.NewPullDownPlanner(impl.Rational{Num: 0, Den: 1}, impl.Rational{Num: 25, Den: 1}); !errors.Is(err, impl.ErrRateRejected) {
		t.Fatalf("zero source rate returned %v, want ErrRateRejected", err)
	}
	if _, err := impl.NewPullDownPlanner(impl.Rational{Num: 25, Den: 1}, impl.Rational{Num: 25, Den: 0}); !errors.Is(err, impl.ErrRateRejected) {
		t.Fatalf("zero target denominator returned %v, want ErrRateRejected", err)
	}
}

func TestRejectsOverflowingRateProduct(t *testing.T) {
	src := impl.Rational{Num: 1 << 40, Den: 1}
	dst := impl.Rational{Num: 1, Den: 1 << 40}
	if _, err := impl.NewPullDownPlanner(src, dst); !errors.Is(err, impl.ErrRateRejected) {
		t.Fatalf("overflowing time base product returned %v, want ErrRateRejected", err)
	}
}

func TestExpectedOutputsUsesRationalMath(t *testing.T) {
	p := mustPlanner(t, impl.Rational{Num: 30000, Den: 1001}, impl.Rational{Num: 25, Den: 1})
	if got := p.ExpectedOutputs(30000); got != 25025 {
		t.Fatalf("expected outputs for 30000 source frames = %d, want 25025", got)
	}
}

func TestSameRateMapsOneToOne(t *testing.T) {
	p := mustPlanner(t, impl.Rational{Num: 25, Den: 1}, impl.Rational{Num: 25, Den: 1})
	pushRange(t, p, 9)
	for i := int64(0); i < 10; i++ {
		dec, err := p.Next()
		if err != nil {
			t.Fatalf("next output %d: %v", i, err)
		}
		if dec.Output != i || dec.Source != i {
			t.Fatalf("output %d used source %d, want output %d source %d", dec.Output, dec.Source, i, i)
		}
		if dec.Repeat {
			t.Fatalf("output %d marked as a repeat under identical rates", i)
		}
	}
	stats := p.Stats()
	if stats.Repeats != 0 || stats.Drops != 0 {
		t.Fatalf("repeat/drop counters = %d/%d, want 0/0", stats.Repeats, stats.Drops)
	}
}

func TestRepeatForFasterTarget(t *testing.T) {
	p := mustPlanner(t, impl.Rational{Num: 24, Den: 1}, impl.Rational{Num: 48, Den: 1})
	pushRange(t, p, 2)
	wantSource := []int64{0, 0, 1, 1}
	wantRepeat := []bool{false, true, false, true}
	for i := range wantSource {
		dec, err := p.Next()
		if err != nil {
			t.Fatalf("next output %d: %v", i, err)
		}
		if dec.Source != wantSource[i] || dec.Repeat != wantRepeat[i] {
			t.Fatalf("output %d = source %d repeat %v, want source %d repeat %v", i, dec.Source, dec.Repeat, wantSource[i], wantRepeat[i])
		}
	}
	if got := p.Stats().Repeats; got != 2 {
		t.Fatalf("repeat counter = %d, want 2", got)
	}
}

func TestDropForSlowerTarget(t *testing.T) {
	p := mustPlanner(t, impl.Rational{Num: 60, Den: 1}, impl.Rational{Num: 30, Den: 1})
	pushRange(t, p, 7)
	want := []int64{0, 2, 4, 6}
	for i, wantSource := range want {
		dec, err := p.Next()
		if err != nil {
			t.Fatalf("next output %d: %v", i, err)
		}
		if dec.Source != wantSource {
			t.Fatalf("output %d used source %d, want %d", i, dec.Source, wantSource)
		}
	}
	if got := p.Stats().Drops; got != 3 {
		t.Fatalf("drop counter = %d, want 3", got)
	}
}

func TestRepeatOverflowCounted(t *testing.T) {
	p := mustPlanner(t, impl.Rational{Num: 24, Den: 1}, impl.Rational{Num: 96, Den: 1})
	if err := p.SetPolicy(impl.PlayoutPolicy{MaxRepeat: 1, MaxDrop: 8}); err != nil {
		t.Fatalf("set policy: %v", err)
	}
	pushRange(t, p, 1)
	for i := 0; i < 4; i++ {
		if _, err := p.Next(); err != nil {
			t.Fatalf("next output %d: %v", i, err)
		}
	}
	if got := p.Stats().RepeatOver; got != 2 {
		t.Fatalf("repeat overflow counter = %d, want 2", got)
	}
}

func TestDropOverflowCounted(t *testing.T) {
	p := mustPlanner(t, impl.Rational{Num: 60, Den: 1}, impl.Rational{Num: 10, Den: 1})
	if err := p.SetPolicy(impl.PlayoutPolicy{MaxRepeat: 8, MaxDrop: 1}); err != nil {
		t.Fatalf("set policy: %v", err)
	}
	pushRange(t, p, 20)
	for i := 0; i < 4; i++ {
		if _, err := p.Next(); err != nil {
			t.Fatalf("next output %d: %v", i, err)
		}
	}
	if got := p.Stats().DropOver; got != 3 {
		t.Fatalf("drop overflow counter = %d, want 3", got)
	}
}

func TestNextRejectsSourceUnderrun(t *testing.T) {
	p := mustPlanner(t, impl.Rational{Num: 25, Den: 1}, impl.Rational{Num: 25, Den: 1})
	if err := p.PushFrame(0); err != nil {
		t.Fatalf("push frame 0: %v", err)
	}
	if _, err := p.Next(); err != nil {
		t.Fatalf("first output: %v", err)
	}
	if _, err := p.Next(); !errors.Is(err, impl.ErrSourceUnderrun) {
		t.Fatalf("second output returned %v, want ErrSourceUnderrun", err)
	}
}

func TestPushNegativeFrameRejected(t *testing.T) {
	p := mustPlanner(t, impl.Rational{Num: 25, Den: 1}, impl.Rational{Num: 25, Den: 1})
	if err := p.PushFrame(-3); !errors.Is(err, impl.ErrFrameOrder) {
		t.Fatalf("negative frame index returned %v, want ErrFrameOrder", err)
	}
	if got := p.FramesAvailable(); got != 0 {
		t.Fatalf("available frames = %d after rejected push, want 0", got)
	}
}

func TestSetTargetRateResetsPhase(t *testing.T) {
	p := mustPlanner(t, impl.Rational{Num: 25, Den: 1}, impl.Rational{Num: 25, Den: 1})
	pushRange(t, p, 9)
	for i := 0; i < 5; i++ {
		if _, err := p.Next(); err != nil {
			t.Fatalf("next output %d: %v", i, err)
		}
	}
	if got := p.Phase(); got != 5 {
		t.Fatalf("phase before rate change = %d, want 5", got)
	}
	if err := p.SetTargetRate(impl.Rational{Num: 24, Den: 1}); err != nil {
		t.Fatalf("set target rate: %v", err)
	}
	if got := p.Phase(); got != 0 {
		t.Fatalf("phase after rate change = %d, want 0", got)
	}
	dec, err := p.Next()
	if err != nil {
		t.Fatalf("first output after rate change: %v", err)
	}
	if dec.Output != 0 || dec.Source != 0 {
		t.Fatalf("first output after rate change = output %d source %d, want 0/0", dec.Output, dec.Source)
	}
	if got := p.Stats().Resets; got != 1 {
		t.Fatalf("reset counter = %d, want 1", got)
	}
}

func TestAccumulatedErrorResidual(t *testing.T) {
	p := mustPlanner(t, impl.Rational{Num: 30000, Den: 1001}, impl.Rational{Num: 25, Den: 1})
	pushRange(t, p, 4)
	for i := 0; i < 4; i++ {
		if _, err := p.Next(); err != nil {
			t.Fatalf("next output %d: %v", i, err)
		}
	}
	if got := p.AccumulatedError(); got != 796 {
		t.Fatalf("accumulated error after four outputs = %d, want 796", got)
	}
}

func TestPolicyRejectsNegativeLimits(t *testing.T) {
	p := mustPlanner(t, impl.Rational{Num: 25, Den: 1}, impl.Rational{Num: 25, Den: 1})
	if err := p.SetPolicy(impl.PlayoutPolicy{MaxRepeat: -1, MaxDrop: 2}); !errors.Is(err, impl.ErrPolicyRejected) {
		t.Fatalf("negative repeat limit returned %v, want ErrPolicyRejected", err)
	}
	if err := p.SetPolicy(impl.PlayoutPolicy{MaxRepeat: 2, MaxDrop: -1}); !errors.Is(err, impl.ErrPolicyRejected) {
		t.Fatalf("negative drop limit returned %v, want ErrPolicyRejected", err)
	}
}

func TestSnapshotRestoreKeepsPhase(t *testing.T) {
	src := impl.Rational{Num: 25, Den: 1}
	dst := impl.Rational{Num: 25, Den: 1}
	p := mustPlanner(t, src, dst)
	pushRange(t, p, 9)
	for i := 0; i < 5; i++ {
		if _, err := p.Next(); err != nil {
			t.Fatalf("next output %d: %v", i, err)
		}
	}
	snap, err := p.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	restored := mustPlanner(t, src, dst)
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := restored.Phase(); got != 5 {
		t.Fatalf("phase after restore = %d, want 5", got)
	}
	dec, err := restored.Next()
	if err != nil {
		t.Fatalf("next output after restore: %v", err)
	}
	if dec.Output != 5 || dec.Source != 5 {
		t.Fatalf("output after restore = output %d source %d, want 5/5", dec.Output, dec.Source)
	}
	if got := restored.Stats().Outputs; got != 5 {
		t.Fatalf("output counter after restore = %d, want 5 preserved", got)
	}
}

func TestRestoreRejectsCorruptSnapshot(t *testing.T) {
	p := mustPlanner(t, impl.Rational{Num: 25, Den: 1}, impl.Rational{Num: 30, Den: 1})
	if err := p.Restore([]byte("not a planner snapshot")); !errors.Is(err, impl.ErrPlannerSnapshot) {
		t.Fatalf("corrupt restore returned %v, want ErrPlannerSnapshot", err)
	}
	if got := p.Phase(); got != 0 {
		t.Fatalf("phase after corrupt restore = %d, want 0 untouched", got)
	}
}
