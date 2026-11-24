package task

import (
	"errors"
	"sync"
	"testing"

	impl "gsb/batch48-go-safety-b-06"
)

func newExtraGuard(t *testing.T) *impl.RlimitProfileGuard {
	t.Helper()
	return impl.NewRlimitProfileGuard(impl.Options{MaxProfiles: 16, MaxStack: 8})
}

// A profile that only carries a soft ceiling must never drag the hard ceiling
// down to the new soft value.
func TestExtraSoftOnlyProfileKeepsHard(t *testing.T) {
	g := newExtraGuard(t)
	if err := g.Define("base", impl.Limits{
		Soft: map[int]uint64{impl.ResourceNoFile: 1024},
		Hard: map[int]uint64{impl.ResourceNoFile: 4096},
	}); err != nil {
		t.Fatalf("define base: %v", err)
	}
	if err := g.Define("soft-only", impl.Limits{
		Soft: map[int]uint64{impl.ResourceNoFile: 256},
	}); err != nil {
		t.Fatalf("define soft-only: %v", err)
	}
	if _, err := g.Apply(101, []string{"base", "soft-only"}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	lim, err := g.Effective(101)
	if err != nil {
		t.Fatalf("effective: %v", err)
	}
	if got := lim.Soft[impl.ResourceNoFile]; got != 256 {
		t.Fatalf("soft = %d, want 256", got)
	}
	if got := lim.Hard[impl.ResourceNoFile]; got != 4096 {
		t.Fatalf("hard = %d, want 4096 preserved by soft-only profile", got)
	}
}

// Once a hard ceiling was lowered, another profile asking for a higher hard
// ceiling must be rejected and the original state must survive untouched.
func TestExtraLoweredHardCannotBeRaised(t *testing.T) {
	g := newExtraGuard(t)
	defineNoFile(t, g, "wide", 1024, 8192)
	defineNoFile(t, g, "tight", 512, 2048)
	defineNoFile(t, g, "wide-again", 1024, 8192)

	if _, err := g.Apply(102, []string{"wide", "tight"}); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if _, err := g.Apply(102, []string{"wide-again"}); !errors.Is(err, impl.ErrHardRaise) {
		t.Fatalf("raising a lowered hard must fail with ErrHardRaise, got %v", err)
	}
	lim, err := g.Effective(102)
	if err != nil {
		t.Fatalf("effective after rejected apply: %v", err)
	}
	if got := lim.Hard[impl.ResourceNoFile]; got != 2048 {
		t.Fatalf("hard = %d, want 2048 unchanged", got)
	}
	if got := lim.Soft[impl.ResourceNoFile]; got != 512 {
		t.Fatalf("soft = %d, want 512 unchanged", got)
	}
}

// Lowering NOFILE never reclaims descriptors that are already open; the
// tracked usage stays visible separately from the enforced ceiling.
func TestExtraNOFILEKeepsOpenFDsSeparate(t *testing.T) {
	g := newExtraGuard(t)
	defineNoFile(t, g, "io", 256, 1024)
	if _, err := g.Apply(103, []string{"io"}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := g.TrackOpenFDs(103, 800); err != nil {
		t.Fatalf("track open fds: %v", err)
	}
	lim, err := g.Effective(103)
	if err != nil {
		t.Fatalf("effective: %v", err)
	}
	if got := lim.Soft[impl.ResourceNoFile]; got != 256 {
		t.Fatalf("soft = %d, want 256", got)
	}
	if lim.OpenFDs != 800 {
		t.Fatalf("open fds = %d, want 800 retained below the new soft ceiling", lim.OpenFDs)
	}
	if err := g.TrackOpenFDs(103, 900); err != nil {
		t.Fatalf("update open fds: %v", err)
	}
	lim, err = g.Effective(103)
	if err != nil {
		t.Fatalf("effective: %v", err)
	}
	if lim.OpenFDs != 900 {
		t.Fatalf("open fds = %d, want 900", lim.OpenFDs)
	}
	if got := lim.Hard[impl.ResourceNoFile]; got != 1024 {
		t.Fatalf("hard = %d, want 1024", got)
	}
}

// Apply and Reset racing on one pid must never leave the guard in a partial or
// duplicated state; after each round exactly one outcome is observable.
func TestExtraApplyAndResetInterleave(t *testing.T) {
	g := newExtraGuard(t)
	defineNoFile(t, g, "io", 1024, 4096)

	const rounds = 200
	for i := 0; i < rounds; i++ {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = g.Apply(104, []string{"io"})
		}()
		go func() {
			defer wg.Done()
			_ = g.Reset(104)
		}()
		wg.Wait()
		if _, err := g.Effective(104); err != nil {
			if !errors.Is(err, impl.ErrUnknownPID) {
				t.Fatalf("effective err = %v, want limits or ErrUnknownPID", err)
			}
		}
	}
}

// A rejected Apply must be atomic: none of its resources may land before the
// hard-raise error is returned, so the previous ceilings stay intact.
func TestExtraFailedApplyRollsBackWholeState(t *testing.T) {
	g := newExtraGuard(t)
	defineNoFile(t, g, "tight", 256, 1024)
	if err := g.Define("mixed", impl.Limits{
		Soft: map[int]uint64{
			impl.ResourceNoFile: 128,
			impl.ResourceCore:   1024,
		},
		Hard: map[int]uint64{
			impl.ResourceNoFile: 8192,
			impl.ResourceCore:   2048,
		},
	}); err != nil {
		t.Fatalf("define mixed: %v", err)
	}
	if _, err := g.Apply(105, []string{"tight"}); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if _, err := g.Apply(105, []string{"mixed"}); !errors.Is(err, impl.ErrHardRaise) {
		t.Fatalf("mixed apply must fail with ErrHardRaise, got %v", err)
	}
	lim, err := g.Effective(105)
	if err != nil {
		t.Fatalf("effective: %v", err)
	}
	if got := lim.Hard[impl.ResourceNoFile]; got != 1024 {
		t.Fatalf("nofile hard = %d, want 1024", got)
	}
	if _, ok := lim.Hard[impl.ResourceCore]; ok {
		t.Fatalf("core must not be present after a rolled-back apply")
	}
}

// After a snapshot, a brand new guard must accept Inherit requests and keep
// enforcing the ceilings restored from disk.
func TestExtraInheritOnRestoredGuard(t *testing.T) {
	g := newExtraGuard(t)
	defineNoFile(t, g, "parent-profile", 2048, 8192)
	defineNoFile(t, g, "child-profile", 1024, 4096)
	parent, err := g.Apply(106, []string{"parent-profile"})
	if err != nil {
		t.Fatalf("parent apply: %v", err)
	}
	payload, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	restored := impl.NewRlimitProfileGuard(impl.Options{})
	if err := restored.Restore(payload); err != nil {
		t.Fatalf("restore on new guard: %v", err)
	}
	child, err := restored.Inherit(parent, impl.Request{
		Names:     []string{"child-profile"},
		RaiseSoft: map[int]uint64{impl.ResourceNoFile: 3000},
	})
	if err != nil {
		t.Fatalf("inherit after restart: %v", err)
	}
	if got := child.Limits.Hard[impl.ResourceNoFile]; got != 4096 {
		t.Fatalf("child hard = %d, want 4096", got)
	}
	if got := child.Limits.Soft[impl.ResourceNoFile]; got != 3000 {
		t.Fatalf("child soft = %d, want 3000", got)
	}
	if _, err := restored.Inherit(parent, impl.Request{
		Names:     []string{"child-profile"},
		RaiseHard: map[int]uint64{impl.ResourceNoFile: 16384},
	}); !errors.Is(err, impl.ErrHardRaise) {
		t.Fatalf("hard above parent hard must fail with ErrHardRaise, got %v", err)
	}
}

// Restoring the same snapshot twice must produce identical guard state.
func TestExtraRestoreIsIdempotent(t *testing.T) {
	g := newExtraGuard(t)
	defineNoFile(t, g, "io", 512, 2048)
	if _, err := g.Apply(107, []string{"io"}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	payload, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	restored := impl.NewRlimitProfileGuard(impl.Options{})
	for i := 0; i < 2; i++ {
		if err := restored.Restore(payload); err != nil {
			t.Fatalf("restore %d: %v", i, err)
		}
	}
	lim, err := restored.Effective(107)
	if err != nil {
		t.Fatalf("effective: %v", err)
	}
	if got := lim.Soft[impl.ResourceNoFile]; got != 512 {
		t.Fatalf("soft = %d, want 512", got)
	}
	if got := lim.Hard[impl.ResourceNoFile]; got != 2048 {
		t.Fatalf("hard = %d, want 2048", got)
	}
	if got := restored.Profiles(); len(got) != 1 || got[0] != "io" {
		t.Fatalf("profiles = %v, want [io]", got)
	}
}

// Reset makes the next Apply behave like a fresh process, and a second Reset
// keeps reporting an unknown pid.
func TestExtraApplyAfterResetIsFresh(t *testing.T) {
	g := newExtraGuard(t)
	defineNoFile(t, g, "wide", 1024, 8192)
	defineNoFile(t, g, "narrow", 256, 1024)
	if _, err := g.Apply(108, []string{"narrow"}); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if err := g.Reset(108); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if err := g.Reset(108); !errors.Is(err, impl.ErrUnknownPID) {
		t.Fatalf("second reset = %v, want ErrUnknownPID", err)
	}
	if _, err := g.Apply(108, []string{"wide"}); err != nil {
		t.Fatalf("apply after reset must be treated as fresh: %v", err)
	}
	lim, err := g.Effective(108)
	if err != nil {
		t.Fatalf("effective: %v", err)
	}
	if got := lim.Hard[impl.ResourceNoFile]; got != 8192 {
		t.Fatalf("hard = %d, want 8192 for a fresh process", got)
	}
}
