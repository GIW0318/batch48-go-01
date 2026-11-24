// Package task trims per vertex skin influences to a bounded count, normalizes
// them, and falls back to the bind pose root bone for empty vertices.
package task

import (
	"errors"
	"math"
	"sort"
	"sync"
)

// Errors reported by the skin weight reducer.
var (
	ErrBadLimit          = errors.New("limit must be positive")
	ErrBadMax            = errors.New("trim count is outside the configured limit")
	ErrBadVertex         = errors.New("vertex or bone index is invalid")
	ErrEmptyInfluences   = errors.New("vertex has no influences")
	ErrTooManyInfluences = errors.New("vertex has more influences than allowed")
	ErrNegativeWeight    = errors.New("skin weight must not be negative")
	ErrDuplicateBone     = errors.New("bone appears more than once")
	ErrSumMismatch       = errors.New("influence weights do not sum to one")
	ErrTooManyVertices   = errors.New("mesh has more vertices than allowed")
	ErrZeroWeight        = errors.New("influence weights sum to zero")
)

// MinWeight is the lower bound applied to normalized influence weights.
const MinWeight = 1e-6

// Influence is one bone weight of a vertex.
type Influence struct {
	Bone   int
	Weight float64
}

// VertexSkin is the influence list of one vertex.
type VertexSkin struct {
	Vertex     int
	Influences []Influence
}

// Mesh is a set of skinned vertices.
type Mesh struct {
	Vertices []VertexSkin
	RootBone int
}

// ReducerStats is a point in time summary of the reducer.
type ReducerStats struct {
	Reduced       int
	Fallbacks     int
	InfluencesIn  int
	InfluencesOut int
}

// SkinWeightReducer trims and normalizes skin weights.
type SkinWeightReducer struct {
	mu            sync.Mutex
	rootBone      int
	maxInfluences int
	maxVertices   int
	stats         ReducerStats
}

// NewSkinWeightReducer returns a reducer bound to the given root bone.
func NewSkinWeightReducer(rootBone, maxInfluences int) *SkinWeightReducer {
	return &SkinWeightReducer{rootBone: rootBone, maxInfluences: maxInfluences, maxVertices: 4096}
}

// SetMaxInfluences changes the hard limit of influences per vertex.
func (r *SkinWeightReducer) SetMaxInfluences(n int) error {
	if n <= 0 {
		return ErrBadLimit
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.maxInfluences = n
	return nil
}

// SetMaxVertices changes the hard limit of vertices per mesh.
func (r *SkinWeightReducer) SetMaxVertices(n int) error {
	if n <= 0 {
		return ErrBadLimit
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.maxVertices = n
	return nil
}

// MaxInfluences returns the current influence limit.
func (r *SkinWeightReducer) MaxInfluences() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.maxInfluences
}

// MaxVertices returns the current vertex limit.
func (r *SkinWeightReducer) MaxVertices() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.maxVertices
}

// Reduce keeps the strongest max influences and normalizes them to one.
func (r *SkinWeightReducer) Reduce(in []Influence, max int) ([]Influence, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if max < 1 || max > r.maxInfluences {
		return nil, ErrBadMax
	}

	out, fallback := r.reduceLocked(in, max)
	r.stats.Reduced++
	r.stats.InfluencesIn += len(in)
	r.stats.InfluencesOut += len(out)
	if fallback {
		r.stats.Fallbacks++
	}
	return out, nil
}

// Normalize sorts and normalizes every positive influence without trimming.
func (r *SkinWeightReducer) Normalize(in []Influence) ([]Influence, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	merged := mergeAndSort(in)
	if len(merged) == 0 {
		return nil, ErrZeroWeight
	}
	return normalizeInfluences(merged), nil
}

// Validate checks one vertex skin against the configured limits.
func (r *SkinWeightReducer) Validate(v VertexSkin) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if v.Vertex < 0 {
		return ErrBadVertex
	}
	if len(v.Influences) == 0 {
		return ErrEmptyInfluences
	}
	if len(v.Influences) > r.maxInfluences {
		return ErrTooManyInfluences
	}

	seen := make(map[int]struct{}, len(v.Influences))
	sum := 0.0
	for _, inf := range v.Influences {
		if inf.Bone < 0 {
			return ErrBadVertex
		}
		if inf.Weight < 0 {
			return ErrNegativeWeight
		}
		if _, ok := seen[inf.Bone]; ok {
			return ErrDuplicateBone
		}
		seen[inf.Bone] = struct{}{}
		sum += inf.Weight
	}
	if math.IsNaN(sum) || math.Abs(sum-1) > 1e-6 {
		return ErrSumMismatch
	}
	return nil
}

// Fallback replaces a vertex with a single root bone influence.
func (r *SkinWeightReducer) Fallback(v VertexSkin) (VertexSkin, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if v.Vertex < 0 {
		return VertexSkin{}, ErrBadVertex
	}
	r.stats.Fallbacks++
	return r.fallbackLocked(v.Vertex), nil
}

// ReduceMesh reduces every vertex of a mesh with the configured influence limit.
func (r *SkinWeightReducer) ReduceMesh(m Mesh) (Mesh, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.maxInfluences < 1 {
		return Mesh{}, ErrBadMax
	}
	if len(m.Vertices) > r.maxVertices {
		return Mesh{}, ErrTooManyVertices
	}

	out := Mesh{RootBone: m.RootBone}
	if m.Vertices != nil {
		out.Vertices = make([]VertexSkin, len(m.Vertices))
	}
	for i, v := range m.Vertices {
		influences, fallback := r.reduceLocked(v.Influences, r.maxInfluences)
		out.Vertices[i] = VertexSkin{Vertex: v.Vertex, Influences: influences}

		r.stats.Reduced++
		r.stats.InfluencesIn += len(v.Influences)
		r.stats.InfluencesOut += len(influences)
		if fallback {
			r.stats.Fallbacks++
		}
	}
	return out, nil
}

// FallbackCount returns how many vertices fell back to the root bone.
func (r *SkinWeightReducer) FallbackCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stats.Fallbacks
}

// RootBone returns the bind pose root bone used by fallbacks.
func (r *SkinWeightReducer) RootBone() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rootBone
}

// Stats returns a consistent snapshot of the reducer counters.
func (r *SkinWeightReducer) Stats() ReducerStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stats
}

// Reset clears every counter while keeping the configuration.
func (r *SkinWeightReducer) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stats = ReducerStats{}
}

func (r *SkinWeightReducer) reduceLocked(in []Influence, max int) ([]Influence, bool) {
	merged := mergeAndSort(in)
	if len(merged) == 0 {
		return []Influence{{Bone: r.rootBone, Weight: 1}}, true
	}
	if len(merged) > max {
		merged = merged[:max]
	}
	return normalizeInfluences(merged), false
}

func (r *SkinWeightReducer) fallbackLocked(vertex int) VertexSkin {
	return VertexSkin{
		Vertex: vertex,
		Influences: []Influence{
			{Bone: r.rootBone, Weight: 1},
		},
	}
}

func mergeAndSort(in []Influence) []Influence {
	weights := make(map[int]float64, len(in))
	for _, inf := range in {
		if inf.Bone < 0 {
			continue
		}
		weights[inf.Bone] += inf.Weight
	}

	merged := make([]Influence, 0, len(weights))
	for bone, weight := range weights {
		if weight > 0 {
			merged = append(merged, Influence{Bone: bone, Weight: weight})
		}
	}
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].Weight == merged[j].Weight {
			return merged[i].Bone < merged[j].Bone
		}
		return merged[i].Weight > merged[j].Weight
	})
	return merged
}

func normalizeInfluences(in []Influence) []Influence {
	sum := 0.0
	for _, inf := range in {
		sum += inf.Weight
	}

	out := make([]Influence, len(in))
	for i, inf := range in {
		out[i] = Influence{Bone: inf.Bone, Weight: inf.Weight / sum}
	}

	sum = 0
	for i := range out {
		if out[i].Weight < MinWeight {
			out[i].Weight = MinWeight
		}
		sum += out[i].Weight
	}
	for i := range out {
		out[i].Weight /= sum
	}
	return out
}
