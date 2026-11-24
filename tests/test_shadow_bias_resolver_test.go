package task

import (
	"errors"
	"math"
	"sync"
	"testing"

	impl "gsb/batch48-go-game-b-14"
)

func newResolver(scales ...float64) *impl.ShadowBiasResolver {
	if len(scales) == 0 {
		scales = []float64{1, 1, 1}
	}
	return impl.NewShadowBiasResolver(impl.ResolverConfig{
		Constant:        0.5,
		SlopeScale:      0.25,
		MinBias:         0.05,
		MaxBias:         4,
		NormalScale:     0.5,
		MaxNormalOffset: 4,
		CascadeScales:   scales,
	})
}

func nearly(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func TestBiasGrowsWithSlope(t *testing.T) {
	r := newResolver()
	flat, err := r.Bias(0, impl.KindDirectional, 0)
	if err != nil {
		t.Fatalf("Bias with zero slope returned unexpected error: %v", err)
	}
	steep, err := r.Bias(0, impl.KindDirectional, 2)
	if err != nil {
		t.Fatalf("Bias with slope two returned unexpected error: %v", err)
	}
	if !nearly(flat, 0.5) {
		t.Fatalf("Bias(flat) = %v, want 0.5", flat)
	}
	if !nearly(steep, 1.0) {
		t.Fatalf("Bias(steep) = %v, want 1.0", steep)
	}
	if steep <= flat {
		t.Fatalf("Bias did not grow with slope: %v -> %v", flat, steep)
	}
}

func TestBiasGrowsWithCascadeScale(t *testing.T) {
	r := newResolver(1, 2, 3)
	first, err := r.Bias(0, impl.KindSpot, 0)
	if err != nil {
		t.Fatalf("Bias(level 0) returned unexpected error: %v", err)
	}
	last, err := r.Bias(2, impl.KindSpot, 0)
	if err != nil {
		t.Fatalf("Bias(level 2) returned unexpected error: %v", err)
	}
	if !nearly(first, 0.375) {
		t.Fatalf("Bias(level 0) = %v, want 0.375", first)
	}
	if !nearly(last, 1.125) {
		t.Fatalf("Bias(level 2) = %v, want 1.125", last)
	}
	if last <= first {
		t.Fatalf("Bias did not grow with the cascade scale: %v -> %v", first, last)
	}
}

func TestBiasHonoursMinimum(t *testing.T) {
	r := impl.NewShadowBiasResolver(impl.ResolverConfig{
		Constant:        0,
		SlopeScale:      0.25,
		MinBias:         0.1,
		MaxBias:         4,
		NormalScale:     0.5,
		MaxNormalOffset: 4,
		CascadeScales:   []float64{1, 1, 1},
	})
	got, err := r.Bias(0, impl.KindDirectional, 0)
	if err != nil {
		t.Fatalf("Bias returned unexpected error: %v", err)
	}
	if !nearly(got, 0.1) {
		t.Fatalf("Bias below the configured floor = %v, want 0.1", got)
	}
}

func TestBiasRejectsUnknownKind(t *testing.T) {
	r := newResolver()
	if _, err := r.Bias(0, impl.LightKind("area"), 0); !errors.Is(err, impl.ErrUnknownKind) {
		t.Fatalf("Bias with unknown kind: got %v, want ErrUnknownKind", err)
	}
	if _, err := r.Coefficients(impl.LightKind("area")); !errors.Is(err, impl.ErrUnknownKind) {
		t.Fatalf("Coefficients with unknown kind: got %v, want ErrUnknownKind", err)
	}
}

func TestBiasRejectsOutOfRangeSlope(t *testing.T) {
	r := newResolver()
	if _, err := r.Bias(0, impl.KindDirectional, -1); !errors.Is(err, impl.ErrBadSlope) {
		t.Fatalf("Bias with negative slope: got %v, want ErrBadSlope", err)
	}
	if _, err := r.Bias(0, impl.KindDirectional, 17); !errors.Is(err, impl.ErrBadSlope) {
		t.Fatalf("Bias with excessive slope: got %v, want ErrBadSlope", err)
	}
}

func TestBiasRejectsBadCascadeLevel(t *testing.T) {
	r := newResolver()
	if _, err := r.Bias(-1, impl.KindDirectional, 0); !errors.Is(err, impl.ErrBadLevel) {
		t.Fatalf("Bias(level -1): got %v, want ErrBadLevel", err)
	}
	if _, err := r.Bias(3, impl.KindDirectional, 0); !errors.Is(err, impl.ErrBadLevel) {
		t.Fatalf("Bias(level 3): got %v, want ErrBadLevel", err)
	}
}

func TestNormalOffsetFormula(t *testing.T) {
	r := newResolver()
	got := r.NormalOffset(2, 1)
	if !nearly(got, 2) {
		t.Fatalf("NormalOffset(2,1) = %v, want 2", got)
	}
	larger := r.NormalOffset(2, 3)
	if larger <= got {
		t.Fatalf("NormalOffset did not grow with slope: %v -> %v", got, larger)
	}
}

func TestNormalOffsetCheckedValidatesInputs(t *testing.T) {
	r := newResolver()
	if _, err := r.NormalOffsetChecked(0, 1); !errors.Is(err, impl.ErrZeroTexel) {
		t.Fatalf("NormalOffsetChecked with zero texel: got %v, want ErrZeroTexel", err)
	}
	if _, err := r.NormalOffsetChecked(2, -1); !errors.Is(err, impl.ErrBadSlope) {
		t.Fatalf("NormalOffsetChecked with negative slope: got %v, want ErrBadSlope", err)
	}
	got, err := r.NormalOffsetChecked(2, 1)
	if err != nil {
		t.Fatalf("NormalOffsetChecked returned unexpected error: %v", err)
	}
	if !nearly(got, r.NormalOffset(2, 1)) {
		t.Fatalf("NormalOffsetChecked = %v, want %v", got, r.NormalOffset(2, 1))
	}
}

func TestClampLiftsBiasAndNormalOffset(t *testing.T) {
	r := newResolver()
	bias, normal := r.Clamp(0.01, -0.5)
	if !nearly(bias, 0.05) {
		t.Fatalf("clamped bias = %v, want the configured floor 0.05", bias)
	}
	if normal != 0 {
		t.Fatalf("clamped normal offset = %v, want 0", normal)
	}
}

func TestCombineAddsEachTermOnce(t *testing.T) {
	r := newResolver()
	got := r.Combine(0.3, 0.6)
	if !nearly(got.Bias, 0.3) || !nearly(got.NormalOffset, 0.6) {
		t.Fatalf("Combine = %+v, want bias 0.3 and offset 0.6", got)
	}
	if !nearly(got.Total, 0.9) {
		t.Fatalf("Combine total = %v, want 0.9", got.Total)
	}
}

func TestSetCascadeScaleInvalidatesBias(t *testing.T) {
	r := newResolver(1, 1, 1)
	before, err := r.Bias(0, impl.KindDirectional, 0)
	if err != nil {
		t.Fatalf("Bias before the scale change returned unexpected error: %v", err)
	}
	versionBefore := r.Version()
	if err := r.SetCascadeScale([]float64{2, 2, 2}); err != nil {
		t.Fatalf("SetCascadeScale returned unexpected error: %v", err)
	}
	if r.Version() <= versionBefore {
		t.Fatalf("Version did not advance after SetCascadeScale: %d -> %d", versionBefore, r.Version())
	}
	after, err := r.Bias(0, impl.KindDirectional, 0)
	if err != nil {
		t.Fatalf("Bias after the scale change returned unexpected error: %v", err)
	}
	if !nearly(after, 1.0) {
		t.Fatalf("Bias after doubling the scale = %v, want 1.0", after)
	}
	if after <= before {
		t.Fatalf("Bias kept a stale value after SetCascadeScale: %v -> %v", before, after)
	}
	if got := r.Stats().Recomputes; got < 1 {
		t.Fatalf("Stats().Recomputes = %d, want at least 1", got)
	}
}

func TestSetCascadeScaleRejectsBadTables(t *testing.T) {
	r := newResolver()
	if err := r.SetCascadeScale(nil); !errors.Is(err, impl.ErrBadScale) {
		t.Fatalf("SetCascadeScale(nil): got %v, want ErrBadScale", err)
	}
	if err := r.SetCascadeScale([]float64{1, 0, 1}); !errors.Is(err, impl.ErrBadScale) {
		t.Fatalf("SetCascadeScale with a zero entry: got %v, want ErrBadScale", err)
	}
	if err := r.SetCascadeScale([]float64{1, 1, 1, 1, 1, 1, 1, 1, 1}); !errors.Is(err, impl.ErrBadScale) {
		t.Fatalf("SetCascadeScale with nine entries: got %v, want ErrBadScale", err)
	}
}

func TestCoefficientsRankLightKinds(t *testing.T) {
	r := newResolver()
	dir, err := r.Coefficients(impl.KindDirectional)
	if err != nil {
		t.Fatalf("Coefficients(directional) returned unexpected error: %v", err)
	}
	spot, err := r.Coefficients(impl.KindSpot)
	if err != nil {
		t.Fatalf("Coefficients(spot) returned unexpected error: %v", err)
	}
	point, err := r.Coefficients(impl.KindPoint)
	if err != nil {
		t.Fatalf("Coefficients(point) returned unexpected error: %v", err)
	}
	if !(dir.Constant > spot.Constant && spot.Constant > point.Constant) {
		t.Fatalf("kind constants are not ranked: directional %v spot %v point %v", dir.Constant, spot.Constant, point.Constant)
	}
}

func TestSnapshotRestoreKeepsScales(t *testing.T) {
	r := newResolver(1, 2, 3)
	bias, err := r.Bias(2, impl.KindDirectional, 0)
	if err != nil {
		t.Fatalf("Bias returned unexpected error: %v", err)
	}
	payload, err := r.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot returned unexpected error: %v", err)
	}
	fresh := newResolver(1, 1, 1)
	if err := fresh.Restore(payload); err != nil {
		t.Fatalf("Restore returned unexpected error: %v", err)
	}
	scale, err := fresh.CascadeScale(2)
	if err != nil {
		t.Fatalf("CascadeScale after Restore returned unexpected error: %v", err)
	}
	if !nearly(scale, 3) {
		t.Fatalf("CascadeScale(2) after Restore = %v, want 3", scale)
	}
	restored, err := fresh.Bias(2, impl.KindDirectional, 0)
	if err != nil {
		t.Fatalf("Bias after Restore returned unexpected error: %v", err)
	}
	if !nearly(restored, bias) {
		t.Fatalf("Bias after Restore = %v, want %v", restored, bias)
	}
	if err := fresh.Restore([]byte("garbage")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("Restore(garbage): got %v, want ErrBadSnapshot", err)
	}
}

func TestConcurrentBiasQueries(t *testing.T) {
	r := newResolver(1, 2, 3)
	kinds := []impl.LightKind{impl.KindDirectional, impl.KindSpot, impl.KindPoint}
	results := make([]float64, 6)
	errs := make([]error, 6)
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = r.Bias(i%3, kinds[i%3], 1)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent Bias %d returned error: %v", i, err)
		}
	}
	for i := 0; i < 6; i++ {
		want, err := r.Bias(i%3, kinds[i%3], 1)
		if err != nil || !nearly(results[i], want) {
			t.Fatalf("concurrent Bias %d = %v, want %v (err %v)", i, results[i], want, err)
		}
	}
}
