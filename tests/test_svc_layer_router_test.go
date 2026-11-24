package task

import (
	"errors"
	"testing"

	impl "gsb/batch48-go-game-c-11"
)

func layer(t, s, q int) impl.LayerID {
	return impl.LayerID{Temporal: t, Spatial: s, Quality: q}
}

func TestCoverageListsDependencyClosure(t *testing.T) {
	r := impl.NewLayerRouter(impl.RouterConfig{MaxSubscribers: 8, MaxLayers: 64})
	got := r.Coverage(layer(1, 1, 0))
	want := []impl.LayerID{layer(0, 0, 0), layer(0, 1, 0), layer(1, 0, 0), layer(1, 1, 0)}
	if len(got) != len(want) {
		t.Fatalf("coverage has %d layers, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("coverage entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestCoverageOfBaseLayerIsSingle(t *testing.T) {
	r := impl.NewLayerRouter(impl.RouterConfig{MaxSubscribers: 4, MaxLayers: 16})
	got := r.Coverage(layer(0, 0, 0))
	if len(got) != 1 {
		t.Fatalf("base layer closure has %d entries, want 1", len(got))
	}
	if got[0] != layer(0, 0, 0) {
		t.Fatalf("base layer closure starts with %+v", got[0])
	}
}

func TestCostCountsClosureLayers(t *testing.T) {
	r := impl.NewLayerRouter(impl.RouterConfig{MaxSubscribers: 4, MaxLayers: 16})
	if got := r.Cost(layer(1, 1, 1)); got != 800 {
		t.Fatalf("cost of 1/1/1 = %d, want 800", got)
	}
	if got := r.Cost(layer(2, 0, 0)); got != 300 {
		t.Fatalf("cost of 2/0/0 = %d, want 300", got)
	}
}

func TestDropPlanListsDependentsFirst(t *testing.T) {
	r := impl.NewLayerRouter(impl.RouterConfig{MaxSubscribers: 4, MaxLayers: 16})
	got := r.DropPlan(layer(1, 1, 0), layer(0, 0, 0))
	want := []impl.LayerID{layer(1, 1, 0), layer(1, 0, 0), layer(0, 1, 0)}
	if len(got) != len(want) {
		t.Fatalf("drop plan has %d layers, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("drop plan entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestSubscribeRejectsBlankIdentity(t *testing.T) {
	r := impl.NewLayerRouter(impl.RouterConfig{MaxSubscribers: 4, MaxLayers: 16})
	if err := r.Subscribe("   ", 500); !errors.Is(err, impl.ErrBlankSubscriber) {
		t.Fatalf("subscribe with blank id returned %v, want ErrBlankSubscriber", err)
	}
	if got := r.Stats().Subscribers; got != 0 {
		t.Fatalf("subscriber count = %d after rejected subscribe, want 0", got)
	}
}

func TestSubscribeRejectsNonPositiveBudget(t *testing.T) {
	r := impl.NewLayerRouter(impl.RouterConfig{MaxSubscribers: 4, MaxLayers: 16})
	if err := r.Subscribe("viewer-a", 0); !errors.Is(err, impl.ErrBudgetInvalid) {
		t.Fatalf("subscribe with zero budget returned %v, want ErrBudgetInvalid", err)
	}
}

func TestSubscriberLimitEnforcedAndDetachFreesSlot(t *testing.T) {
	r := impl.NewLayerRouter(impl.RouterConfig{MaxSubscribers: 1, MaxLayers: 16})
	if err := r.Subscribe("viewer-a", 500); err != nil {
		t.Fatalf("first subscribe failed: %v", err)
	}
	if err := r.Subscribe("viewer-b", 500); !errors.Is(err, impl.ErrSubscriberLimit) {
		t.Fatalf("second subscribe returned %v, want ErrSubscriberLimit", err)
	}
	if err := r.Detach("viewer-a"); err != nil {
		t.Fatalf("detach failed: %v", err)
	}
	if err := r.Subscribe("viewer-b", 500); err != nil {
		t.Fatalf("subscribe after detach failed: %v", err)
	}
	if got := r.Stats().Detached; got != 1 {
		t.Fatalf("detached counter = %d, want 1", got)
	}
}

func TestDowngradeAppliesWithoutKeyframe(t *testing.T) {
	r := impl.NewLayerRouter(impl.RouterConfig{MaxSubscribers: 4, MaxLayers: 16})
	if err := r.Subscribe("viewer-a", 1000); err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}
	if _, err := r.SelectLayers("viewer-a", layer(2, 0, 0)); err != nil {
		t.Fatalf("select upgrade failed: %v", err)
	}
	if eff, err := r.OnKeyframe("viewer-a", 1000); err != nil || eff != layer(2, 0, 0) {
		t.Fatalf("keyframe applied %+v (err=%v), want 2/0/0", eff, err)
	}
	eff, err := r.SelectLayers("viewer-a", layer(1, 0, 0))
	if err != nil {
		t.Fatalf("select downgrade failed: %v", err)
	}
	if eff != layer(1, 0, 0) {
		t.Fatalf("downgrade effective layer = %+v, want 1/0/0 immediately", eff)
	}
	if got := r.Forwarded("viewer-a"); got != layer(1, 0, 0) {
		t.Fatalf("forwarded layer = %+v after downgrade, want 1/0/0", got)
	}
	if target, ok := r.Pending("viewer-a"); ok {
		t.Fatalf("pending target %+v survived a downgrade", target)
	}
	if got := r.Stats().Downgrades; got != 1 {
		t.Fatalf("downgrade counter = %d, want 1", got)
	}
}

func TestUpgradeWaitsForKeyframe(t *testing.T) {
	r := impl.NewLayerRouter(impl.RouterConfig{MaxSubscribers: 4, MaxLayers: 16})
	if err := r.Subscribe("viewer-a", 1000); err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}
	eff, err := r.SelectLayers("viewer-a", layer(1, 0, 0))
	if err != nil {
		t.Fatalf("select upgrade failed: %v", err)
	}
	if eff != layer(0, 0, 0) {
		t.Fatalf("upgrade took effect at %+v before any keyframe", eff)
	}
	if got := r.Forwarded("viewer-a"); got != layer(0, 0, 0) {
		t.Fatalf("forwarded layer = %+v before keyframe, want base", got)
	}
	if target, ok := r.Pending("viewer-a"); !ok || target != layer(1, 0, 0) {
		t.Fatalf("pending target = %+v (ok=%v), want 1/0/0", target, ok)
	}
	if got := r.Stats().Upgrades; got != 0 {
		t.Fatalf("upgrade counter = %d before keyframe, want 0", got)
	}
	eff, err = r.OnKeyframe("viewer-a", 500)
	if err != nil {
		t.Fatalf("keyframe failed: %v", err)
	}
	if eff != layer(1, 0, 0) {
		t.Fatalf("layer after keyframe = %+v, want 1/0/0", eff)
	}
	stats := r.Stats()
	if stats.Upgrades != 1 || stats.Keyframes != 1 {
		t.Fatalf("counters upgrades=%d keyframes=%d, want 1 and 1", stats.Upgrades, stats.Keyframes)
	}
}

func TestOversizedSelectionRejected(t *testing.T) {
	r := impl.NewLayerRouter(impl.RouterConfig{MaxSubscribers: 4, MaxLayers: 16})
	if err := r.Subscribe("viewer-a", 150); err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}
	if _, err := r.SelectLayers("viewer-a", layer(1, 0, 0)); !errors.Is(err, impl.ErrBudgetExceeded) {
		t.Fatalf("oversized selection returned %v, want ErrBudgetExceeded", err)
	}
	if target, ok := r.Pending("viewer-a"); ok {
		t.Fatalf("rejected selection still queued %+v", target)
	}
	if got := r.Stats().BudgetRejects; got != 1 {
		t.Fatalf("budget reject counter = %d, want 1", got)
	}
}

func TestLowerBudgetDowngradesToHighestAffordable(t *testing.T) {
	r := impl.NewLayerRouter(impl.RouterConfig{MaxSubscribers: 4, MaxLayers: 32})
	if err := r.Subscribe("viewer-a", 800); err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}
	if _, err := r.SelectLayers("viewer-a", layer(1, 1, 1)); err != nil {
		t.Fatalf("select upgrade failed: %v", err)
	}
	if _, err := r.OnKeyframe("viewer-a", 10); err != nil {
		t.Fatalf("keyframe failed: %v", err)
	}
	eff, err := r.SetBudget("viewer-a", 200)
	if err != nil {
		t.Fatalf("set budget failed: %v", err)
	}
	if eff != layer(1, 0, 0) {
		t.Fatalf("layer after budget cut = %+v, want highest affordable 1/0/0", eff)
	}
	if got := r.Forwarded("viewer-a"); got != layer(1, 0, 0) {
		t.Fatalf("forwarded layer = %+v after budget cut, want 1/0/0", got)
	}
	if got := r.Stats().Downgrades; got != 1 {
		t.Fatalf("downgrade counter = %d after budget cut, want 1", got)
	}
}

func TestLayerCapRejectsNewSelection(t *testing.T) {
	r := impl.NewLayerRouter(impl.RouterConfig{MaxSubscribers: 4, MaxLayers: 3})
	if err := r.Subscribe("viewer-a", 1000); err != nil {
		t.Fatalf("subscribe a failed: %v", err)
	}
	if err := r.Subscribe("viewer-b", 1000); err != nil {
		t.Fatalf("subscribe b failed: %v", err)
	}
	if _, err := r.SelectLayers("viewer-a", layer(1, 0, 0)); err != nil {
		t.Fatalf("select a failed: %v", err)
	}
	if _, err := r.OnKeyframe("viewer-a", 1); err != nil {
		t.Fatalf("keyframe a failed: %v", err)
	}
	if _, err := r.SelectLayers("viewer-b", layer(0, 1, 0)); err != nil {
		t.Fatalf("select b failed: %v", err)
	}
	if _, err := r.OnKeyframe("viewer-b", 2); err != nil {
		t.Fatalf("keyframe b failed: %v", err)
	}
	if _, err := r.SelectLayers("viewer-b", layer(0, 1, 1)); !errors.Is(err, impl.ErrLayerLimit) {
		t.Fatalf("selection beyond layer cap returned %v, want ErrLayerLimit", err)
	}
	if got := r.Forwarded("viewer-b"); got != layer(0, 1, 0) {
		t.Fatalf("forwarded layer = %+v after cap rejection, want 0/1/0 kept", got)
	}
}

func TestMixedDimensionSwitchRejected(t *testing.T) {
	r := impl.NewLayerRouter(impl.RouterConfig{MaxSubscribers: 4, MaxLayers: 32})
	if err := r.Subscribe("viewer-a", 1000); err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}
	if _, err := r.SelectLayers("viewer-a", layer(1, 0, 0)); err != nil {
		t.Fatalf("select upgrade failed: %v", err)
	}
	if _, err := r.OnKeyframe("viewer-a", 1); err != nil {
		t.Fatalf("keyframe failed: %v", err)
	}
	if _, err := r.SelectLayers("viewer-a", layer(0, 1, 0)); !errors.Is(err, impl.ErrMixedSwitch) {
		t.Fatalf("mixed switch returned %v, want ErrMixedSwitch", err)
	}
	if got := r.Forwarded("viewer-a"); got != layer(1, 0, 0) {
		t.Fatalf("forwarded layer = %+v after mixed switch, want 1/0/0 kept", got)
	}
}

func TestSnapshotRestoreKeepsSubscribersAndPending(t *testing.T) {
	r := impl.NewLayerRouter(impl.RouterConfig{MaxSubscribers: 8, MaxLayers: 64})
	if err := r.Subscribe("viewer-a", 800); err != nil {
		t.Fatalf("subscribe a failed: %v", err)
	}
	if err := r.Subscribe("viewer-b", 800); err != nil {
		t.Fatalf("subscribe b failed: %v", err)
	}
	if _, err := r.SelectLayers("viewer-a", layer(1, 1, 0)); err != nil {
		t.Fatalf("select a failed: %v", err)
	}
	if _, err := r.OnKeyframe("viewer-a", 100); err != nil {
		t.Fatalf("keyframe a failed: %v", err)
	}
	if _, err := r.SelectLayers("viewer-b", layer(0, 1, 0)); err != nil {
		t.Fatalf("select b failed: %v", err)
	}
	snap, err := r.Snapshot()
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	r2 := impl.NewLayerRouter(impl.RouterConfig{MaxSubscribers: 8, MaxLayers: 64})
	if err := r2.Restore(snap); err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	if got := r2.Stats().Subscribers; got != 2 {
		t.Fatalf("subscribers after restore = %d, want 2", got)
	}
	if got := r2.Forwarded("viewer-a"); got != layer(1, 1, 0) {
		t.Fatalf("forwarded layer of viewer-a after restore = %+v, want 1/1/0", got)
	}
	if target, ok := r2.Pending("viewer-b"); !ok || target != layer(0, 1, 0) {
		t.Fatalf("pending target of viewer-b after restore = %+v (ok=%v), want 0/1/0", target, ok)
	}
	if got := r2.Stats().Keyframes; got != 1 {
		t.Fatalf("keyframe counter after restore = %d, want 1", got)
	}
}

func TestRestoreRejectsCorruptSnapshot(t *testing.T) {
	r := impl.NewLayerRouter(impl.RouterConfig{MaxSubscribers: 4, MaxLayers: 16})
	if err := r.Subscribe("viewer-a", 500); err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}
	if err := r.Restore([]byte("not a router snapshot")); !errors.Is(err, impl.ErrRouterSnapshot) {
		t.Fatalf("corrupt restore returned %v, want ErrRouterSnapshot", err)
	}
	if got := r.Stats().Subscribers; got != 1 {
		t.Fatalf("subscribers after corrupt restore = %d, want 1 untouched", got)
	}
}
