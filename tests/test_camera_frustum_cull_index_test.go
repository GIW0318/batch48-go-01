package task

import (
	"errors"
	"math"
	"testing"

	impl "gsb/batch48-go-game-b-25"
)

func item25(id int, cx, cy, cz, half float64, layer uint32) impl.Item {
	return impl.Item{
		ID: id,
		Box: impl.Box{
			Min: impl.Vec3{X: cx - half, Y: cy - half, Z: cz - half},
			Max: impl.Vec3{X: cx + half, Y: cy + half, Z: cz + half},
		},
		Sphere: impl.Sphere{Center: impl.Vec3{X: cx, Y: cy, Z: cz}, Radius: half},
		Layer:  layer,
	}
}

func frustumCamera25(t *testing.T, x *impl.FrustumCullIndex) {
	t.Helper()
	m, err := x.OrthoMatrix(-10, 10, -10, 10, 1, 100)
	if err != nil {
		t.Fatalf("ortho matrix failed: %v", err)
	}
	if err := x.SetCamera(m); err != nil {
		t.Fatalf("set camera failed: %v", err)
	}
}

func equalIDs25(got []int, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestBuildRejectsDuplicateID(t *testing.T) {
	x := impl.NewFrustumCullIndex(10)
	err := x.Build([]impl.Item{
		item25(7, 0, 0, -5, 0.5, 1),
		item25(7, 5, 0, -5, 0.5, 1),
	})
	if !errors.Is(err, impl.ErrDuplicateID) {
		t.Fatalf("duplicate id: got %v, want ErrDuplicateID", err)
	}
}

func TestBuildRejectsNonPositiveID(t *testing.T) {
	x := impl.NewFrustumCullIndex(10)
	if err := x.Build([]impl.Item{item25(0, 0, 0, -5, 0.5, 1)}); !errors.Is(err, impl.ErrBadID) {
		t.Fatalf("id 0: got %v, want ErrBadID", err)
	}
	if err := x.Build([]impl.Item{item25(-3, 0, 0, -5, 0.5, 1)}); !errors.Is(err, impl.ErrBadID) {
		t.Fatalf("id -3: got %v, want ErrBadID", err)
	}
}

func TestBuildRejectsInvertedBox(t *testing.T) {
	x := impl.NewFrustumCullIndex(10)
	bad := item25(1, 0, 0, -5, 0.5, 1)
	bad.Box = impl.Box{Min: impl.Vec3{X: 1, Y: 1, Z: 1}, Max: impl.Vec3{X: 0, Y: 0, Z: 0}}
	if err := x.Build([]impl.Item{bad}); !errors.Is(err, impl.ErrBadBox) {
		t.Fatalf("inverted box: got %v, want ErrBadBox", err)
	}
}

func TestBuildRejectsNegativeRadius(t *testing.T) {
	x := impl.NewFrustumCullIndex(10)
	bad := item25(1, 0, 0, -5, 0.5, 1)
	bad.Sphere.Radius = -1
	if err := x.Build([]impl.Item{bad}); !errors.Is(err, impl.ErrBadSphere) {
		t.Fatalf("negative radius: got %v, want ErrBadSphere", err)
	}
}

func TestBuildRejectsTooManyItems(t *testing.T) {
	x := impl.NewFrustumCullIndex(10)
	if err := x.SetMaxItems(2); err != nil {
		t.Fatalf("set max items failed: %v", err)
	}
	err := x.Build([]impl.Item{
		item25(1, 0, 0, -5, 0.5, 1),
		item25(2, 5, 0, -5, 0.5, 1),
		item25(3, 10, 0, -5, 0.5, 1),
	})
	if !errors.Is(err, impl.ErrTooManyItems) {
		t.Fatalf("3 items above a limit of 2: got %v, want ErrTooManyItems", err)
	}
}

func TestItemCountAndResolve(t *testing.T) {
	x := impl.NewFrustumCullIndex(10)
	if err := x.Build([]impl.Item{
		item25(1, 0, 0, -5, 0.5, 1),
		item25(2, 30, 0, -5, 0.5, 4),
	}); err != nil {
		t.Fatalf("build failed: %v", err)
	}
	if got := x.ItemCount(); got != 2 {
		t.Fatalf("item count = %d, want 2", got)
	}
	got, ok := x.Resolve(2)
	if !ok {
		t.Fatalf("item 2 must resolve after build")
	}
	if got.ID != 2 || got.Layer != 4 {
		t.Fatalf("resolved item = id %d layer %d, want id 2 layer 4", got.ID, got.Layer)
	}
	if _, ok := x.Resolve(99); ok {
		t.Fatalf("unknown id must not resolve")
	}
}

func TestOrthoMatrixRejectsZeroSpan(t *testing.T) {
	x := impl.NewFrustumCullIndex(10)
	if _, err := x.OrthoMatrix(0, 0, -10, 10, 1, 100); !errors.Is(err, impl.ErrBadBounds) {
		t.Fatalf("zero horizontal span: got %v, want ErrBadBounds", err)
	}
	if _, err := x.OrthoMatrix(-10, 10, 5, 5, 1, 100); !errors.Is(err, impl.ErrBadBounds) {
		t.Fatalf("zero vertical span: got %v, want ErrBadBounds", err)
	}
}

func TestPlanesExtractedAndNormalized(t *testing.T) {
	x := impl.NewFrustumCullIndex(10)
	m, err := x.OrthoMatrix(-16, 16, -9, 9, 1, 100)
	if err != nil {
		t.Fatalf("ortho matrix failed: %v", err)
	}
	if err := x.SetCamera(m); err != nil {
		t.Fatalf("set camera failed: %v", err)
	}
	planes, err := x.Planes()
	if err != nil {
		t.Fatalf("planes failed: %v", err)
	}
	if len(planes) != 6 {
		t.Fatalf("extracted %d planes, want 6", len(planes))
	}
	for i, p := range planes {
		length := math.Sqrt(p.N.X*p.N.X + p.N.Y*p.N.Y + p.N.Z*p.N.Z)
		if math.Abs(length-1) > 1e-9 {
			t.Fatalf("plane %d normal length = %v, want 1", i, length)
		}
	}
	cases := []struct {
		axis impl.Vec3
		d    float64
	}{
		{impl.Vec3{X: 1}, 16},
		{impl.Vec3{X: -1}, 16},
		{impl.Vec3{Z: -1}, -1},
		{impl.Vec3{Z: 1}, 100},
	}
	for _, c := range cases {
		found := false
		for _, p := range planes {
			if math.Abs(p.N.X-c.axis.X) < 1e-9 && math.Abs(p.N.Y-c.axis.Y) < 1e-9 && math.Abs(p.N.Z-c.axis.Z) < 1e-9 {
				found = true
				if math.Abs(p.D-c.d) > 1e-9 {
					t.Fatalf("plane with normal %+v has D=%v, want %v", c.axis, p.D, c.d)
				}
			}
		}
		if !found {
			t.Fatalf("no plane with normal %+v", c.axis)
		}
	}
}

func TestSetCameraBumpsGeneration(t *testing.T) {
	x := impl.NewFrustumCullIndex(10)
	if got := x.Generation(); got != 0 {
		t.Fatalf("initial generation = %d, want 0", got)
	}
	frustumCamera25(t, x)
	if got := x.Generation(); got != 1 {
		t.Fatalf("generation after first camera = %d, want 1", got)
	}
	m, err := x.OrthoMatrix(-5, 5, -5, 5, 1, 50)
	if err != nil {
		t.Fatalf("ortho matrix failed: %v", err)
	}
	if err := x.SetCamera(m); err != nil {
		t.Fatalf("second camera failed: %v", err)
	}
	if got := x.Generation(); got != 2 {
		t.Fatalf("generation after second camera = %d, want 2", got)
	}
}

func TestSetCameraRejectsDegenerateMatrix(t *testing.T) {
	x := impl.NewFrustumCullIndex(10)
	if err := x.SetCamera(impl.Mat4{}); !errors.Is(err, impl.ErrBadMatrix) {
		t.Fatalf("zero matrix: got %v, want ErrBadMatrix", err)
	}
}

func TestQueryReturnsVisibleAscending(t *testing.T) {
	x := impl.NewFrustumCullIndex(10)
	if err := x.Build([]impl.Item{
		item25(3, 0, 0, -5, 0.5, 1),
		item25(1, -5, 0, -5, 0.5, 1),
		item25(2, 20, 0, -5, 0.5, 1),
	}); err != nil {
		t.Fatalf("build failed: %v", err)
	}
	frustumCamera25(t, x)
	got, err := x.Query(1)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	want := []int{1, 3}
	if !equalIDs25(got, want) {
		t.Fatalf("visible ids = %v, want %v in ascending order", got, want)
	}
}

func TestQueryRespectsLayerMask(t *testing.T) {
	x := impl.NewFrustumCullIndex(10)
	if err := x.Build([]impl.Item{
		item25(1, -3, 0, -5, 0.5, 1),
		item25(2, 0, 0, -5, 0.5, 2),
		item25(3, 3, 0, -5, 0.5, 3),
	}); err != nil {
		t.Fatalf("build failed: %v", err)
	}
	frustumCamera25(t, x)
	got, err := x.Query(1)
	if err != nil {
		t.Fatalf("query mask 1 failed: %v", err)
	}
	if !equalIDs25(got, []int{1, 3}) {
		t.Fatalf("mask 1 ids = %v, want [1 3]", got)
	}
	got, err = x.Query(2)
	if err != nil {
		t.Fatalf("query mask 2 failed: %v", err)
	}
	if !equalIDs25(got, []int{2, 3}) {
		t.Fatalf("mask 2 ids = %v, want [2 3]", got)
	}
}

func TestQueryBoundaryTouchingIsVisible(t *testing.T) {
	x := impl.NewFrustumCullIndex(10)
	touching := impl.Item{
		ID:     1,
		Box:    impl.Box{Min: impl.Vec3{X: 9.5, Y: -0.5, Z: -5.5}, Max: impl.Vec3{X: 10, Y: 0.5, Z: -4.5}},
		Sphere: impl.Sphere{Center: impl.Vec3{X: 9.75, Z: -5}, Radius: 0.25},
		Layer:  1,
	}
	outside := impl.Item{
		ID:     2,
		Box:    impl.Box{Min: impl.Vec3{X: 10.4, Y: -0.1, Z: -5.1}, Max: impl.Vec3{X: 10.6, Y: 0.1, Z: -4.9}},
		Sphere: impl.Sphere{Center: impl.Vec3{X: 10.5, Z: -5}, Radius: 0.1},
		Layer:  1,
	}
	if err := x.Build([]impl.Item{touching, outside}); err != nil {
		t.Fatalf("build failed: %v", err)
	}
	frustumCamera25(t, x)
	got, err := x.Query(1)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if !equalIDs25(got, []int{1}) {
		t.Fatalf("visible ids = %v, want only the boundary touching item", got)
	}
}

func TestQueryRejectsWithoutCamera(t *testing.T) {
	x := impl.NewFrustumCullIndex(10)
	if err := x.Build([]impl.Item{item25(1, 0, 0, -5, 0.5, 1)}); err != nil {
		t.Fatalf("build failed: %v", err)
	}
	if _, err := x.Query(1); !errors.Is(err, impl.ErrNoCamera) {
		t.Fatalf("query without a camera: got %v, want ErrNoCamera", err)
	}
}

func TestCandidatesReturnsOverlappingCells(t *testing.T) {
	x := impl.NewFrustumCullIndex(10)
	if err := x.Build([]impl.Item{
		item25(1, -5, -5, -5, 0.5, 1),
		item25(2, 25, -5, -5, 0.5, 1),
		item25(3, 55, -5, -5, 0.5, 1),
	}); err != nil {
		t.Fatalf("build failed: %v", err)
	}
	near := impl.Box{Min: impl.Vec3{X: -12, Y: -12, Z: -12}, Max: impl.Vec3{X: 2, Y: 2, Z: 2}}
	got, err := x.Candidates(near)
	if err != nil {
		t.Fatalf("candidates failed: %v", err)
	}
	if !equalIDs25(got, []int{1}) {
		t.Fatalf("near candidates = %v, want [1]", got)
	}
	far := impl.Box{Min: impl.Vec3{X: 20, Y: -12, Z: -12}, Max: impl.Vec3{X: 31, Y: 2, Z: 2}}
	got, err = x.Candidates(far)
	if err != nil {
		t.Fatalf("candidates failed: %v", err)
	}
	if !equalIDs25(got, []int{2}) {
		t.Fatalf("far candidates = %v, want [2]", got)
	}
}
