package task

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	impl "gsb/batch48-go-game-b-16"
)

func sampler() impl.Sampler {
	return impl.Sampler{WrapU: "repeat", WrapV: "repeat", Filter: "linear", Anisotropy: 1}
}

func TestBindValidatesMaterialSlotAndSampler(t *testing.T) {
	b := impl.NewTextureSlotBinder("default_albedo", 8)
	good := sampler()

	if _, err := b.Bind("", impl.Slot{Name: "base", Texture: "rock", Sampler: good}); !errors.Is(err, impl.ErrEmptyMaterial) {
		t.Fatalf("empty material id: got %v, want ErrEmptyMaterial", err)
	}
	if _, err := b.Bind("mat", impl.Slot{Name: "", Texture: "rock", Sampler: good}); !errors.Is(err, impl.ErrEmptySlotName) {
		t.Fatalf("empty slot name: got %v, want ErrEmptySlotName", err)
	}
	if _, err := b.Bind("mat", impl.Slot{Name: "base", Texture: "", Sampler: good}); !errors.Is(err, impl.ErrEmptyTexture) {
		t.Fatalf("empty texture: got %v, want ErrEmptyTexture", err)
	}
	badFilter := good
	badFilter.Filter = "cubic"
	if _, err := b.Bind("mat", impl.Slot{Name: "base", Texture: "rock", Sampler: badFilter}); !errors.Is(err, impl.ErrBadSampler) {
		t.Fatalf("bad filter: got %v, want ErrBadSampler", err)
	}
	badAniso := good
	badAniso.Anisotropy = 0
	if _, err := b.Bind("mat", impl.Slot{Name: "base", Texture: "rock", Sampler: badAniso}); !errors.Is(err, impl.ErrBadSampler) {
		t.Fatalf("bad anisotropy: got %v, want ErrBadSampler", err)
	}

	slot, err := b.Bind("mat", impl.Slot{Name: "base", Texture: "rock", Sampler: good})
	if err != nil {
		t.Fatalf("valid bind returned error: %v", err)
	}
	if slot != 0 {
		t.Fatalf("first physical slot = %d, want 0", slot)
	}
}

func TestSameTextureAndSamplerSharesPhysicalSlot(t *testing.T) {
	b := impl.NewTextureSlotBinder("default_albedo", 8)
	s := sampler()
	a := impl.Slot{Name: "base", Texture: "rock", Sampler: s}
	c := impl.Slot{Name: "base", Texture: "rock", Sampler: s}

	sa, err := b.Bind("mat_a", a)
	if err != nil {
		t.Fatalf("bind mat_a: %v", err)
	}
	sb, err := b.Bind("mat_b", c)
	if err != nil {
		t.Fatalf("bind mat_b: %v", err)
	}
	if sa != sb {
		t.Fatalf("identical texture and sampler took slots %d and %d, want the same slot", sa, sb)
	}
	if got := b.RefCount(sa); got != 2 {
		t.Fatalf("refcount of slot %d = %d, want 2", sa, got)
	}
	if got := b.SlotCount(); got != 1 {
		t.Fatalf("live slot count = %d, want 1", got)
	}
	if got := b.Stats().LiveBindings; got != 2 {
		t.Fatalf("live bindings = %d, want 2", got)
	}
}

func TestSameTextureDifferentSamplerUsesSeparateSlot(t *testing.T) {
	b := impl.NewTextureSlotBinder("default_albedo", 8)
	a := impl.Slot{Name: "base", Texture: "rock", Sampler: sampler()}
	clamp := sampler()
	clamp.WrapU = "clamp"
	c := impl.Slot{Name: "base", Texture: "rock", Sampler: clamp}

	sa, err := b.Bind("mat_a", a)
	if err != nil {
		t.Fatalf("bind mat_a: %v", err)
	}
	sb, err := b.Bind("mat_b", c)
	if err != nil {
		t.Fatalf("bind mat_b: %v", err)
	}
	if sa == sb {
		t.Fatalf("different wrap modes shared slot %d, want separate slots", sa)
	}
	if got := b.SlotCount(); got != 2 {
		t.Fatalf("live slot count = %d, want 2", got)
	}
	if b.RefCount(sa) != 1 || b.RefCount(sb) != 1 {
		t.Fatalf("refcounts = (%d,%d), want (1,1)", b.RefCount(sa), b.RefCount(sb))
	}
}

func TestDistinctTexturesTakeAscendingSlots(t *testing.T) {
	b := impl.NewTextureSlotBinder("default_albedo", 8)
	for i, tex := range []string{"tex_a", "tex_b", "tex_c"} {
		slot, err := b.Bind("mat", impl.Slot{Name: fmt.Sprintf("slot_%d", i), Texture: tex, Sampler: sampler()})
		if err != nil {
			t.Fatalf("bind %s: %v", tex, err)
		}
		if slot != i {
			t.Fatalf("bind %s returned slot %d, want %d", tex, slot, i)
		}
	}
	if got := b.SlotCount(); got != 3 {
		t.Fatalf("live slot count = %d, want 3", got)
	}
}

func TestUnbindReleasesOneReference(t *testing.T) {
	b := impl.NewTextureSlotBinder("default_albedo", 8)
	s := impl.Slot{Name: "base", Texture: "rock", Sampler: sampler()}
	if _, err := b.Bind("mat_a", s); err != nil {
		t.Fatalf("bind mat_a: %v", err)
	}
	if _, err := b.Bind("mat_b", s); err != nil {
		t.Fatalf("bind mat_b: %v", err)
	}

	if err := b.Unbind("mat_a", 0); err != nil {
		t.Fatalf("unbind mat_a: %v", err)
	}
	if got := b.RefCount(0); got != 1 {
		t.Fatalf("refcount after first unbind = %d, want 1", got)
	}
	slots, err := b.Resolve("mat_a")
	if err != nil {
		t.Fatalf("resolve mat_a: %v", err)
	}
	if len(slots) != 0 {
		t.Fatalf("mat_a still resolves %d slots after unbind", len(slots))
	}
	if err := b.Unbind("mat_a", 0); !errors.Is(err, impl.ErrSlotNotBound) {
		t.Fatalf("second unbind of mat_a: got %v, want ErrSlotNotBound", err)
	}
	if err := b.Unbind("mat_b", 0); err != nil {
		t.Fatalf("unbind mat_b: %v", err)
	}
	if got := b.SlotCount(); got != 0 {
		t.Fatalf("live slot count after all unbinds = %d, want 0", got)
	}
}

func TestUnbindUnknownSlotIsRejected(t *testing.T) {
	b := impl.NewTextureSlotBinder("default_albedo", 8)
	if _, err := b.Bind("mat", impl.Slot{Name: "base", Texture: "rock", Sampler: sampler()}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if err := b.Unbind("mat", 7); !errors.Is(err, impl.ErrSlotNotBound) {
		t.Fatalf("unbind missing slot: got %v, want ErrSlotNotBound", err)
	}
	if err := b.Unbind("mat", 0); err != nil {
		t.Fatalf("unbind existing slot: %v", err)
	}
	slots, err := b.Resolve("mat")
	if err != nil {
		t.Fatalf("resolve after unbind: %v", err)
	}
	if len(slots) != 0 {
		t.Fatalf("material still has %d bindings after unbind", len(slots))
	}
}

func TestResolveOrdersByPhysicalSlot(t *testing.T) {
	b := impl.NewTextureSlotBinder("default_albedo", 8)
	inputs := []impl.Slot{
		{Name: "normal", Texture: "tex_normal", Sampler: sampler()},
		{Name: "base", Texture: "tex_base", Sampler: sampler()},
		{Name: "extra", Texture: "tex_extra", Sampler: sampler()},
	}
	for _, s := range inputs {
		if _, err := b.Bind("mat", s); err != nil {
			t.Fatalf("bind %s: %v", s.Name, err)
		}
	}

	got, err := b.Resolve("mat")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("resolved %d bindings, want 3", len(got))
	}
	for i, s := range inputs {
		if got[i].Name != s.Name {
			t.Fatalf("binding %d name = %q, want %q", i, got[i].Name, s.Name)
		}
		if got[i].Slot != i {
			t.Fatalf("binding %d slot = %d, want %d", i, got[i].Slot, i)
		}
		if got[i].Texture != s.Texture {
			t.Fatalf("binding %d texture = %q, want %q", i, got[i].Texture, s.Texture)
		}
		if !reflect.DeepEqual(got[i].Sampler, s.Sampler) {
			t.Fatalf("binding %d sampler = %+v, want %+v", i, got[i].Sampler, s.Sampler)
		}
		if got[i].Degraded {
			t.Fatalf("binding %d unexpectedly degraded", i)
		}
		if got[i].Generation != 0 {
			t.Fatalf("binding %d generation = %d, want 0", i, got[i].Generation)
		}
	}
}

func TestResolveUnknownMaterialReturnsEmpty(t *testing.T) {
	b := impl.NewTextureSlotBinder("default_albedo", 8)
	got, err := b.Resolve("ghost")
	if err != nil {
		t.Fatalf("resolve unknown material: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("unknown material resolved %d bindings, want 0", len(got))
	}
	if _, ok := b.ResolveAll()["ghost"]; ok {
		t.Fatalf("ResolveAll reported an entry for an unbound material")
	}
	if b.Degraded("ghost") {
		t.Fatalf("unknown material reported degraded")
	}
}

func TestSlotExhaustionFallsBackToDefaultTexture(t *testing.T) {
	b := impl.NewTextureSlotBinder("default_albedo", 1)
	if _, err := b.Bind("mat_a", impl.Slot{Name: "base", Texture: "tex_a", Sampler: sampler()}); err != nil {
		t.Fatalf("bind mat_a: %v", err)
	}
	clamp := sampler()
	clamp.WrapV = "clamp"
	slot, err := b.Bind("mat_b", impl.Slot{Name: "base", Texture: "tex_b", Sampler: clamp})
	if err != nil {
		t.Fatalf("exhausted bind returned error: %v", err)
	}
	if slot != -1 {
		t.Fatalf("degraded bind returned slot %d, want -1", slot)
	}
	if !b.Degraded("mat_b") {
		t.Fatalf("mat_b not marked degraded after slot exhaustion")
	}
	got, err := b.Resolve("mat_b")
	if err != nil {
		t.Fatalf("resolve mat_b: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("degraded material resolved %d slots, want 1", len(got))
	}
	if got[0].Texture != "default_albedo" {
		t.Fatalf("degraded texture = %q, want default_albedo", got[0].Texture)
	}
	if !got[0].Degraded || got[0].Slot != -1 {
		t.Fatalf("degraded binding = %+v, want Degraded=true Slot=-1", got[0])
	}
	if !reflect.DeepEqual(got[0].Sampler, clamp) {
		t.Fatalf("degraded binding lost sampler state: %+v", got[0].Sampler)
	}
	if b.RefCount(-1) != 0 {
		t.Fatalf("refcount of the fallback slot = %d, want 0", b.RefCount(-1))
	}
	if b.SlotCount() != 1 {
		t.Fatalf("live slot count = %d, want 1", b.SlotCount())
	}
	if b.Stats().DegradedMaterials != 1 {
		t.Fatalf("degraded material count = %d, want 1", b.Stats().DegradedMaterials)
	}
}

func TestSetSlotLimitValidatesLiveCount(t *testing.T) {
	b := impl.NewTextureSlotBinder("default_albedo", 8)
	if err := b.SetSlotLimit(0); !errors.Is(err, impl.ErrBadSlotLimit) {
		t.Fatalf("zero slot limit: got %v, want ErrBadSlotLimit", err)
	}
	if _, err := b.Bind("mat_a", impl.Slot{Name: "base", Texture: "tex_a", Sampler: sampler()}); err != nil {
		t.Fatalf("bind mat_a: %v", err)
	}
	if _, err := b.Bind("mat_b", impl.Slot{Name: "base", Texture: "tex_b", Sampler: sampler()}); err != nil {
		t.Fatalf("bind mat_b: %v", err)
	}
	if err := b.SetSlotLimit(1); !errors.Is(err, impl.ErrSlotLimitBelowLive) {
		t.Fatalf("lowering limit below live slots: got %v, want ErrSlotLimitBelowLive", err)
	}
	if err := b.SetSlotLimit(4); err != nil {
		t.Fatalf("raising slot limit: %v", err)
	}
	slot, err := b.Bind("mat_c", impl.Slot{Name: "base", Texture: "tex_c", Sampler: sampler()})
	if err != nil {
		t.Fatalf("bind mat_c: %v", err)
	}
	if slot != 2 {
		t.Fatalf("third binding took slot %d, want 2", slot)
	}
}

func TestMaterialSamplerLimitIsEnforced(t *testing.T) {
	b := impl.NewTextureSlotBinder("default_albedo", 8)
	if err := b.SetMaterialLimit(1); err != nil {
		t.Fatalf("set material limit: %v", err)
	}
	if _, err := b.Bind("mat", impl.Slot{Name: "base", Texture: "tex_a", Sampler: sampler()}); err != nil {
		t.Fatalf("bind base: %v", err)
	}
	if _, err := b.Bind("mat", impl.Slot{Name: "normal", Texture: "tex_b", Sampler: sampler()}); !errors.Is(err, impl.ErrMaterialLimit) {
		t.Fatalf("second slot on limited material: got %v, want ErrMaterialLimit", err)
	}
	got, err := b.Resolve("mat")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(got) != 1 || got[0].Name != "base" {
		t.Fatalf("material bindings after rejected bind = %+v, want only base", got)
	}
	if err := b.SetMaterialLimit(0); !errors.Is(err, impl.ErrBadMaterialLimit) {
		t.Fatalf("zero material limit: got %v, want ErrBadMaterialLimit", err)
	}
}

func TestReloadMaterialInvalidatesAllBindings(t *testing.T) {
	b := impl.NewTextureSlotBinder("default_albedo", 8)
	if _, err := b.Bind("mat", impl.Slot{Name: "base", Texture: "tex_a", Sampler: sampler()}); err != nil {
		t.Fatalf("bind base: %v", err)
	}
	if _, err := b.Bind("mat", impl.Slot{Name: "normal", Texture: "tex_b", Sampler: sampler()}); err != nil {
		t.Fatalf("bind normal: %v", err)
	}
	if got := b.Generation("mat"); got != 0 {
		t.Fatalf("initial generation = %d, want 0", got)
	}
	if err := b.ReloadMaterial("mat"); err != nil {
		t.Fatalf("reload material: %v", err)
	}
	if got := b.Generation("mat"); got != 1 {
		t.Fatalf("generation after reload = %d, want 1", got)
	}
	got, err := b.Resolve("mat")
	if err != nil {
		t.Fatalf("resolve after reload: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("material still resolves %d bindings after reload", len(got))
	}
	if b.Degraded("mat") {
		t.Fatalf("material still degraded after reload")
	}
	if b.SlotCount() != 0 {
		t.Fatalf("live slot count after reload = %d, want 0", b.SlotCount())
	}
	if st := b.Stats(); st.PhysicalSlots != 0 || st.LiveBindings != 0 {
		t.Fatalf("stats after reload = %+v, want no live slots or bindings", st)
	}
	if err := b.ReloadMaterial("mat"); err != nil {
		t.Fatalf("second reload: %v", err)
	}
	if got := b.Generation("mat"); got != 2 {
		t.Fatalf("generation after second reload = %d, want 2", got)
	}
}

func TestReloadReleasesSharedSlotReferences(t *testing.T) {
	b := impl.NewTextureSlotBinder("default_albedo", 8)
	s := impl.Slot{Name: "base", Texture: "shared", Sampler: sampler()}
	if _, err := b.Bind("mat_a", s); err != nil {
		t.Fatalf("bind mat_a: %v", err)
	}
	if _, err := b.Bind("mat_b", s); err != nil {
		t.Fatalf("bind mat_b: %v", err)
	}

	if err := b.ReloadMaterial("mat_a"); err != nil {
		t.Fatalf("reload mat_a: %v", err)
	}
	if got := b.RefCount(0); got != 1 {
		t.Fatalf("refcount after reloading one of two materials = %d, want 1", got)
	}
	if got := b.SlotCount(); got != 1 {
		t.Fatalf("live slot count = %d, want 1 while mat_b still binds the slot", got)
	}
	slots, err := b.Resolve("mat_b")
	if err != nil {
		t.Fatalf("resolve mat_b: %v", err)
	}
	if len(slots) != 1 {
		t.Fatalf("mat_b resolves %d slots, want 1", len(slots))
	}

	if err := b.ReloadMaterial("mat_b"); err != nil {
		t.Fatalf("reload mat_b: %v", err)
	}
	if got := b.RefCount(0); got != 0 {
		t.Fatalf("refcount after both reloads = %d, want 0", got)
	}
	if got := b.SlotCount(); got != 0 {
		t.Fatalf("live slot count after both reloads = %d, want 0", got)
	}
}

func TestStatsAndResolveAllReflectLiveState(t *testing.T) {
	b := impl.NewTextureSlotBinder("default_albedo", 2)
	if _, err := b.Bind("mat_a", impl.Slot{Name: "base", Texture: "shared", Sampler: sampler()}); err != nil {
		t.Fatalf("bind mat_a: %v", err)
	}
	if _, err := b.Bind("mat_b", impl.Slot{Name: "base", Texture: "shared", Sampler: sampler()}); err != nil {
		t.Fatalf("bind mat_b: %v", err)
	}
	if _, err := b.Bind("mat_c", impl.Slot{Name: "base", Texture: "other", Sampler: sampler()}); err != nil {
		t.Fatalf("bind mat_c: %v", err)
	}
	slot, err := b.Bind("mat_d", impl.Slot{Name: "base", Texture: "third", Sampler: sampler()})
	if err != nil {
		t.Fatalf("bind mat_d: %v", err)
	}
	if slot != -1 {
		t.Fatalf("exhausted bind returned slot %d, want -1", slot)
	}

	st := b.Stats()
	if st.PhysicalSlots != 2 {
		t.Fatalf("physical slots = %d, want 2", st.PhysicalSlots)
	}
	if st.LiveBindings != 4 {
		t.Fatalf("live bindings = %d, want 4", st.LiveBindings)
	}
	if st.DegradedMaterials != 1 {
		t.Fatalf("degraded materials = %d, want 1", st.DegradedMaterials)
	}
	all := b.ResolveAll()
	if len(all) != 4 {
		t.Fatalf("ResolveAll returned %d materials, want 4", len(all))
	}
	for _, mat := range []string{"mat_a", "mat_b", "mat_c", "mat_d"} {
		if len(all[mat]) != 1 {
			t.Fatalf("ResolveAll[%q] has %d bindings, want 1", mat, len(all[mat]))
		}
	}
	if b.Degraded("mat_d") != true || b.Degraded("mat_c") != false {
		t.Fatalf("degraded flags: mat_d=%v mat_c=%v, want true and false", b.Degraded("mat_d"), b.Degraded("mat_c"))
	}
}

func TestConcurrentBindUnbindKeepsRefCountsSane(t *testing.T) {
	b := impl.NewTextureSlotBinder("default_albedo", 4)
	const workers = 8
	const rounds = 25

	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			mat := fmt.Sprintf("mat-%d", w)
			tex := fmt.Sprintf("tex-%d", w)
			for i := 0; i < rounds; i++ {
				slot, err := b.Bind(mat, impl.Slot{Name: "base", Texture: tex, Sampler: sampler()})
				if err != nil {
					errCh <- fmt.Errorf("bind %s: %w", mat, err)
					return
				}
				if slot < 0 {
					continue
				}
				if err := b.Unbind(mat, slot); err != nil {
					errCh <- fmt.Errorf("unbind %s slot %d: %w", mat, slot, err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent bind/unbind failed: %v", err)
	}
	if got := b.SlotCount(); got != 0 {
		t.Fatalf("live slot count after concurrent bind/unbind = %d, want 0", got)
	}
	if got := b.Stats().LiveBindings; got != 0 {
		t.Fatalf("live bindings after concurrent bind/unbind = %d, want 0", got)
	}
	for slot := 0; slot < 4; slot++ {
		if rc := b.RefCount(slot); rc < 0 {
			t.Fatalf("refcount of slot %d went negative: %d", slot, rc)
		}
	}
}
