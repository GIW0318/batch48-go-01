package task

import (
	"errors"
	"math"
	"sync"
	"testing"

	impl "gsb/batch48-go-game-b-13"
)

func newSolver() *impl.CascadeFitSolver {
	return impl.NewCascadeFitSolver(impl.CascadeConfig{
		Cascades:      3,
		Lambda:        0.5,
		BlendFraction: 0.25,
		TexelSize:     impl.Vec2{X: 1, Y: 1},
	})
}

func testCamera() impl.Camera {
	return impl.Camera{
		Position: impl.Vec3{X: 3, Y: 5, Z: -10},
		Yaw:      35,
		Pitch:    -25,
		FovYDeg:  55,
		Aspect:   1.6,
		Near:     0.1,
		Far:      120,
	}
}

func closeEnough(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func TestSplitAutomaticIsStrictlyIncreasing(t *testing.T) {
	s := newSolver()
	bounds, err := s.Split(nil, 0.1, 100)
	if err != nil {
		t.Fatalf("Split returned unexpected error: %v", err)
	}
	if len(bounds) != 4 {
		t.Fatalf("Split returned %d bounds, want 4", len(bounds))
	}
	if bounds[0] != 0.1 || bounds[3] != 100 {
		t.Fatalf("Split bounds = %v, want the near and far planes at the ends", bounds)
	}
	for i := 1; i < len(bounds); i++ {
		if bounds[i] <= bounds[i-1] {
			t.Fatalf("Split bounds are not strictly increasing: %v", bounds)
		}
	}
}

func TestSplitManualDistancesOverrideAutomation(t *testing.T) {
	s := newSolver()
	bounds, err := s.Split([]float64{10, 30}, 0.1, 100)
	if err != nil {
		t.Fatalf("Split with manual distances returned unexpected error: %v", err)
	}
	want := []float64{0.1, 10, 30, 100}
	if len(bounds) != len(want) {
		t.Fatalf("Split returned %v, want %v", bounds, want)
	}
	for i := range want {
		if !closeEnough(bounds[i], want[i]) {
			t.Fatalf("Split returned %v, want %v", bounds, want)
		}
	}
}

func TestSplitRejectsBadRange(t *testing.T) {
	s := newSolver()
	if _, err := s.Split(nil, 0, 100); !errors.Is(err, impl.ErrBadRange) {
		t.Fatalf("Split with near zero: got %v, want ErrBadRange", err)
	}
	if _, err := s.Split(nil, 50, 50); !errors.Is(err, impl.ErrBadRange) {
		t.Fatalf("Split with far equal to near: got %v, want ErrBadRange", err)
	}
}

func TestSplitRejectsNonMonotonicManualDistances(t *testing.T) {
	s := newSolver()
	if _, err := s.Split([]float64{40, 10}, 0.1, 100); !errors.Is(err, impl.ErrNonMonotonic) {
		t.Fatalf("Split with reversed distances: got %v, want ErrNonMonotonic", err)
	}
}

func TestSplitRejectsWrongManualCount(t *testing.T) {
	s := newSolver()
	if _, err := s.Split([]float64{10}, 0.1, 100); !errors.Is(err, impl.ErrBadSplitCount) {
		t.Fatalf("Split with one distance: got %v, want ErrBadSplitCount", err)
	}
	if _, err := s.Split([]float64{10, 20, 30}, 0.1, 100); !errors.Is(err, impl.ErrBadSplitCount) {
		t.Fatalf("Split with three distances: got %v, want ErrBadSplitCount", err)
	}
}

func TestFitIsDeterministicAndCached(t *testing.T) {
	s := newSolver()
	first, err := s.Fit(testCamera(), 0)
	if err != nil {
		t.Fatalf("first Fit returned unexpected error: %v", err)
	}
	if first == (impl.Mat4{}) {
		t.Fatalf("Fit returned the zero matrix")
	}
	second, err := s.Fit(testCamera(), 0)
	if err != nil {
		t.Fatalf("second Fit returned unexpected error: %v", err)
	}
	if first != second {
		t.Fatalf("repeated Fit produced different matrices")
	}
	if got := s.Stats().CacheHits; got < 1 {
		t.Fatalf("Stats().CacheHits = %d after a repeated Fit, want at least 1", got)
	}
}

func TestFitRejectsBadCascadeLevel(t *testing.T) {
	s := newSolver()
	if _, err := s.Fit(testCamera(), -1); !errors.Is(err, impl.ErrBadCascade) {
		t.Fatalf("Fit(level -1): got %v, want ErrBadCascade", err)
	}
	if _, err := s.Fit(testCamera(), 3); !errors.Is(err, impl.ErrBadCascade) {
		t.Fatalf("Fit(level 3): got %v, want ErrBadCascade", err)
	}
}

func TestFitRejectsInvalidCamera(t *testing.T) {
	s := newSolver()
	cam := testCamera()
	cam.FovYDeg = 0
	if _, err := s.Fit(cam, 0); !errors.Is(err, impl.ErrBadCamera) {
		t.Fatalf("Fit with zero field of view: got %v, want ErrBadCamera", err)
	}
	cam = testCamera()
	cam.Aspect = 0
	if _, err := s.Fit(cam, 0); !errors.Is(err, impl.ErrBadCamera) {
		t.Fatalf("Fit with zero aspect: got %v, want ErrBadCamera", err)
	}
	cam = testCamera()
	cam.Far = cam.Near
	if _, err := s.Fit(cam, 0); !errors.Is(err, impl.ErrBadCamera) {
		t.Fatalf("Fit with far equal to near: got %v, want ErrBadCamera", err)
	}
}

func TestFarPlaneChangeInvalidatesCache(t *testing.T) {
	s := newSolver()
	before, err := s.Fit(testCamera(), 0)
	if err != nil {
		t.Fatalf("Fit before the change returned unexpected error: %v", err)
	}
	versionBefore := s.Version()
	cam := testCamera()
	cam.Far = 170
	after, err := s.Fit(cam, 0)
	if err != nil {
		t.Fatalf("Fit after the change returned unexpected error: %v", err)
	}
	if after == before {
		t.Fatalf("Fit reused the stale matrix after the far plane changed")
	}
	if s.Version() <= versionBefore {
		t.Fatalf("Version did not advance after the far plane change: %d -> %d", versionBefore, s.Version())
	}
	if got := s.Stats().Invalidations; got < 1 {
		t.Fatalf("Stats().Invalidations = %d, want at least 1", got)
	}
}

func TestSnapTexelAlignsTranslation(t *testing.T) {
	s := newSolver()
	m := impl.Mat4{}
	m[0] = 1
	m[5] = 1
	m[10] = 1
	m[15] = 1
	m[12] = 1.27
	m[13] = -2.63
	snapped := s.SnapTexel(m, 0.5)
	if !closeEnough(snapped[12], 1.5) {
		t.Fatalf("snapped x translation = %v, want 1.5", snapped[12])
	}
	if !closeEnough(snapped[13], -2.5) {
		t.Fatalf("snapped y translation = %v, want -2.5", snapped[13])
	}
	if snapped[0] != m[0] || snapped[5] != m[5] || snapped[10] != m[10] || snapped[15] != m[15] {
		t.Fatalf("SnapTexel changed a non translation element: %v", snapped)
	}
}

func TestSnapTexelLeavesNonPositiveTexelUntouched(t *testing.T) {
	s := newSolver()
	m := impl.Mat4{}
	m[12] = 1.27
	m[13] = -2.63
	if got := s.SnapTexel(m, 0); got != m {
		t.Fatalf("SnapTexel with zero texel changed the matrix: %v", got)
	}
	if got := s.SnapTexel(m, -1); got != m {
		t.Fatalf("SnapTexel with negative texel changed the matrix: %v", got)
	}
}

func TestBlendBandReportsConfiguredWidth(t *testing.T) {
	s := newSolver()
	start, end := s.BlendBand(0)
	if !closeEnough(start, 0.75) || !closeEnough(end, 1) {
		t.Fatalf("BlendBand(0) = (%v,%v), want (0.75,1)", start, end)
	}
	start, end = s.BlendBand(2)
	if !closeEnough(start, 1) || !closeEnough(end, 1) {
		t.Fatalf("BlendBand(last) = (%v,%v), want (1,1)", start, end)
	}
	start, end = s.BlendBand(9)
	if start != 0 || end != 0 {
		t.Fatalf("BlendBand(9) = (%v,%v), want (0,0)", start, end)
	}
}

func TestInvalidateDropsCachedMatrix(t *testing.T) {
	s := newSolver()
	if _, err := s.Fit(testCamera(), 1); err != nil {
		t.Fatalf("Fit before Invalidate returned unexpected error: %v", err)
	}
	versionBefore := s.Version()
	s.Invalidate("shadow distance changed")
	if s.Version() <= versionBefore {
		t.Fatalf("Version did not advance after Invalidate: %d -> %d", versionBefore, s.Version())
	}
	if _, err := s.Fit(testCamera(), 1); err != nil {
		t.Fatalf("Fit after Invalidate returned unexpected error: %v", err)
	}
	if got := s.Stats().Invalidations; got < 1 {
		t.Fatalf("Stats().Invalidations = %d, want at least 1", got)
	}
}

func TestSnapshotRestoreKeepsConfigurationAndVersion(t *testing.T) {
	s := newSolver()
	m, err := s.Fit(testCamera(), 1)
	if err != nil {
		t.Fatalf("Fit returned unexpected error: %v", err)
	}
	payload, err := s.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot returned unexpected error: %v", err)
	}
	fresh := newSolver()
	if err := fresh.Restore(payload); err != nil {
		t.Fatalf("Restore returned unexpected error: %v", err)
	}
	if fresh.Version() != s.Version() {
		t.Fatalf("Version after Restore = %d, want %d", fresh.Version(), s.Version())
	}
	if fresh.CascadeCount() != s.CascadeCount() {
		t.Fatalf("CascadeCount after Restore = %d, want %d", fresh.CascadeCount(), s.CascadeCount())
	}
	got, err := fresh.Fit(testCamera(), 1)
	if err != nil {
		t.Fatalf("Fit after Restore returned unexpected error: %v", err)
	}
	if got != m {
		t.Fatalf("matrix after Restore differs from the snapshot state")
	}
	if err := fresh.Restore([]byte("garbage")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("Restore(garbage): got %v, want ErrBadSnapshot", err)
	}
}

func TestConcurrentFitUsesSameMatrix(t *testing.T) {
	s := newSolver()
	cam := testCamera()
	results := make([]impl.Mat4, 6)
	errs := make([]error, 6)
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = s.Fit(cam, 2)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent Fit %d returned error: %v", i, err)
		}
	}
	for i := 1; i < len(results); i++ {
		if results[i] != results[0] {
			t.Fatalf("concurrent Fit %d produced a different matrix", i)
		}
	}
}
