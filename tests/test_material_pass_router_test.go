package task

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	impl "gsb/batch48-go-game-b-03"
)

func samePasses(got []impl.PassID, want ...impl.PassID) bool {
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

func sameNames(got []string, want ...string) bool {
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

func TestRouteSendsDepthWritingOpaqueMaterialToTheOpaquePass(t *testing.T) {
	r := impl.NewMaterialPassRouter()
	got, err := r.Route(impl.Material{ID: "m1", Blend: impl.BlendOpaque, DepthWrite: true})
	if err != nil {
		t.Fatalf("routing must succeed, got %v", err)
	}
	if !samePasses(got, impl.PassOpaque) {
		t.Fatalf("passes = %v, want the single opaque pass", got)
	}
	got, err = r.Route(impl.Material{ID: "m2", Blend: impl.BlendOpaque, DepthWrite: true, DoubleSided: true})
	if err != nil {
		t.Fatalf("routing must succeed, got %v", err)
	}
	if !samePasses(got, impl.PassOpaque) {
		t.Fatalf("double sided material went to %v, want the single opaque pass", got)
	}
}

func TestRouteNeverPutsBlendedMaterialIntoTheOpaquePass(t *testing.T) {
	r := impl.NewMaterialPassRouter()
	got, err := r.Route(impl.Material{ID: "a1", Blend: impl.BlendAlpha, DepthWrite: true})
	if err != nil {
		t.Fatalf("routing must succeed, got %v", err)
	}
	if !samePasses(got, impl.PassTransparent) {
		t.Fatalf("alpha material went to %v, want only the transparent pass", got)
	}
	got, err = r.Route(impl.Material{ID: "a2", Blend: impl.BlendAdditive})
	if err != nil {
		t.Fatalf("routing must succeed, got %v", err)
	}
	if !samePasses(got, impl.PassTransparent) {
		t.Fatalf("additive material went to %v, want only the transparent pass", got)
	}
	got, err = r.Route(impl.Material{ID: "a3", Blend: impl.BlendMasked, DepthWrite: true})
	if err != nil {
		t.Fatalf("routing must succeed, got %v", err)
	}
	if !samePasses(got, impl.PassOpaque) {
		t.Fatalf("depth writing masked material went to %v, want only the opaque pass", got)
	}
}

func TestRouteSendsDepthWriteFreeOpaqueMaterialToTransparency(t *testing.T) {
	r := impl.NewMaterialPassRouter()
	got, err := r.Route(impl.Material{ID: "m1", Blend: impl.BlendOpaque, DepthWrite: false})
	if err != nil {
		t.Fatalf("routing must succeed, got %v", err)
	}
	if !samePasses(got, impl.PassTransparent) {
		t.Fatalf("opaque material without depth write went to %v, want only the transparent pass", got)
	}
}

func TestRouteAddsTheShadowPassFirstForCasters(t *testing.T) {
	r := impl.NewMaterialPassRouter()
	got, err := r.Route(impl.Material{ID: "m1", Blend: impl.BlendOpaque, DepthWrite: true, CastShadow: true})
	if err != nil {
		t.Fatalf("routing must succeed, got %v", err)
	}
	if !samePasses(got, impl.PassShadow, impl.PassOpaque) {
		t.Fatalf("passes = %v, want shadow before opaque", got)
	}
	got, err = r.Route(impl.Material{ID: "m2", Blend: impl.BlendAlpha, CastShadow: true})
	if err != nil {
		t.Fatalf("routing must succeed, got %v", err)
	}
	if !samePasses(got, impl.PassShadow, impl.PassTransparent) {
		t.Fatalf("passes = %v, want shadow before transparent", got)
	}
}

func TestRouteRejectsEmptyIDsAndUnknownBlendModes(t *testing.T) {
	r := impl.NewMaterialPassRouter()
	if _, err := r.Route(impl.Material{ID: "", Blend: impl.BlendOpaque}); !errors.Is(err, impl.ErrEmptyMaterialID) {
		t.Fatalf("an empty material id must report ErrEmptyMaterialID, got %v", err)
	}
	if _, err := r.Route(impl.Material{ID: "m1", Blend: impl.BlendMode("stencil")}); !errors.Is(err, impl.ErrUnknownBlend) {
		t.Fatalf("an unknown blend mode must report ErrUnknownBlend, got %v", err)
	}
	if _, err := r.Route(impl.Material{ID: "m1", Blend: ""}); !errors.Is(err, impl.ErrUnknownBlend) {
		t.Fatalf("an empty blend mode must report ErrUnknownBlend, got %v", err)
	}
}

func TestVariantKeyHasAStableReadableShape(t *testing.T) {
	r := impl.NewMaterialPassRouter()
	mat := impl.Material{ID: "m1", Blend: impl.BlendOpaque, DepthWrite: true}
	if _, err := r.Route(mat); err != nil {
		t.Fatalf("routing must succeed, got %v", err)
	}
	key, err := r.VariantKey(mat, impl.PassOpaque)
	if err != nil {
		t.Fatalf("computing the variant key must succeed, got %v", err)
	}
	if key != "opaque|opaque|ds=0|dw=1|cs=0" {
		t.Fatalf("variant key = %q, want %q", key, "opaque|opaque|ds=0|dw=1|cs=0")
	}
	again, err := r.VariantKey(mat, impl.PassOpaque)
	if err != nil || again != key {
		t.Fatalf("repeated variant key = %q err %v, want the stable key %q", again, err, key)
	}
	sided := impl.Material{ID: "m2", Blend: impl.BlendOpaque, DepthWrite: true, DoubleSided: true}
	if _, err := r.Route(sided); err != nil {
		t.Fatalf("routing must succeed, got %v", err)
	}
	key, err = r.VariantKey(sided, impl.PassOpaque)
	if err != nil {
		t.Fatalf("computing the variant key must succeed, got %v", err)
	}
	if key != "opaque|opaque|ds=1|dw=1|cs=0" {
		t.Fatalf("double sided variant key = %q, want %q", key, "opaque|opaque|ds=1|dw=1|cs=0")
	}
}

func TestVariantKeyRejectsUnknownMaterialsAndPasses(t *testing.T) {
	r := impl.NewMaterialPassRouter()
	if _, err := r.VariantKey(impl.Material{ID: "m1", Blend: impl.BlendOpaque}, impl.PassID("probe")); !errors.Is(err, impl.ErrUnknownPass) {
		t.Fatalf("an unknown pass must report ErrUnknownPass, got %v", err)
	}
	if _, err := r.VariantKey(impl.Material{ID: "never-routed", Blend: impl.BlendOpaque}, impl.PassOpaque); !errors.Is(err, impl.ErrUnknownMaterial) {
		t.Fatalf("an unrouted material must report ErrUnknownMaterial, got %v", err)
	}
	alpha := impl.Material{ID: "a1", Blend: impl.BlendAlpha}
	if _, err := r.Route(alpha); err != nil {
		t.Fatalf("routing must succeed, got %v", err)
	}
	if _, err := r.VariantKey(alpha, impl.PassOpaque); !errors.Is(err, impl.ErrPassNotRouted) {
		t.Fatalf("a pass outside the route must report ErrPassNotRouted, got %v", err)
	}
	if _, err := r.VariantKey(alpha, impl.PassTransparent); err != nil {
		t.Fatalf("a pass inside the route must succeed, got %v", err)
	}
}

func TestAssignCountsAMaterialOnlyOncePerPass(t *testing.T) {
	r := impl.NewMaterialPassRouter()
	if _, err := r.Route(impl.Material{ID: "m1", Blend: impl.BlendOpaque, DepthWrite: true}); err != nil {
		t.Fatalf("routing must succeed, got %v", err)
	}
	if err := r.Assign(impl.PassOpaque, "m1"); err != nil {
		t.Fatalf("first assign must succeed, got %v", err)
	}
	if err := r.Assign(impl.PassOpaque, "m1"); err != nil {
		t.Fatalf("repeating the assign must be idempotent, got %v", err)
	}
	if got := r.Materials(impl.PassOpaque); !sameNames(got, "m1") {
		t.Fatalf("pass materials = %v, want the material exactly once", got)
	}
}

func TestAssignRejectsUnknownMaterialsAndForeignPasses(t *testing.T) {
	r := impl.NewMaterialPassRouter()
	if _, err := r.Route(impl.Material{ID: "m1", Blend: impl.BlendOpaque, DepthWrite: true}); err != nil {
		t.Fatalf("routing must succeed, got %v", err)
	}
	if _, err := r.Route(impl.Material{ID: "a1", Blend: impl.BlendAlpha}); err != nil {
		t.Fatalf("routing must succeed, got %v", err)
	}
	if err := r.Assign(impl.PassOpaque, "ghost"); !errors.Is(err, impl.ErrUnknownMaterial) {
		t.Fatalf("assigning an unrouted material must report ErrUnknownMaterial, got %v", err)
	}
	if err := r.Assign(impl.PassOpaque, "a1"); !errors.Is(err, impl.ErrPassNotRouted) {
		t.Fatalf("assigning a foreign pass must report ErrPassNotRouted, got %v", err)
	}
	if err := r.Assign(impl.PassID("probe"), "m1"); !errors.Is(err, impl.ErrUnknownPass) {
		t.Fatalf("assigning an unknown pass must report ErrUnknownPass, got %v", err)
	}
	if got := r.Materials(impl.PassOpaque); len(got) != 0 {
		t.Fatalf("rejected assigns must not fill the pass, got %v", got)
	}
}

func TestPassBudgetStopsTheNextMaterial(t *testing.T) {
	r := impl.NewMaterialPassRouter()
	if err := r.PassBudget(impl.PassOpaque, 2); err != nil {
		t.Fatalf("setting the budget must succeed, got %v", err)
	}
	for i := 0; i < 2; i++ {
		id := fmt.Sprintf("m%d", i)
		if _, err := r.Route(impl.Material{ID: id, Blend: impl.BlendOpaque, DepthWrite: true}); err != nil {
			t.Fatalf("routing %s must succeed, got %v", id, err)
		}
		if err := r.Assign(impl.PassOpaque, id); err != nil {
			t.Fatalf("assigning %s must succeed, got %v", id, err)
		}
	}
	if _, err := r.Route(impl.Material{ID: "m9", Blend: impl.BlendOpaque, DepthWrite: true}); err != nil {
		t.Fatalf("routing m9 must succeed, got %v", err)
	}
	if err := r.Assign(impl.PassOpaque, "m9"); !errors.Is(err, impl.ErrBudgetExceeded) {
		t.Fatalf("assigning past the budget must report ErrBudgetExceeded, got %v", err)
	}
	if got := r.Materials(impl.PassOpaque); len(got) != 2 {
		t.Fatalf("pass materials = %v, want two", got)
	}
	if err := r.PassBudget(impl.PassOpaque, 0); !errors.Is(err, impl.ErrBadBudget) {
		t.Fatalf("a zero budget must report ErrBadBudget, got %v", err)
	}
	if err := r.PassBudget(impl.PassID("probe"), 1); !errors.Is(err, impl.ErrUnknownPass) {
		t.Fatalf("a budget on an unknown pass must report ErrUnknownPass, got %v", err)
	}
}

func TestMaterialsKeepDrawOrder(t *testing.T) {
	r := impl.NewMaterialPassRouter()
	entries := []struct {
		id  string
		key int
	}{
		{"late", 50},
		{"early", 10},
		{"middle", 30},
		{"twin-b", 10},
	}
	for _, e := range entries {
		if _, err := r.Route(impl.Material{ID: e.id, Blend: impl.BlendOpaque, DepthWrite: true, SortKey: e.key}); err != nil {
			t.Fatalf("routing %s must succeed, got %v", e.id, err)
		}
	}
	for _, e := range entries {
		if err := r.Assign(impl.PassOpaque, e.id); err != nil {
			t.Fatalf("assigning %s must succeed, got %v", e.id, err)
		}
	}
	if got := r.Materials(impl.PassOpaque); !sameNames(got, "early", "twin-b", "middle", "late") {
		t.Fatalf("draw order = %v, want early, twin-b, middle, late", got)
	}
}

func TestInvalidateDropsOnlyThatMaterialsVariants(t *testing.T) {
	r := impl.NewMaterialPassRouter()
	first := impl.Material{ID: "m1", Blend: impl.BlendOpaque, DepthWrite: true}
	second := impl.Material{ID: "m2", Blend: impl.BlendOpaque, DepthWrite: true}
	for _, mat := range []impl.Material{first, second} {
		if _, err := r.Route(mat); err != nil {
			t.Fatalf("routing %s must succeed, got %v", mat.ID, err)
		}
		if _, err := r.VariantKey(mat, impl.PassOpaque); err != nil {
			t.Fatalf("computing the key of %s must succeed, got %v", mat.ID, err)
		}
	}
	if got := r.CacheEntries(); got != 2 {
		t.Fatalf("cache entries = %d, want 2", got)
	}
	if err := r.Invalidate("m1"); err != nil {
		t.Fatalf("invalidating m1 must succeed, got %v", err)
	}
	if got := r.CacheEntries(); got != 1 {
		t.Fatalf("cache entries after invalidating m1 = %d, want 1", got)
	}
	if err := r.Invalidate("m2"); err != nil {
		t.Fatalf("invalidating m2 must succeed, got %v", err)
	}
	if got := r.CacheEntries(); got != 0 {
		t.Fatalf("cache entries after invalidating m2 = %d, want 0", got)
	}
}

func TestInvalidateRejectsUnknownMaterialsAndRebuildsKeys(t *testing.T) {
	r := impl.NewMaterialPassRouter()
	if err := r.Invalidate("ghost"); !errors.Is(err, impl.ErrUnknownMaterial) {
		t.Fatalf("invalidating an unknown material must report ErrUnknownMaterial, got %v", err)
	}
	mat := impl.Material{ID: "m1", Blend: impl.BlendOpaque, DepthWrite: true}
	if _, err := r.Route(mat); err != nil {
		t.Fatalf("routing must succeed, got %v", err)
	}
	key, err := r.VariantKey(mat, impl.PassOpaque)
	if err != nil {
		t.Fatalf("computing the key must succeed, got %v", err)
	}
	if err := r.Invalidate("m1"); err != nil {
		t.Fatalf("invalidating must succeed, got %v", err)
	}
	rebuilt, err := r.VariantKey(mat, impl.PassOpaque)
	if err != nil {
		t.Fatalf("rebuilding the key must succeed, got %v", err)
	}
	if rebuilt != key {
		t.Fatalf("rebuilt key = %q, want the original %q", rebuilt, key)
	}
	if got := r.CacheEntries(); got != 1 {
		t.Fatalf("cache entries after the rebuild = %d, want 1", got)
	}
}

func TestSnapshotRestoreKeepsRoutesAssignmentsAndBudgets(t *testing.T) {
	r := impl.NewMaterialPassRouter()
	if err := r.PassBudget(impl.PassOpaque, 2); err != nil {
		t.Fatalf("setting the budget must succeed, got %v", err)
	}
	for _, id := range []string{"m1", "m2"} {
		if _, err := r.Route(impl.Material{ID: id, Blend: impl.BlendOpaque, DepthWrite: true, SortKey: 1}); err != nil {
			t.Fatalf("routing %s must succeed, got %v", id, err)
		}
		if err := r.Assign(impl.PassOpaque, id); err != nil {
			t.Fatalf("assigning %s must succeed, got %v", id, err)
		}
	}
	data, err := r.Snapshot()
	if err != nil {
		t.Fatalf("snapshot must succeed, got %v", err)
	}
	restored := impl.NewMaterialPassRouter()
	if err := restored.Restore(data); err != nil {
		t.Fatalf("restore must succeed, got %v", err)
	}
	if got := restored.Materials(impl.PassOpaque); !sameNames(got, "m1", "m2") {
		t.Fatalf("restored pass = %v, want m1, m2", got)
	}
	key, err := restored.VariantKey(impl.Material{ID: "m1", Blend: impl.BlendOpaque, DepthWrite: true}, impl.PassOpaque)
	if err != nil || key != "opaque|opaque|ds=0|dw=1|cs=0" {
		t.Fatalf("restored variant key = %q err %v, want the original key", key, err)
	}
	if _, err := restored.Route(impl.Material{ID: "m3", Blend: impl.BlendOpaque, DepthWrite: true}); err != nil {
		t.Fatalf("routing after restore must succeed, got %v", err)
	}
	if err := restored.Assign(impl.PassOpaque, "m3"); !errors.Is(err, impl.ErrBudgetExceeded) {
		t.Fatalf("the restored budget must still be 2, got %v", err)
	}
	if err := restored.Restore([]byte("{")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("a broken snapshot must report ErrBadSnapshot, got %v", err)
	}
	if got := restored.Materials(impl.PassOpaque); len(got) != 2 {
		t.Fatalf("a rejected snapshot must leave the pass alone, got %v", got)
	}
}

func TestConcurrentAssignFillsEachPassExactlyOnce(t *testing.T) {
	r := impl.NewMaterialPassRouter()
	const total = 16
	if err := r.PassBudget(impl.PassOpaque, total); err != nil {
		t.Fatalf("setting the budget must succeed, got %v", err)
	}
	want := make([]string, total)
	for i := 0; i < total; i++ {
		id := fmt.Sprintf("m%02d", i)
		want[i] = id
		if _, err := r.Route(impl.Material{ID: id, Blend: impl.BlendOpaque, DepthWrite: true}); err != nil {
			t.Fatalf("routing %s must succeed, got %v", id, err)
		}
	}
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for n := 0; n < 4; n++ {
				if err := r.Assign(impl.PassOpaque, want[worker*4+n]); err != nil {
					t.Errorf("worker %d could not assign: %v", worker, err)
				}
			}
		}(w)
	}
	wg.Wait()
	if got := r.Materials(impl.PassOpaque); !sameNames(got, want...) {
		t.Fatalf("pass materials = %v, want every routed material exactly once", got)
	}
}
