package task

import (
	"errors"
	"sync"
	"testing"

	impl "gsb/batch48-go-game-b-10"
)

func params16f(scale float64) impl.Params {
	return impl.Params{Scale: scale, In: impl.FormatRGBA16F, Out: impl.FormatRGBA16F}
}

func enableDefaults(t *testing.T, c *impl.PostFXChainCompiler, effects ...impl.Effect) {
	t.Helper()
	for _, e := range effects {
		if err := c.Enable(e, params16f(1)); err != nil {
			t.Fatalf("Enable(%s) returned unexpected error: %v", e, err)
		}
	}
}

func effectOrder(chain impl.Chain) []impl.Effect {
	var out []impl.Effect
	for _, p := range chain.Passes {
		if p.Kind == impl.PassEffect {
			out = append(out, p.Effect)
		}
	}
	return out
}

func TestEnableRejectsUnknownEffect(t *testing.T) {
	c := impl.NewPostFXChainCompiler()
	if err := c.Enable(impl.Effect("sharpen"), params16f(1)); !errors.Is(err, impl.ErrUnknownEffect) {
		t.Fatalf("Enable(sharpen): got %v, want ErrUnknownEffect", err)
	}
	if got := len(c.Enabled()); got != 0 {
		t.Fatalf("Enabled holds %d effects after a rejected enable, want 0", got)
	}
}

func TestEnableRejectsBadScaleAndFormat(t *testing.T) {
	c := impl.NewPostFXChainCompiler()
	if err := c.Enable(impl.EffectBloom, params16f(0)); !errors.Is(err, impl.ErrBadScale) {
		t.Fatalf("Enable(scale 0): got %v, want ErrBadScale", err)
	}
	if err := c.Enable(impl.EffectBloom, params16f(1.5)); !errors.Is(err, impl.ErrBadScale) {
		t.Fatalf("Enable(scale 1.5): got %v, want ErrBadScale", err)
	}
	bad := impl.Params{Scale: 1, In: "rgb24", Out: impl.FormatRGBA16F}
	if err := c.Enable(impl.EffectBloom, bad); !errors.Is(err, impl.ErrBadFormat) {
		t.Fatalf("Enable(bad format): got %v, want ErrBadFormat", err)
	}
}

func TestEnableIsIdempotentAndUpdatesParams(t *testing.T) {
	c := impl.NewPostFXChainCompiler()
	if err := c.Enable(impl.EffectBloom, params16f(0.5)); err != nil {
		t.Fatalf("first Enable returned unexpected error: %v", err)
	}
	if err := c.Enable(impl.EffectBloom, params16f(0.25)); err != nil {
		t.Fatalf("second Enable returned unexpected error: %v", err)
	}
	enabled := c.Enabled()
	if len(enabled) != 1 || enabled[0] != impl.EffectBloom {
		t.Fatalf("Enabled = %v, want exactly [bloom]", enabled)
	}
	got, ok := c.ParamsOf(impl.EffectBloom)
	if !ok {
		t.Fatalf("ParamsOf(bloom) missing after Enable")
	}
	if got.Scale != 0.25 {
		t.Fatalf("ParamsOf(bloom).Scale = %v, want the latest 0.25", got.Scale)
	}
}

func TestDisableRemovesEffect(t *testing.T) {
	c := impl.NewPostFXChainCompiler()
	enableDefaults(t, c, impl.EffectBloom)
	if err := c.Disable(impl.EffectBloom); err != nil {
		t.Fatalf("Disable(bloom) returned unexpected error: %v", err)
	}
	if got := len(c.Enabled()); got != 0 {
		t.Fatalf("Enabled holds %d effects after Disable, want 0", got)
	}
	if err := c.Disable(impl.EffectBloom); !errors.Is(err, impl.ErrNotEnabled) {
		t.Fatalf("second Disable: got %v, want ErrNotEnabled", err)
	}
}

func TestCompileOrdersPassesByFixedPriority(t *testing.T) {
	c := impl.NewPostFXChainCompiler()
	enableDefaults(t, c, impl.EffectTonemap, impl.EffectFXAA, impl.EffectBloom)
	chain, err := c.Compile(1280, 720)
	if err != nil {
		t.Fatalf("Compile returned unexpected error: %v", err)
	}
	want := []impl.Effect{impl.EffectBloom, impl.EffectTonemap, impl.EffectFXAA}
	got := effectOrder(chain)
	if len(got) != len(want) {
		t.Fatalf("chain carries %d effect passes, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pass %d is %s, want %s in fixed priority order", i, got[i], want[i])
		}
	}
}

func TestCompileInsertsConversionPassOnFormatMismatch(t *testing.T) {
	c := impl.NewPostFXChainCompiler()
	ssao := impl.Params{Scale: 1, In: impl.FormatRGBA16F, Out: impl.FormatRGBA8}
	if err := c.Enable(impl.EffectSSAO, ssao); err != nil {
		t.Fatalf("Enable(ssao) returned unexpected error: %v", err)
	}
	if err := c.Enable(impl.EffectBloom, params16f(1)); err != nil {
		t.Fatalf("Enable(bloom) returned unexpected error: %v", err)
	}
	chain, err := c.Compile(800, 600)
	if err != nil {
		t.Fatalf("Compile returned unexpected error: %v", err)
	}
	if len(chain.Passes) != 3 {
		t.Fatalf("chain holds %d passes, want ssao + convert + bloom", len(chain.Passes))
	}
	conv := chain.Passes[1]
	if conv.Kind != impl.PassConvert {
		t.Fatalf("pass 1 kind = %q, want %q", conv.Kind, impl.PassConvert)
	}
	if conv.Input != impl.FormatRGBA8 || conv.Output != impl.FormatRGBA16F {
		t.Fatalf("convert pass goes %q -> %q, want rgba8 -> rgba16f", conv.Input, conv.Output)
	}
}

func TestCompileRejectsNonPositiveSize(t *testing.T) {
	c := impl.NewPostFXChainCompiler()
	enableDefaults(t, c, impl.EffectBloom)
	if _, err := c.Compile(0, 720); !errors.Is(err, impl.ErrBadSize) {
		t.Fatalf("Compile(0,720): got %v, want ErrBadSize", err)
	}
	if _, err := c.Compile(1280, -1); !errors.Is(err, impl.ErrBadSize) {
		t.Fatalf("Compile(1280,-1): got %v, want ErrBadSize", err)
	}
}

func TestCompileRejectsEmptyChain(t *testing.T) {
	c := impl.NewPostFXChainCompiler()
	if _, err := c.Compile(640, 360); !errors.Is(err, impl.ErrEmptyChain) {
		t.Fatalf("Compile with no effects: got %v, want ErrEmptyChain", err)
	}
}

func TestCompileUsesPingPongTemporaries(t *testing.T) {
	c := impl.NewPostFXChainCompiler()
	enableDefaults(t, c, impl.EffectSSAO, impl.EffectBloom, impl.EffectTonemap)
	chain, err := c.Compile(1920, 1080)
	if err != nil {
		t.Fatalf("Compile returned unexpected error: %v", err)
	}
	if len(chain.Passes) != 3 {
		t.Fatalf("chain holds %d passes, want 3", len(chain.Passes))
	}
	if len(chain.Targets) != 2 {
		t.Fatalf("chain allocates %d targets, want exactly 2 ping-pong temporaries", len(chain.Targets))
	}
	if chain.Passes[0].Input != "external" {
		t.Fatalf("first pass input = %q, want external", chain.Passes[0].Input)
	}
	if chain.Passes[2].Output != "external" {
		t.Fatalf("last pass output = %q, want external", chain.Passes[2].Output)
	}
	if chain.Passes[0].Output == chain.Passes[1].Output {
		t.Fatalf("adjacent passes write the same target %q", chain.Passes[0].Output)
	}
}

func TestCompileAppliesPerEffectScale(t *testing.T) {
	c := impl.NewPostFXChainCompiler()
	if err := c.Enable(impl.EffectSSAO, params16f(0.5)); err != nil {
		t.Fatalf("Enable(ssao) returned unexpected error: %v", err)
	}
	if err := c.Enable(impl.EffectBloom, params16f(1)); err != nil {
		t.Fatalf("Enable(bloom) returned unexpected error: %v", err)
	}
	chain, err := c.Compile(1920, 1080)
	if err != nil {
		t.Fatalf("Compile returned unexpected error: %v", err)
	}
	if chain.Passes[0].Width != 960 || chain.Passes[0].Height != 540 {
		t.Fatalf("ssao pass = %dx%d, want 960x540 at scale 0.5", chain.Passes[0].Width, chain.Passes[0].Height)
	}
	if chain.Passes[1].Width != 1920 || chain.Passes[1].Height != 1080 {
		t.Fatalf("bloom pass = %dx%d, want 1920x1080 at scale 1", chain.Passes[1].Width, chain.Passes[1].Height)
	}
}

func TestCompileOverPassLimitKeepsLastChain(t *testing.T) {
	c := impl.NewPostFXChainCompiler()
	enableDefaults(t, c, impl.EffectBloom, impl.EffectTonemap)
	first, err := c.Compile(640, 360)
	if err != nil {
		t.Fatalf("first Compile returned unexpected error: %v", err)
	}
	if err := c.SetMaxPasses(2); err != nil {
		t.Fatalf("SetMaxPasses(2) returned unexpected error: %v", err)
	}
	enableDefaults(t, c, impl.EffectFXAA)
	if _, err := c.Compile(640, 360); !errors.Is(err, impl.ErrPassLimit) {
		t.Fatalf("Compile with three effects and a limit of two: got %v, want ErrPassLimit", err)
	}
	kept, ok := c.LastChain()
	if !ok {
		t.Fatalf("LastChain missing after a rejected Compile")
	}
	if len(kept.Passes) != len(first.Passes) {
		t.Fatalf("LastChain holds %d passes, want the previous %d", len(kept.Passes), len(first.Passes))
	}
}

func TestSnapshotRestoreKeepsEffectsAndParams(t *testing.T) {
	c := impl.NewPostFXChainCompiler()
	enableDefaults(t, c, impl.EffectBloom, impl.EffectSSAO)
	if err := c.Enable(impl.EffectSSAO, params16f(0.5)); err != nil {
		t.Fatalf("Enable(ssao) returned unexpected error: %v", err)
	}
	if _, err := c.Compile(800, 600); err != nil {
		t.Fatalf("Compile returned unexpected error: %v", err)
	}
	payload, err := c.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot returned unexpected error: %v", err)
	}
	fresh := impl.NewPostFXChainCompiler()
	if err := fresh.Restore(payload); err != nil {
		t.Fatalf("Restore returned unexpected error: %v", err)
	}
	if got := len(fresh.Enabled()); got != 2 {
		t.Fatalf("Enabled after Restore holds %d effects, want 2", got)
	}
	got, ok := fresh.ParamsOf(impl.EffectSSAO)
	if !ok {
		t.Fatalf("ParamsOf(ssao) missing after Restore")
	}
	if got.Scale != 0.5 {
		t.Fatalf("ParamsOf(ssao).Scale after Restore = %v, want 0.5", got.Scale)
	}
	if _, ok := fresh.LastChain(); !ok {
		t.Fatalf("LastChain missing after Restore")
	}
}

func TestRestoreRejectsCorruptPayload(t *testing.T) {
	c := impl.NewPostFXChainCompiler()
	enableDefaults(t, c, impl.EffectBloom)
	if err := c.Restore([]byte("garbage")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("Restore(garbage): got %v, want ErrBadSnapshot", err)
	}
	if got := len(c.Enabled()); got != 1 {
		t.Fatalf("Enabled after rejected Restore holds %d effects, want 1", got)
	}
}

func TestHistoryEffectAllocatesDoubleTargets(t *testing.T) {
	c := impl.NewPostFXChainCompiler()
	mb := impl.Params{Scale: 1, In: impl.FormatRGBA16F, Out: impl.FormatRGBA16F, History: true}
	if err := c.Enable(impl.EffectMotionBlur, mb); err != nil {
		t.Fatalf("Enable(motion_blur) returned unexpected error: %v", err)
	}
	if err := c.Enable(impl.EffectBloom, params16f(1)); err != nil {
		t.Fatalf("Enable(bloom) returned unexpected error: %v", err)
	}
	chain, err := c.Compile(640, 360)
	if err != nil {
		t.Fatalf("Compile returned unexpected error: %v", err)
	}
	history := 0
	for _, target := range chain.Targets {
		if target.Kind == impl.TargetHistory {
			history++
		}
	}
	if history != 2 {
		t.Fatalf("chain allocates %d history targets for motion blur, want a double buffer of 2", history)
	}
}

func TestConcurrentEnableDisableAndCompile(t *testing.T) {
	c := impl.NewPostFXChainCompiler()
	effects := []impl.Effect{impl.EffectSSAO, impl.EffectBloom, impl.EffectTonemap, impl.EffectFXAA}
	var wg sync.WaitGroup
	for _, e := range effects {
		e := e
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := c.Enable(e, params16f(1)); err != nil {
				t.Errorf("concurrent Enable(%s) returned error: %v", e, err)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 4; i++ {
			_, _ = c.Compile(640, 360)
		}
	}()
	wg.Wait()
	if got := len(c.Enabled()); got != len(effects) {
		t.Fatalf("Enabled holds %d effects, want %d", got, len(effects))
	}
	chain, err := c.Compile(640, 360)
	if err != nil {
		t.Fatalf("final Compile returned unexpected error: %v", err)
	}
	if got := len(effectOrder(chain)); got != len(effects) {
		t.Fatalf("final chain carries %d effect passes, want %d", got, len(effects))
	}
}
