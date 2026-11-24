package task

import (
	"errors"
	"math"
	"testing"

	impl "gsb/batch48-go-game-b-22"
)

func effect22(name string, need impl.ResolutionNeed, enabled bool) impl.Effect {
	return impl.Effect{Name: name, Need: need, Enabled: enabled}
}

func TestSetScaleRejectsOutOfRange(t *testing.T) {
	p := impl.NewPostFxResolutionPlanner(4)
	for _, scale := range []float64{0, -0.5, 1.5, math.NaN()} {
		if err := p.SetScale(scale); !errors.Is(err, impl.ErrBadScale) {
			t.Fatalf("scale %v: got %v, want ErrBadScale", scale, err)
		}
	}
	if got := p.Scale(); got != 1 {
		t.Fatalf("rejected scales must not be installed, scale=%v", got)
	}
}

func TestSetScaleBumpsGeneration(t *testing.T) {
	p := impl.NewPostFxResolutionPlanner(4)
	before := p.Generation()
	if err := p.SetScale(0.5); err != nil {
		t.Fatalf("set scale failed: %v", err)
	}
	if got := p.Scale(); got != 0.5 {
		t.Fatalf("scale = %v, want 0.5", got)
	}
	if got := p.Generation(); got != before+1 {
		t.Fatalf("generation after one scale change = %d, want %d", got, before+1)
	}
}

func TestClassifyForcedNeedIsNativeAtAnyScale(t *testing.T) {
	p := impl.NewPostFxResolutionPlanner(4)
	forced := effect22("grade", impl.NeedForced, true)
	if got := p.Classify(forced); got != impl.ClassNative {
		t.Fatalf("forced need at scale 1 classified as %v, want ClassNative", got)
	}
	if err := p.SetScale(0.5); err != nil {
		t.Fatalf("set scale failed: %v", err)
	}
	if got := p.Classify(forced); got != impl.ClassNative {
		t.Fatalf("forced need at scale 0.5 classified as %v, want ClassNative", got)
	}
}

func TestClassifyHalfNeedUnderLowScale(t *testing.T) {
	p := impl.NewPostFxResolutionPlanner(4)
	if err := p.SetScale(0.5); err != nil {
		t.Fatalf("set scale failed: %v", err)
	}
	if got := p.Classify(effect22("bloom", impl.NeedHalf, true)); got != impl.ClassHalf {
		t.Fatalf("half need classified as %v, want ClassHalf", got)
	}
	if got := p.Classify(effect22("ssao", impl.NeedNative, true)); got != impl.ClassNative {
		t.Fatalf("native need classified as %v, want ClassNative", got)
	}
}

func TestScaleOneKeepsAllPassesNative(t *testing.T) {
	p := impl.NewPostFxResolutionPlanner(4)
	if err := p.SetScale(1.0); err != nil {
		t.Fatalf("set scale failed: %v", err)
	}
	if err := p.SetChain([]impl.Effect{
		effect22("bloom", impl.NeedHalf, true),
		effect22("ssao", impl.NeedNative, true),
	}); err != nil {
		t.Fatalf("set chain failed: %v", err)
	}
	passes, err := p.Plan(800, 600)
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	if len(passes) != 2 {
		t.Fatalf("planned %d passes at scale 1, want 2 effect passes", len(passes))
	}
	for i, name := range []string{"bloom", "ssao"} {
		if passes[i].Kind != impl.PassEffect {
			t.Fatalf("pass %d kind = %v, want PassEffect", i, passes[i].Kind)
		}
		if passes[i].Class != impl.ClassNative {
			t.Fatalf("pass %d class = %v, want ClassNative", i, passes[i].Class)
		}
		if passes[i].Width != 800 || passes[i].Height != 600 {
			t.Fatalf("pass %d size = %dx%d, want 800x600", i, passes[i].Width, passes[i].Height)
		}
		if passes[i].Effect != name {
			t.Fatalf("pass %d effect = %q, want %q", i, passes[i].Effect, name)
		}
	}
}

func TestPlanInsertsSingleUpscaleBeforeNativeEffect(t *testing.T) {
	p := impl.NewPostFxResolutionPlanner(4)
	if err := p.SetScale(0.5); err != nil {
		t.Fatalf("set scale failed: %v", err)
	}
	if err := p.SetChain([]impl.Effect{
		effect22("bloom", impl.NeedHalf, true),
		effect22("dof", impl.NeedHalf, true),
		effect22("tonemap", impl.NeedNative, true),
	}); err != nil {
		t.Fatalf("set chain failed: %v", err)
	}
	passes, err := p.Plan(800, 600)
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	if len(passes) != 4 {
		t.Fatalf("planned %d passes, want 4", len(passes))
	}
	kinds := []impl.PassKind{impl.PassEffect, impl.PassEffect, impl.PassUpscale, impl.PassEffect}
	names := []string{"bloom", "dof", "", "tonemap"}
	for i := range kinds {
		if passes[i].Kind != kinds[i] {
			t.Fatalf("pass %d kind = %v, want %v", i, passes[i].Kind, kinds[i])
		}
		if passes[i].Effect != names[i] {
			t.Fatalf("pass %d effect = %q, want %q", i, passes[i].Effect, names[i])
		}
	}
	if passes[2].Class != impl.ClassNative || passes[2].Width != 800 || passes[2].Height != 600 {
		t.Fatalf("upscale pass = %v %dx%d, want ClassNative 800x600", passes[2].Class, passes[2].Width, passes[2].Height)
	}
}

func TestPlanDownscaleBeforeHalfEffect(t *testing.T) {
	p := impl.NewPostFxResolutionPlanner(4)
	if err := p.SetScale(0.5); err != nil {
		t.Fatalf("set scale failed: %v", err)
	}
	if err := p.SetChain([]impl.Effect{
		effect22("ssao", impl.NeedNative, true),
		effect22("grain", impl.NeedHalf, true),
	}); err != nil {
		t.Fatalf("set chain failed: %v", err)
	}
	passes, err := p.Plan(960, 540)
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	if len(passes) != 4 {
		t.Fatalf("planned %d passes, want 4", len(passes))
	}
	kinds := []impl.PassKind{impl.PassUpscale, impl.PassEffect, impl.PassDownscale, impl.PassEffect}
	for i, want := range kinds {
		if passes[i].Kind != want {
			t.Fatalf("pass %d kind = %v, want %v", i, passes[i].Kind, want)
		}
	}
	if passes[2].Class != impl.ClassHalf || passes[2].Width != 480 || passes[2].Height != 270 {
		t.Fatalf("downscale pass = %v %dx%d, want ClassHalf 480x270", passes[2].Class, passes[2].Width, passes[2].Height)
	}
	if passes[3].Class != impl.ClassHalf || passes[3].Width != 480 || passes[3].Height != 270 {
		t.Fatalf("grain pass = %v %dx%d, want ClassHalf 480x270", passes[3].Class, passes[3].Width, passes[3].Height)
	}
}

func TestPlanHalfPassDimensionsFollowScale(t *testing.T) {
	p := impl.NewPostFxResolutionPlanner(4)
	if err := p.SetScale(0.5); err != nil {
		t.Fatalf("set scale failed: %v", err)
	}
	if err := p.SetChain([]impl.Effect{
		effect22("bloom", impl.NeedHalf, true),
	}); err != nil {
		t.Fatalf("set chain failed: %v", err)
	}
	passes, err := p.Plan(1919, 1079)
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	if len(passes) != 1 {
		t.Fatalf("planned %d passes, want 1", len(passes))
	}
	if passes[0].Width != 960 || passes[0].Height != 540 {
		t.Fatalf("half pass size = %dx%d, want the scale rounded to 960x540", passes[0].Width, passes[0].Height)
	}
}

func TestPlanSkipsDisabledEffects(t *testing.T) {
	p := impl.NewPostFxResolutionPlanner(4)
	if err := p.SetScale(0.5); err != nil {
		t.Fatalf("set scale failed: %v", err)
	}
	if err := p.SetChain([]impl.Effect{
		effect22("bloom", impl.NeedHalf, true),
		effect22("vignette", impl.NeedHalf, false),
		effect22("tonemap", impl.NeedNative, true),
	}); err != nil {
		t.Fatalf("set chain failed: %v", err)
	}
	passes, err := p.Plan(800, 600)
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	if len(passes) != 3 {
		t.Fatalf("planned %d passes, want 3 with the disabled effect skipped", len(passes))
	}
	for i, name := range []string{"bloom", "", "tonemap"} {
		if passes[i].Effect != name {
			t.Fatalf("pass %d effect = %q, want %q", i, passes[i].Effect, name)
		}
	}
}

func TestPlanRejectsNonPositiveSize(t *testing.T) {
	p := impl.NewPostFxResolutionPlanner(4)
	if err := p.SetChain([]impl.Effect{effect22("bloom", impl.NeedHalf, true)}); err != nil {
		t.Fatalf("set chain failed: %v", err)
	}
	if _, err := p.Plan(0, 600); !errors.Is(err, impl.ErrBadSize) {
		t.Fatalf("width 0: got %v, want ErrBadSize", err)
	}
	if _, err := p.Plan(800, -2); !errors.Is(err, impl.ErrBadSize) {
		t.Fatalf("height -2: got %v, want ErrBadSize", err)
	}
}

func TestPlanIsStableAcrossCalls(t *testing.T) {
	p := impl.NewPostFxResolutionPlanner(4)
	if err := p.SetScale(0.5); err != nil {
		t.Fatalf("set scale failed: %v", err)
	}
	if err := p.SetChain([]impl.Effect{
		effect22("bloom", impl.NeedHalf, true),
		effect22("tonemap", impl.NeedNative, true),
	}); err != nil {
		t.Fatalf("set chain failed: %v", err)
	}
	first, err := p.Plan(1280, 720)
	if err != nil {
		t.Fatalf("first plan failed: %v", err)
	}
	if len(first) != 3 {
		t.Fatalf("planned %d passes, want 3", len(first))
	}
	second, err := p.Plan(1280, 720)
	if err != nil {
		t.Fatalf("second plan failed: %v", err)
	}
	if len(second) != len(first) {
		t.Fatalf("second plan has %d passes, want %d", len(second), len(first))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("pass %d differs between identical plans: %+v vs %+v", i, first[i], second[i])
		}
	}
}

func TestPlanEnforcesPassLimit(t *testing.T) {
	p := impl.NewPostFxResolutionPlanner(4)
	if err := p.SetScale(0.5); err != nil {
		t.Fatalf("set scale failed: %v", err)
	}
	if err := p.SetChain([]impl.Effect{
		effect22("bloom", impl.NeedHalf, true),
		effect22("dof", impl.NeedHalf, true),
		effect22("grain", impl.NeedHalf, true),
	}); err != nil {
		t.Fatalf("set chain failed: %v", err)
	}
	if err := p.SetPassLimit(2); err != nil {
		t.Fatalf("set pass limit failed: %v", err)
	}
	if _, err := p.Plan(800, 600); !errors.Is(err, impl.ErrPassLimit) {
		t.Fatalf("3 passes above a limit of 2: got %v, want ErrPassLimit", err)
	}
	if err := p.SetPassLimit(0); !errors.Is(err, impl.ErrBadLimit) {
		t.Fatalf("limit 0: got %v, want ErrBadLimit", err)
	}
}

func TestAcquireTargetReusesSameKey(t *testing.T) {
	p := impl.NewPostFxResolutionPlanner(4)
	k := impl.TargetKey{Name: "hdr", Width: 960, Height: 540, Class: impl.ClassHalf}
	first, err := p.AcquireTarget(k)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	second, err := p.AcquireTarget(k)
	if err != nil {
		t.Fatalf("second acquire failed: %v", err)
	}
	if first <= 0 {
		t.Fatalf("target handle = %d, want a positive handle", first)
	}
	if second != first {
		t.Fatalf("equal target keys returned handles %d and %d, want the same handle", first, second)
	}
	if got := p.TargetCount(); got != 1 {
		t.Fatalf("target count = %d, want 1 physical target", got)
	}
	w, h, ok := p.TargetSize(first)
	if !ok || w != 960 || h != 540 {
		t.Fatalf("target size = %dx%d ok=%v, want 960x540 ok=true", w, h, ok)
	}
}

func TestAcquireTargetRejectsPoolOverflow(t *testing.T) {
	p := impl.NewPostFxResolutionPlanner(1)
	if _, err := p.AcquireTarget(impl.TargetKey{Name: "a", Width: 100, Height: 100, Class: impl.ClassNative}); err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	_, err := p.AcquireTarget(impl.TargetKey{Name: "b", Width: 100, Height: 100, Class: impl.ClassNative})
	if !errors.Is(err, impl.ErrTargetPoolFull) {
		t.Fatalf("second distinct target above a pool of 1: got %v, want ErrTargetPoolFull", err)
	}
}

func TestReleaseTargetRejectsSecondRelease(t *testing.T) {
	p := impl.NewPostFxResolutionPlanner(4)
	h, err := p.AcquireTarget(impl.TargetKey{Name: "ldr", Width: 320, Height: 180, Class: impl.ClassHalf})
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	if err := p.ReleaseTarget(h); err != nil {
		t.Fatalf("release failed: %v", err)
	}
	if got := p.TargetCount(); got != 0 {
		t.Fatalf("target count after release = %d, want 0", got)
	}
	if _, _, ok := p.TargetSize(h); ok {
		t.Fatalf("released handle must not report a size")
	}
	if err := p.ReleaseTarget(h); !errors.Is(err, impl.ErrUnknownTarget) {
		t.Fatalf("second release: got %v, want ErrUnknownTarget", err)
	}
}
