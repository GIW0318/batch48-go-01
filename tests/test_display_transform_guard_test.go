package task

import (
	"errors"
	"testing"

	impl "gsb/batch48-go-game-c-15"
)

func mustChain(t *testing.T, w, h, align int) *impl.TransformChain {
	t.Helper()
	c, err := impl.NewTransformChain(impl.Size{W: w, H: h}, align)
	if err != nil {
		t.Fatalf("build chain for %dx%d align %d: %v", w, h, align, err)
	}
	return c
}

func TestNormalizeMergesDoubleFlip(t *testing.T) {
	c := mustChain(t, 1920, 1080, 2)
	got, err := c.Normalize(impl.Transform{FlipH: true, FlipV: true})
	if err != nil {
		t.Fatalf("normalize double flip: %v", err)
	}
	if got.Rotation != 180 || got.FlipH || got.FlipV {
		t.Fatalf("double flip normalised to rotation %d flipH %v flipV %v, want 180/false/false", got.Rotation, got.FlipH, got.FlipV)
	}
}

func TestNormalizeWrapsRotation(t *testing.T) {
	c := mustChain(t, 1920, 1080, 2)
	for _, tc := range []struct {
		in   int
		want int
	}{{450, 90}, {-90, 270}, {720, 0}, {270, 270}} {
		got, err := c.Normalize(impl.Transform{Rotation: tc.in})
		if err != nil {
			t.Fatalf("normalize rotation %d: %v", tc.in, err)
		}
		if got.Rotation != tc.want {
			t.Fatalf("rotation %d normalised to %d, want %d", tc.in, got.Rotation, tc.want)
		}
	}
}

func TestSetTransformRejectsBadRotation(t *testing.T) {
	c := mustChain(t, 1920, 1080, 2)
	if err := c.SetTransform(impl.Transform{Rotation: 45}); !errors.Is(err, impl.ErrTransformRotation) {
		t.Fatalf("45 degree rotation returned %v, want ErrTransformRotation", err)
	}
	if got := c.Current(); got.Rotation != 0 {
		t.Fatalf("stored rotation = %d after rejection, want 0", got.Rotation)
	}
}

func TestSetTransformRejectsCropOutsideSource(t *testing.T) {
	c := mustChain(t, 1920, 1080, 2)
	crop := impl.Rect{X: 1900, Y: 0, W: 100, H: 100}
	if err := c.SetTransform(impl.Transform{Crop: crop}); !errors.Is(err, impl.ErrTransformCrop) {
		t.Fatalf("out of bounds crop returned %v, want ErrTransformCrop", err)
	}
	if got := c.Current().Crop; got != (impl.Rect{}) {
		t.Fatalf("stored crop = %+v after rejection, want zero rect", got)
	}
}

func TestOutputSizeSwapsForQuarterTurn(t *testing.T) {
	c := mustChain(t, 1920, 1080, 2)
	if err := c.SetTransform(impl.Transform{Rotation: 90}); err != nil {
		t.Fatalf("set rotation: %v", err)
	}
	got, err := c.OutputSize()
	if err != nil {
		t.Fatalf("output size: %v", err)
	}
	if got != (impl.Size{W: 1080, H: 1920}) {
		t.Fatalf("output size after 90 degree turn = %+v, want 1080x1920", got)
	}
}

func TestOutputSizeAlignsOddDimensions(t *testing.T) {
	c := mustChain(t, 1919, 1079, 2)
	got, err := c.OutputSize()
	if err != nil {
		t.Fatalf("output size: %v", err)
	}
	if got != (impl.Size{W: 1920, H: 1080}) {
		t.Fatalf("output size for odd source = %+v, want padded 1920x1080", got)
	}
}

func TestCropAlignedToChromaGrid(t *testing.T) {
	c := mustChain(t, 1920, 1080, 2)
	crop := impl.Rect{X: 3, Y: 5, W: 101, H: 99}
	res, err := c.Apply(impl.Transform{Crop: crop})
	if err != nil {
		t.Fatalf("apply aligned crop: %v", err)
	}
	if res.Crop != (impl.Rect{X: 2, Y: 4, W: 102, H: 100}) {
		t.Fatalf("aligned crop = %+v, want 2/4/102/100", res.Crop)
	}
	if res.Output != (impl.Size{W: 102, H: 100}) {
		t.Fatalf("output size = %+v, want 102x100", res.Output)
	}
	if !res.Aligned {
		t.Fatal("result did not report that alignment adjusted the crop")
	}
}

func TestCropConvertedIntoRotatedFrame(t *testing.T) {
	c := mustChain(t, 1920, 1080, 2)
	res, err := c.Apply(impl.Transform{Rotation: 90, Crop: impl.Rect{X: 10, Y: 20, W: 100, H: 50}})
	if err != nil {
		t.Fatalf("apply rotated crop: %v", err)
	}
	if res.Crop != (impl.Rect{X: 1010, Y: 10, W: 50, H: 100}) {
		t.Fatalf("rotated crop = %+v, want 1010/10/50/100", res.Crop)
	}
	if res.Output != (impl.Size{W: 50, H: 100}) {
		t.Fatalf("rotated output = %+v, want 50x100", res.Output)
	}
	if res.Crop.X+res.Crop.W > 1080 || res.Crop.Y+res.Crop.H > 1920 {
		t.Fatalf("rotated crop %+v leaves the rotated frame", res.Crop)
	}
}

func TestApplyIsIdempotent(t *testing.T) {
	c := mustChain(t, 1920, 1080, 2)
	tr := impl.Transform{Rotation: 270, FlipH: true, Crop: impl.Rect{X: 8, Y: 12, W: 300, H: 200}}
	first, err := c.Apply(tr)
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if first.Output != (impl.Size{W: 200, H: 300}) {
		t.Fatalf("first output = %+v, want 200x300", first.Output)
	}
	second, err := c.Apply(tr)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if second.Output != first.Output || second.Crop != first.Crop {
		t.Fatalf("second apply = %+v / %+v, want identical to %+v / %+v", second.Output, second.Crop, first.Output, first.Crop)
	}
}

func TestSetSourceSizeRejectsNonPositive(t *testing.T) {
	c := mustChain(t, 1920, 1080, 2)
	if err := c.SetSourceSize(impl.Size{W: 0, H: 1080}); !errors.Is(err, impl.ErrTransformSize) {
		t.Fatalf("zero width returned %v, want ErrTransformSize", err)
	}
	if err := c.SetSourceSize(impl.Size{W: 1920, H: -1}); !errors.Is(err, impl.ErrTransformSize) {
		t.Fatalf("negative height returned %v, want ErrTransformSize", err)
	}
	out, err := c.OutputSize()
	if err != nil {
		t.Fatalf("output size after rejected source size: %v", err)
	}
	if out != (impl.Size{W: 1920, H: 1080}) {
		t.Fatalf("output size after rejected source size = %+v, want original 1920x1080", out)
	}
}

func TestSetAlignRejectsUnsupportedValue(t *testing.T) {
	c := mustChain(t, 1920, 1080, 2)
	if err := c.SetAlign(3); !errors.Is(err, impl.ErrTransformAlign) {
		t.Fatalf("align 3 returned %v, want ErrTransformAlign", err)
	}
	if err := c.SetAlign(0); !errors.Is(err, impl.ErrTransformAlign) {
		t.Fatalf("align 0 returned %v, want ErrTransformAlign", err)
	}
}

func TestStatsTrackNormalizationAndApply(t *testing.T) {
	c := mustChain(t, 1920, 1080, 2)
	if err := c.SetTransform(impl.Transform{FlipH: true, FlipV: true}); err != nil {
		t.Fatalf("set double flip: %v", err)
	}
	if err := c.SetTransform(impl.Transform{Rotation: 90}); err != nil {
		t.Fatalf("set rotation: %v", err)
	}
	if _, err := c.Apply(c.Current()); err != nil {
		t.Fatalf("apply current: %v", err)
	}
	stats := c.Stats()
	if stats.Normalized != 2 {
		t.Fatalf("normalized counter = %d, want 2", stats.Normalized)
	}
	if stats.Merged != 1 {
		t.Fatalf("merged counter = %d, want 1", stats.Merged)
	}
	if stats.Applied != 1 {
		t.Fatalf("applied counter = %d, want 1", stats.Applied)
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	c := mustChain(t, 1920, 1080, 2)
	if err := c.SetTransform(impl.Transform{Rotation: 90, Crop: impl.Rect{X: 10, Y: 20, W: 100, H: 50}}); err != nil {
		t.Fatalf("set transform: %v", err)
	}
	snap, err := c.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	restored, err := impl.NewTransformChain(impl.Size{W: 640, H: 480}, 2)
	if err != nil {
		t.Fatalf("build second chain: %v", err)
	}
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := restored.Current().Rotation; got != 90 {
		t.Fatalf("rotation after restore = %d, want 90", got)
	}
	out, err := restored.OutputSize()
	if err != nil {
		t.Fatalf("output size after restore: %v", err)
	}
	if out != (impl.Size{W: 50, H: 100}) {
		t.Fatalf("output size after restore = %+v, want 50x100", out)
	}
	if got := restored.Stats().Restored; got != 1 {
		t.Fatalf("restored counter = %d, want 1", got)
	}
}

func TestRestoreRejectsCorruptSnapshot(t *testing.T) {
	c := mustChain(t, 1920, 1080, 2)
	if err := c.Restore([]byte("not a transform snapshot")); !errors.Is(err, impl.ErrTransformSnapshot) {
		t.Fatalf("corrupt restore returned %v, want ErrTransformSnapshot", err)
	}
	if got := c.Current(); got != (impl.Transform{}) {
		t.Fatalf("transform after corrupt restore = %+v, want identity", got)
	}
}

func TestResetReturnsIdentity(t *testing.T) {
	c := mustChain(t, 1920, 1080, 2)
	if err := c.SetTransform(impl.Transform{Rotation: 90, FlipH: true}); err != nil {
		t.Fatalf("set transform: %v", err)
	}
	if err := c.Reset(); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if got := c.Current(); got != (impl.Transform{}) {
		t.Fatalf("transform after reset = %+v, want identity", got)
	}
	out, err := c.OutputSize()
	if err != nil {
		t.Fatalf("output size after reset: %v", err)
	}
	if out != (impl.Size{W: 1920, H: 1080}) {
		t.Fatalf("output size after reset = %+v, want 1920x1080", out)
	}
}
