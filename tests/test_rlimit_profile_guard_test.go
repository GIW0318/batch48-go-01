package task

import (
	"errors"
	"sync"
	"testing"

	impl "gsb/batch48-go-safety-b-06"
)

func defineNoFile(t *testing.T, g *impl.RlimitProfileGuard, name string, soft, hard uint64) {
	t.Helper()
	if err := g.Define(name, impl.Limits{
		Soft: map[int]uint64{impl.ResourceNoFile: soft},
		Hard: map[int]uint64{impl.ResourceNoFile: hard},
	}); err != nil {
		t.Fatalf("define profile %q: %v", name, err)
	}
}

func TestDefineRejectsBlankName(t *testing.T) {
	g := impl.NewRlimitProfileGuard(impl.Options{MaxProfiles: 4, MaxStack: 4})
	err := g.Define("", impl.Limits{Soft: map[int]uint64{impl.ResourceNoFile: 512}})
	if !errors.Is(err, impl.ErrBlankName) {
		t.Fatalf("blank profile name must fail with ErrBlankName, got %v", err)
	}
	if got := len(g.Profiles()); got != 0 {
		t.Fatalf("rejected definition must not be stored, profile count = %d", got)
	}
}

func TestDefineRejectsDuplicateProfile(t *testing.T) {
	g := impl.NewRlimitProfileGuard(impl.Options{MaxProfiles: 4, MaxStack: 4})
	defineNoFile(t, g, "worker", 1024, 4096)
	err := g.Define("worker", impl.Limits{Soft: map[int]uint64{impl.ResourceNoFile: 256}})
	if !errors.Is(err, impl.ErrDuplicateProfile) {
		t.Fatalf("duplicate name must fail with ErrDuplicateProfile, got %v", err)
	}
	stored, ok := g.Profile("worker")
	if !ok {
		t.Fatalf("original profile must survive a rejected duplicate")
	}
	if got := stored.Hard[impl.ResourceNoFile]; got != 4096 {
		t.Fatalf("original hard limit changed to %d", got)
	}
}

func TestDefineRejectsSoftAboveHard(t *testing.T) {
	g := impl.NewRlimitProfileGuard(impl.Options{MaxProfiles: 4, MaxStack: 4})
	err := g.Define("broken", impl.Limits{
		Soft: map[int]uint64{impl.ResourceNoFile: 8192},
		Hard: map[int]uint64{impl.ResourceNoFile: 4096},
	})
	if !errors.Is(err, impl.ErrSoftAboveHard) {
		t.Fatalf("soft above hard must fail with ErrSoftAboveHard, got %v", err)
	}
}

func TestProfileLimitEnforced(t *testing.T) {
	g := impl.NewRlimitProfileGuard(impl.Options{MaxProfiles: 2, MaxStack: 4})
	defineNoFile(t, g, "one", 256, 512)
	defineNoFile(t, g, "two", 512, 1024)
	err := g.Define("three", impl.Limits{Hard: map[int]uint64{impl.ResourceNoFile: 2048}})
	if !errors.Is(err, impl.ErrProfileLimit) {
		t.Fatalf("third profile must fail with ErrProfileLimit, got %v", err)
	}
	if got := len(g.Profiles()); got != 2 {
		t.Fatalf("profile table size = %d, want 2", got)
	}
}

func TestProfilesListedInDefinitionOrder(t *testing.T) {
	g := impl.NewRlimitProfileGuard(impl.Options{MaxProfiles: 8, MaxStack: 4})
	defineNoFile(t, g, "zeta", 256, 512)
	defineNoFile(t, g, "alpha", 256, 512)
	defineNoFile(t, g, "mid", 256, 512)
	got := g.Profiles()
	want := []string{"zeta", "alpha", "mid"}
	if len(got) != len(want) {
		t.Fatalf("profile listing length = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("profile %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestApplyUnknownProfileFailsClosed(t *testing.T) {
	g := impl.NewRlimitProfileGuard(impl.Options{MaxProfiles: 4, MaxStack: 4})
	defineNoFile(t, g, "base", 512, 1024)
	if _, err := g.Apply(900, []string{"base", "ghost"}); !errors.Is(err, impl.ErrProfileNotFound) {
		t.Fatalf("unknown profile name must fail with ErrProfileNotFound, got %v", err)
	}
	if _, err := g.Effective(900); !errors.Is(err, impl.ErrUnknownPID) {
		t.Fatalf("failed apply must leave the process untouched, Effective err = %v", err)
	}
}

func TestApplyIntersectsStackedProfiles(t *testing.T) {
	g := impl.NewRlimitProfileGuard(impl.Options{MaxProfiles: 4, MaxStack: 4})
	defineNoFile(t, g, "io", 1024, 4096)
	defineNoFile(t, g, "batch", 2048, 8192)
	applied, err := g.Apply(7, []string{"io", "batch"})
	if err != nil {
		t.Fatalf("stacking two profiles must succeed, got %v", err)
	}
	if applied.PID != 7 {
		t.Fatalf("applied pid = %d, want 7", applied.PID)
	}
	lim, err := g.Effective(7)
	if err != nil {
		t.Fatalf("effective limits after apply: %v", err)
	}
	if got := lim.Soft[impl.ResourceNoFile]; got != 1024 {
		t.Fatalf("effective soft = %d, want 1024 from the intersection", got)
	}
	if got := lim.Hard[impl.ResourceNoFile]; got != 4096 {
		t.Fatalf("effective hard = %d, want 4096 from the intersection", got)
	}
}

func TestApplyRejectsEmptyStack(t *testing.T) {
	g := impl.NewRlimitProfileGuard(impl.Options{MaxProfiles: 4, MaxStack: 4})
	if _, err := g.Apply(11, nil); !errors.Is(err, impl.ErrEmptyStack) {
		t.Fatalf("empty stack must fail with ErrEmptyStack, got %v", err)
	}
	if _, err := g.Effective(11); !errors.Is(err, impl.ErrUnknownPID) {
		t.Fatalf("empty stack must not create process state, Effective err = %v", err)
	}
}

func TestApplyTightensExistingHard(t *testing.T) {
	g := impl.NewRlimitProfileGuard(impl.Options{MaxProfiles: 4, MaxStack: 4})
	defineNoFile(t, g, "wide", 1024, 8192)
	defineNoFile(t, g, "tight", 512, 2048)
	if _, err := g.Apply(31, []string{"wide"}); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if _, err := g.Apply(31, []string{"tight"}); err != nil {
		t.Fatalf("tightening apply: %v", err)
	}
	lim, err := g.Effective(31)
	if err != nil {
		t.Fatalf("effective limits: %v", err)
	}
	if got := lim.Hard[impl.ResourceNoFile]; got != 2048 {
		t.Fatalf("tightened hard = %d, want 2048", got)
	}
	if got := lim.Soft[impl.ResourceNoFile]; got != 512 {
		t.Fatalf("tightened soft = %d, want 512", got)
	}
}

func TestInheritIntersectsParentAndRequest(t *testing.T) {
	g := impl.NewRlimitProfileGuard(impl.Options{MaxProfiles: 4, MaxStack: 4})
	defineNoFile(t, g, "child", 1024, 4096)
	parent := impl.Applied{
		PID:   12,
		Names: []string{"base"},
		Limits: impl.Limits{
			Soft: map[int]uint64{impl.ResourceNoFile: 2048},
			Hard: map[int]uint64{impl.ResourceNoFile: 8192},
		},
	}
	got, err := g.Inherit(parent, impl.Request{
		Names:     []string{"child"},
		RaiseSoft: map[int]uint64{impl.ResourceNoFile: 3000},
	})
	if err != nil {
		t.Fatalf("inherit with a legal soft raise must succeed, got %v", err)
	}
	if got.PID != 12 {
		t.Fatalf("inherited pid = %d, want 12", got.PID)
	}
	if got := got.Limits.Hard[impl.ResourceNoFile]; got != 4096 {
		t.Fatalf("inherited hard = %d, want 4096", got)
	}
	if got := got.Limits.Soft[impl.ResourceNoFile]; got != 3000 {
		t.Fatalf("inherited soft = %d, want 3000", got)
	}
}

func TestInheritRejectsRaisedSoftBeyondHard(t *testing.T) {
	g := impl.NewRlimitProfileGuard(impl.Options{MaxProfiles: 4, MaxStack: 4})
	defineNoFile(t, g, "child", 512, 4096)
	parent := impl.Applied{
		PID: 13,
		Limits: impl.Limits{
			Soft: map[int]uint64{impl.ResourceNoFile: 1024},
			Hard: map[int]uint64{impl.ResourceNoFile: 4096},
		},
	}
	_, err := g.Inherit(parent, impl.Request{
		Names:     []string{"child"},
		RaiseSoft: map[int]uint64{impl.ResourceNoFile: 5000},
	})
	if !errors.Is(err, impl.ErrSoftAboveHard) {
		t.Fatalf("soft raise beyond hard must fail with ErrSoftAboveHard, got %v", err)
	}
}

func TestResetClearsProcessState(t *testing.T) {
	g := impl.NewRlimitProfileGuard(impl.Options{MaxProfiles: 4, MaxStack: 4})
	defineNoFile(t, g, "io", 512, 1024)
	if _, err := g.Apply(3, []string{"io"}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := g.Reset(3); err != nil {
		t.Fatalf("reset known process: %v", err)
	}
	if _, err := g.Effective(3); !errors.Is(err, impl.ErrUnknownPID) {
		t.Fatalf("Effective after reset = %v, want ErrUnknownPID", err)
	}
	if err := g.Reset(3); !errors.Is(err, impl.ErrUnknownPID) {
		t.Fatalf("second reset = %v, want ErrUnknownPID", err)
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	g := impl.NewRlimitProfileGuard(impl.Options{MaxProfiles: 4, MaxStack: 4})
	defineNoFile(t, g, "io", 768, 1536)
	if _, err := g.Apply(21, []string{"io"}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(snap) == 0 {
		t.Fatalf("snapshot payload must not be empty")
	}
	restored := impl.NewRlimitProfileGuard(impl.Options{MaxProfiles: 4, MaxStack: 4})
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restore: %v", err)
	}
	lim, err := restored.Effective(21)
	if err != nil {
		t.Fatalf("effective after restore: %v", err)
	}
	if got := lim.Soft[impl.ResourceNoFile]; got != 768 {
		t.Fatalf("restored soft = %d, want 768", got)
	}
	if got := lim.Hard[impl.ResourceNoFile]; got != 1536 {
		t.Fatalf("restored hard = %d, want 1536", got)
	}
	if _, ok := restored.Profile("io"); !ok {
		t.Fatalf("restored guard lost profile %q", "io")
	}
}

func TestRestoreRejectsCorruptSnapshotKeepsState(t *testing.T) {
	g := impl.NewRlimitProfileGuard(impl.Options{MaxProfiles: 4, MaxStack: 4})
	defineNoFile(t, g, "io", 640, 1280)
	if _, err := g.Apply(22, []string{"io"}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := g.Restore([]byte("{\"profiles\":")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("corrupt snapshot must fail with ErrBadSnapshot, got %v", err)
	}
	lim, err := g.Effective(22)
	if err != nil {
		t.Fatalf("corrupt restore must keep the live state, Effective err = %v", err)
	}
	if got := lim.Hard[impl.ResourceNoFile]; got != 1280 {
		t.Fatalf("live hard after rejected restore = %d, want 1280", got)
	}
}

func TestConcurrentApplyOnSamePIDIsSerialized(t *testing.T) {
	g := impl.NewRlimitProfileGuard(impl.Options{MaxProfiles: 4, MaxStack: 4})
	defineNoFile(t, g, "io", 1024, 4096)
	const workers = 8
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			_, errs[slot] = g.Apply(55, []string{"io"})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent apply %d failed: %v", i, err)
		}
	}
	lim, err := g.Effective(55)
	if err != nil {
		t.Fatalf("effective limits after concurrent applies: %v", err)
	}
	if got := lim.Hard[impl.ResourceNoFile]; got != 4096 {
		t.Fatalf("hard after concurrent applies = %d, want 4096", got)
	}
	if got := lim.Soft[impl.ResourceNoFile]; got != 1024 {
		t.Fatalf("soft after concurrent applies = %d, want 1024", got)
	}
}
