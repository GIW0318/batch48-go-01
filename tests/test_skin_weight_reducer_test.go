package task

import (
	"errors"
	"math"
	"reflect"
	"sync"
	"testing"

	impl "gsb/batch48-go-game-b-20"
)

func newReducer(t *testing.T) *impl.SkinWeightReducer {
	t.Helper()
	r := impl.NewSkinWeightReducer(0, 4)
	if r == nil {
		t.Fatalf("NewSkinWeightReducer returned nil")
	}
	return r
}

func closeTo(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func sumWeights(in []impl.Influence) float64 {
	total := 0.0
	for _, inf := range in {
		total += inf.Weight
	}
	return total
}

func TestSetLimitsValidate(t *testing.T) {
	r := newReducer(t)
	if err := r.SetMaxInfluences(0); !errors.Is(err, impl.ErrBadLimit) {
		t.Fatalf("zero influence limit: got %v, want ErrBadLimit", err)
	}
	if err := r.SetMaxInfluences(-2); !errors.Is(err, impl.ErrBadLimit) {
		t.Fatalf("negative influence limit: got %v, want ErrBadLimit", err)
	}
	if err := r.SetMaxVertices(0); !errors.Is(err, impl.ErrBadLimit) {
		t.Fatalf("zero vertex limit: got %v, want ErrBadLimit", err)
	}
	if err := r.SetMaxInfluences(6); err != nil {
		t.Fatalf("SetMaxInfluences(6): %v", err)
	}
	if err := r.SetMaxVertices(10); err != nil {
		t.Fatalf("SetMaxVertices(10): %v", err)
	}
	if got := r.MaxInfluences(); got != 6 {
		t.Fatalf("max influences = %d, want 6", got)
	}
	if got := r.MaxVertices(); got != 10 {
		t.Fatalf("max vertices = %d, want 10", got)
	}
}

func TestReduceKeepsTopByWeight(t *testing.T) {
	r := newReducer(t)
	out, err := r.Reduce([]impl.Influence{
		{Bone: 0, Weight: 5},
		{Bone: 1, Weight: 4},
		{Bone: 2, Weight: 3},
		{Bone: 3, Weight: 2},
		{Bone: 4, Weight: 1},
	}, 2)
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("reduced influences = %d, want 2", len(out))
	}
	if out[0].Bone != 0 || out[1].Bone != 1 {
		t.Fatalf("bones = [%d %d], want [0 1]", out[0].Bone, out[1].Bone)
	}
	if !closeTo(out[0].Weight, 5.0/9.0) || !closeTo(out[1].Weight, 4.0/9.0) {
		t.Fatalf("weights = [%v %v], want [5/9 4/9]", out[0].Weight, out[1].Weight)
	}
	if !closeTo(sumWeights(out), 1) {
		t.Fatalf("weight sum = %v, want 1", sumWeights(out))
	}
}

func TestReduceOrdersEqualWeightsByBoneIndex(t *testing.T) {
	r := newReducer(t)
	out, err := r.Reduce([]impl.Influence{
		{Bone: 3, Weight: 1},
		{Bone: 1, Weight: 1},
		{Bone: 2, Weight: 1},
	}, 3)
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	wantBones := []int{1, 2, 3}
	for i, want := range wantBones {
		if out[i].Bone != want {
			t.Fatalf("influence %d bone = %d, want %d", i, out[i].Bone, want)
		}
		if !closeTo(out[i].Weight, 1.0/3.0) {
			t.Fatalf("influence %d weight = %v, want 1/3", i, out[i].Weight)
		}
	}
}

func TestReduceRejectsBadMax(t *testing.T) {
	r := newReducer(t)
	in := []impl.Influence{{Bone: 0, Weight: 1}}
	for _, max := range []int{0, -1, 5} {
		if _, err := r.Reduce(in, max); !errors.Is(err, impl.ErrBadMax) {
			t.Fatalf("max %d: got %v, want ErrBadMax", max, err)
		}
	}
	if _, err := r.Reduce(in, 4); err != nil {
		t.Fatalf("Reduce at the configured limit: %v", err)
	}
}

func TestReduceEmptyInfluencesFallsBack(t *testing.T) {
	r := newReducer(t)
	out, err := r.Reduce(nil, 4)
	if err != nil {
		t.Fatalf("Reduce(nil): %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("fallback influences = %d, want 1", len(out))
	}
	if out[0].Bone != 0 || out[0].Weight != 1 {
		t.Fatalf("fallback influence = %+v, want root bone 0 with weight 1", out[0])
	}
	if got := r.FallbackCount(); got != 1 {
		t.Fatalf("fallback count = %d, want 1", got)
	}
}

func TestReduceAllZeroWeightsFallsBack(t *testing.T) {
	r := newReducer(t)
	out, err := r.Reduce([]impl.Influence{
		{Bone: 1, Weight: 0},
		{Bone: 2, Weight: 0},
	}, 4)
	if err != nil {
		t.Fatalf("Reduce zeros: %v", err)
	}
	if len(out) != 1 || out[0].Bone != 0 || out[0].Weight != 1 {
		t.Fatalf("fallback = %+v, want the root bone with weight 1", out)
	}
	if got := r.FallbackCount(); got != 1 {
		t.Fatalf("fallback count = %d, want 1", got)
	}
}

func TestReduceNegativeWeightsFallsBack(t *testing.T) {
	r := newReducer(t)
	out, err := r.Reduce([]impl.Influence{
		{Bone: 1, Weight: -1},
		{Bone: 2, Weight: -2},
	}, 4)
	if err != nil {
		t.Fatalf("Reduce negatives: %v", err)
	}
	if len(out) != 1 || out[0].Bone != 0 {
		t.Fatalf("fallback = %+v, want only the root bone", out)
	}
	if got := r.FallbackCount(); got != 1 {
		t.Fatalf("fallback count = %d, want 1", got)
	}
}

func TestReduceSingleInfluenceNormalizesToOne(t *testing.T) {
	r := newReducer(t)
	out, err := r.Reduce([]impl.Influence{{Bone: 7, Weight: 3}}, 4)
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if len(out) != 1 || out[0].Bone != 7 || out[0].Weight != 1 {
		t.Fatalf("single influence = %+v, want bone 7 weight 1", out)
	}
	if got := r.FallbackCount(); got != 0 {
		t.Fatalf("fallback count = %d, want 0", got)
	}
}

func TestNormalizeSortsAndKeepsAll(t *testing.T) {
	r := newReducer(t)
	out, err := r.Normalize([]impl.Influence{
		{Bone: 0, Weight: 2},
		{Bone: 1, Weight: 1},
		{Bone: 2, Weight: 4},
		{Bone: 3, Weight: 3},
	})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if len(out) != 4 {
		t.Fatalf("normalized influences = %d, want all 4", len(out))
	}
	wantBones := []int{2, 3, 0, 1}
	wantWeights := []float64{0.4, 0.3, 0.2, 0.1}
	for i := range wantBones {
		if out[i].Bone != wantBones[i] {
			t.Fatalf("influence %d bone = %d, want %d", i, out[i].Bone, wantBones[i])
		}
		if !closeTo(out[i].Weight, wantWeights[i]) {
			t.Fatalf("influence %d weight = %v, want %v", i, out[i].Weight, wantWeights[i])
		}
	}
	if !closeTo(sumWeights(out), 1) {
		t.Fatalf("normalized sum = %v, want 1", sumWeights(out))
	}
}

func TestNormalizeRejectsZeroSum(t *testing.T) {
	r := newReducer(t)
	if _, err := r.Normalize([]impl.Influence{
		{Bone: 0, Weight: 0},
		{Bone: 1, Weight: 0},
	}); !errors.Is(err, impl.ErrZeroWeight) {
		t.Fatalf("zero weights: got %v, want ErrZeroWeight", err)
	}
	if _, err := r.Normalize(nil); !errors.Is(err, impl.ErrZeroWeight) {
		t.Fatalf("empty influences: got %v, want ErrZeroWeight", err)
	}
}

func TestValidateAcceptsNormalizedSkin(t *testing.T) {
	r := newReducer(t)
	v := impl.VertexSkin{
		Vertex: 7,
		Influences: []impl.Influence{
			{Bone: 1, Weight: 0.5},
			{Bone: 2, Weight: 0.5},
		},
	}
	if err := r.Validate(v); err != nil {
		t.Fatalf("Validate on a normalized vertex: %v", err)
	}
}

func TestValidateRejectsBadSkins(t *testing.T) {
	r := newReducer(t)
	cases := []struct {
		name string
		v    impl.VertexSkin
		want error
	}{
		{
			name: "negative vertex",
			v:    impl.VertexSkin{Vertex: -1, Influences: []impl.Influence{{Bone: 0, Weight: 1}}},
			want: impl.ErrBadVertex,
		},
		{
			name: "negative bone",
			v:    impl.VertexSkin{Vertex: 0, Influences: []impl.Influence{{Bone: -1, Weight: 0.5}, {Bone: 1, Weight: 0.5}}},
			want: impl.ErrBadVertex,
		},
		{
			name: "negative weight",
			v:    impl.VertexSkin{Vertex: 0, Influences: []impl.Influence{{Bone: 0, Weight: -0.5}, {Bone: 1, Weight: 1.5}}},
			want: impl.ErrNegativeWeight,
		},
		{
			name: "duplicate bone",
			v:    impl.VertexSkin{Vertex: 0, Influences: []impl.Influence{{Bone: 1, Weight: 0.5}, {Bone: 1, Weight: 0.5}}},
			want: impl.ErrDuplicateBone,
		},
		{
			name: "too many influences",
			v: impl.VertexSkin{Vertex: 0, Influences: []impl.Influence{
				{Bone: 0, Weight: 0.2}, {Bone: 1, Weight: 0.2}, {Bone: 2, Weight: 0.2},
				{Bone: 3, Weight: 0.2}, {Bone: 4, Weight: 0.2},
			}},
			want: impl.ErrTooManyInfluences,
		},
		{
			name: "sum mismatch",
			v:    impl.VertexSkin{Vertex: 0, Influences: []impl.Influence{{Bone: 0, Weight: 0.5}, {Bone: 1, Weight: 0.4}}},
			want: impl.ErrSumMismatch,
		},
		{
			name: "empty influences",
			v:    impl.VertexSkin{Vertex: 0},
			want: impl.ErrEmptyInfluences,
		},
	}
	for _, tc := range cases {
		if err := r.Validate(tc.v); !errors.Is(err, tc.want) {
			t.Fatalf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestFallbackReturnsRootAndCounts(t *testing.T) {
	r := newReducer(t)
	out, err := r.Fallback(impl.VertexSkin{
		Vertex: 3,
		Influences: []impl.Influence{
			{Bone: 1, Weight: 0.6},
			{Bone: 2, Weight: 0.4},
		},
	})
	if err != nil {
		t.Fatalf("Fallback: %v", err)
	}
	if out.Vertex != 3 {
		t.Fatalf("fallback vertex = %d, want 3", out.Vertex)
	}
	if len(out.Influences) != 1 || out.Influences[0].Bone != 0 || out.Influences[0].Weight != 1 {
		t.Fatalf("fallback influences = %+v, want the root bone with weight 1", out.Influences)
	}
	if _, err := r.Fallback(impl.VertexSkin{Vertex: 4}); err != nil {
		t.Fatalf("second Fallback: %v", err)
	}
	if got := r.FallbackCount(); got != 2 {
		t.Fatalf("fallback count = %d, want 2", got)
	}
	if _, err := r.Fallback(impl.VertexSkin{Vertex: -2}); !errors.Is(err, impl.ErrBadVertex) {
		t.Fatalf("negative fallback vertex: got %v, want ErrBadVertex", err)
	}
	if got := r.FallbackCount(); got != 2 {
		t.Fatalf("rejected fallback changed the count to %d", got)
	}
}

func TestReduceMeshMatchesPerVertexAndLimit(t *testing.T) {
	r := newReducer(t)
	if err := r.SetMaxVertices(3); err != nil {
		t.Fatalf("SetMaxVertices: %v", err)
	}
	tooBig := impl.Mesh{Vertices: []impl.VertexSkin{
		{Vertex: 0, Influences: []impl.Influence{{Bone: 0, Weight: 1}}},
		{Vertex: 1, Influences: []impl.Influence{{Bone: 0, Weight: 1}}},
		{Vertex: 2, Influences: []impl.Influence{{Bone: 0, Weight: 1}}},
		{Vertex: 3, Influences: []impl.Influence{{Bone: 0, Weight: 1}}},
	}}
	if _, err := r.ReduceMesh(tooBig); !errors.Is(err, impl.ErrTooManyVertices) {
		t.Fatalf("mesh above vertex limit: got %v, want ErrTooManyVertices", err)
	}

	mesh := impl.Mesh{Vertices: []impl.VertexSkin{
		{Vertex: 0, Influences: []impl.Influence{{Bone: 0, Weight: 5}, {Bone: 1, Weight: 4}, {Bone: 2, Weight: 3}}},
		{Vertex: 1, Influences: []impl.Influence{{Bone: 1, Weight: 0}, {Bone: 2, Weight: 0}}},
		{Vertex: 2, Influences: []impl.Influence{{Bone: 0, Weight: 0.5}, {Bone: 1, Weight: 0.5}}},
	}}
	before := r.FallbackCount()
	out, err := r.ReduceMesh(mesh)
	if err != nil {
		t.Fatalf("ReduceMesh: %v", err)
	}
	if got := r.FallbackCount(); got != before+1 {
		t.Fatalf("mesh fallback count delta = %d, want 1", got-before)
	}
	if len(out.Vertices) != 3 {
		t.Fatalf("reduced mesh vertices = %d, want 3", len(out.Vertices))
	}
	for i, v := range out.Vertices {
		if v.Vertex != mesh.Vertices[i].Vertex {
			t.Fatalf("vertex %d id = %d, want %d", i, v.Vertex, mesh.Vertices[i].Vertex)
		}
		perVertex, err := r.Reduce(mesh.Vertices[i].Influences, r.MaxInfluences())
		if err != nil {
			t.Fatalf("per vertex Reduce %d: %v", i, err)
		}
		if !reflect.DeepEqual(v.Influences, perVertex) {
			t.Fatalf("vertex %d mesh result %+v differs from single Reduce %+v", i, v.Influences, perVertex)
		}
	}
	if out.Vertices[0].Influences[0].Bone != 0 || !closeTo(sumWeights(out.Vertices[0].Influences), 1) {
		t.Fatalf("vertex 0 influences = %+v, want normalized top bone 0", out.Vertices[0].Influences)
	}
	if out.Vertices[1].Influences[0].Bone != 0 || out.Vertices[1].Influences[0].Weight != 1 {
		t.Fatalf("vertex 1 fallback = %+v, want the root bone", out.Vertices[1].Influences)
	}
}

func TestConcurrentReduceDeterministicAndCounts(t *testing.T) {
	r := newReducer(t)
	input := []impl.Influence{
		{Bone: 0, Weight: 4},
		{Bone: 1, Weight: 3},
		{Bone: 2, Weight: 2},
	}
	baseline, err := r.Reduce(input, 2)
	if err != nil {
		t.Fatalf("baseline Reduce: %v", err)
	}
	fallbacksBefore := r.FallbackCount()

	var wg sync.WaitGroup
	errCh := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				got, err := r.Reduce(input, 2)
				if err != nil {
					errCh <- err
					return
				}
				if !reflect.DeepEqual(got, baseline) {
					errCh <- errors.New("concurrent Reduce returned a different result")
					return
				}
				if _, err := r.Reduce([]impl.Influence{{Bone: 3, Weight: 0}}, 2); err != nil {
					errCh <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent Reduce failed: %v", err)
	}
	if got := r.FallbackCount(); got != fallbacksBefore+80 {
		t.Fatalf("fallback count = %d, want %d", got, fallbacksBefore+80)
	}
}
