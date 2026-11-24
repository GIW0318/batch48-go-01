// Package task builds simplified bone hierarchies for animation levels of
// detail and migrates animation curves onto the merged bones.
package task

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Errors reported by the bone LOD chain builder.
var (
	ErrBadSkeleton    = errors.New("skeleton is malformed")
	ErrBadLevel       = errors.New("lod level is out of range")
	ErrBadMaxBones    = errors.New("bone budget must be positive")
	ErrCannotSimplify = errors.New("skeleton cannot be simplified to the requested bone budget")
	ErrLevelNotBuilt  = errors.New("lod level has not been built")
	ErrUnknownBone    = errors.New("bone is not part of the skeleton")
	ErrBadTrack       = errors.New("animation track is malformed")
	ErrZeroWeight     = errors.New("track group weight sums to zero")
	ErrNoAncestor     = errors.New("track bone has no ancestor in the target skeleton")
)

// Bone is one joint of a simplified hierarchy.
type Bone struct {
	ID         string
	Parent     string
	Weight     float64
	Terminal   bool
	Collider   bool
	Constraint string
}

// Skeleton is a named bone hierarchy.
type Skeleton struct {
	Root  string
	Bones []Bone
}

// Track is one sampled animation curve attached to a bone.
type Track struct {
	BoneID  string
	Weight  float64
	Samples []float64
}

// BuilderStats is a point in time summary of the builder.
type BuilderStats struct {
	Levels   int
	Builds   int
	Warnings int
	Merged   int
}

// BoneLodChainBuilder builds and caches simplified bone levels.
type BoneLodChainBuilder struct {
	mu        sync.Mutex
	maxLevels int
	levels    map[int]Skeleton
	survivors map[int]map[string]bool
	parentOf  map[string]string
	warnings  []string
	builds    int
	merged    int
	highest   int
}

// NewBoneLodChainBuilder returns a builder that keeps up to eight levels.
func NewBoneLodChainBuilder() *BoneLodChainBuilder {
	return &BoneLodChainBuilder{
		maxLevels: 8,
		levels:    make(map[int]Skeleton),
		survivors: make(map[int]map[string]bool),
		parentOf:  make(map[string]string),
	}
}

// SetMaxLevels changes the hard cap of buildable levels.
func (b *BoneLodChainBuilder) SetMaxLevels(n int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n <= 0 {
		return ErrBadLevel
	}
	b.maxLevels = n
	return nil
}

// MaxLevels returns the current level cap.
func (b *BoneLodChainBuilder) MaxLevels() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.maxLevels
}

// validateSkeleton checks the structural invariants of a hierarchy.
func validateSkeleton(s Skeleton) error {
	if s.Root == "" {
		return ErrBadSkeleton
	}
	byID := make(map[string]Bone, len(s.Bones))
	for _, bone := range s.Bones {
		if _, dup := byID[bone.ID]; dup {
			return ErrBadSkeleton
		}
		if bone.Weight < 0 {
			return ErrBadSkeleton
		}
		byID[bone.ID] = bone
	}
	root, ok := byID[s.Root]
	if !ok {
		return ErrBadSkeleton
	}
	if root.Parent != "" {
		return ErrBadSkeleton
	}
	for _, bone := range s.Bones {
		if bone.ID == s.Root {
			continue
		}
		seen := map[string]bool{bone.ID: true}
		cur := bone
		for {
			parent, ok := byID[cur.Parent]
			if !ok {
				return ErrBadSkeleton
			}
			if seen[parent.ID] {
				return ErrBadSkeleton
			}
			seen[parent.ID] = true
			if parent.ID == s.Root {
				break
			}
			cur = parent
		}
	}
	return nil
}

func cloneSkeleton(s Skeleton) Skeleton {
	out := Skeleton{Root: s.Root, Bones: make([]Bone, len(s.Bones))}
	copy(out.Bones, s.Bones)
	return out
}

// BuildLod simplifies the given level down to maxBones bones.
func (b *BoneLodChainBuilder) BuildLod(base Skeleton, level, maxBones int) (Skeleton, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if level < 1 || level > b.maxLevels {
		return Skeleton{}, ErrBadLevel
	}
	if maxBones <= 0 {
		return Skeleton{}, ErrBadMaxBones
	}
	if err := validateSkeleton(base); err != nil {
		return Skeleton{}, err
	}
	src := cloneSkeleton(base)
	if level > 1 {
		prev, ok := b.levels[level-1]
		if !ok {
			return Skeleton{}, ErrLevelNotBuilt
		}
		src = cloneSkeleton(prev)
	}
	if maxBones >= len(src.Bones) {
		return Skeleton{}, ErrCannotSimplify
	}
	for _, bone := range src.Bones {
		b.parentOf[bone.ID] = bone.Parent
	}

	weights := make(map[string]float64, len(src.Bones))
	protected := make(map[string]bool, len(src.Bones))
	constraints := make(map[string]string, len(src.Bones))
	parent := make(map[string]string, len(src.Bones))
	childCount := make(map[string]int, len(src.Bones))
	alive := make(map[string]bool, len(src.Bones))
	for _, bone := range src.Bones {
		weights[bone.ID] = bone.Weight
		protected[bone.ID] = bone.Terminal || bone.Collider
		constraints[bone.ID] = bone.Constraint
		parent[bone.ID] = bone.Parent
		alive[bone.ID] = true
	}
	for _, bone := range src.Bones {
		if bone.Parent != "" {
			childCount[bone.Parent]++
		}
	}

	aliveCount := len(src.Bones)
	mergedNow := 0
	var newWarnings []string
	for aliveCount > maxBones {
		best := ""
		for _, bone := range src.Bones {
			id := bone.ID
			if !alive[id] || id == src.Root || protected[id] || childCount[id] > 0 {
				continue
			}
			if best == "" || weights[id] < weights[best] ||
				(weights[id] == weights[best] && id < best) {
				best = id
			}
		}
		if best == "" {
			return Skeleton{}, ErrCannotSimplify
		}
		p := parent[best]
		weights[p] += weights[best]
		childCount[p]--
		alive[best] = false
		aliveCount--
		mergedNow++
		if c := constraints[best]; c != "" {
			newWarnings = append(newWarnings,
				fmt.Sprintf("bone %q merged into %q carries constraint %q", best, p, c))
		}
	}

	out := Skeleton{Root: src.Root}
	surv := make(map[string]bool, aliveCount)
	for _, bone := range src.Bones {
		if !alive[bone.ID] {
			continue
		}
		bone.Weight = weights[bone.ID]
		out.Bones = append(out.Bones, bone)
		surv[bone.ID] = true
	}

	b.levels[level] = cloneSkeleton(out)
	b.survivors[level] = surv
	b.warnings = append(b.warnings, newWarnings...)
	b.builds++
	b.merged += mergedNow
	if level > b.highest {
		b.highest = level
	}
	return out, nil
}

// MapBone resolves a bone id to its nearest surviving ancestor on one level.
func (b *BoneLodChainBuilder) MapBone(level int, id string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	surv, ok := b.survivors[level]
	if !ok {
		return "", ErrLevelNotBuilt
	}
	if _, known := b.parentOf[id]; !known {
		return "", ErrUnknownBone
	}
	cur := id
	for !surv[cur] {
		p := b.parentOf[cur]
		if p == "" {
			return "", ErrUnknownBone
		}
		cur = p
	}
	return cur, nil
}

// LevelBones returns the cached skeleton of one built level.
func (b *BoneLodChainBuilder) LevelBones(level int) (Skeleton, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	skel, ok := b.levels[level]
	if !ok {
		return Skeleton{}, false
	}
	return cloneSkeleton(skel), true
}

// MigrateCurves moves tracks onto the bones that survive in the target skeleton.
func (b *BoneLodChainBuilder) MigrateCurves(src, dst Skeleton, tracks []Track) ([]Track, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(tracks) == 0 {
		return nil, nil
	}
	sampleLen := len(tracks[0].Samples)
	for _, t := range tracks {
		if t.Weight < 0 {
			return nil, ErrBadTrack
		}
		if len(t.Samples) == 0 || len(t.Samples) != sampleLen {
			return nil, ErrBadTrack
		}
	}
	dstSet := make(map[string]bool, len(dst.Bones))
	for _, bone := range dst.Bones {
		dstSet[bone.ID] = true
	}
	srcParent := make(map[string]string, len(src.Bones))
	for _, bone := range src.Bones {
		srcParent[bone.ID] = bone.Parent
	}
	type group struct {
		weight  float64
		samples []float64
	}
	groups := make(map[string]*group)
	for _, t := range tracks {
		target := t.BoneID
		if !dstSet[target] {
			if _, ok := srcParent[target]; !ok {
				return nil, ErrUnknownBone
			}
			found := false
			for cur := target; ; {
				p, ok := srcParent[cur]
				if !ok || p == "" {
					break
				}
				if dstSet[p] {
					target = p
					found = true
					break
				}
				cur = p
			}
			if !found {
				return nil, ErrNoAncestor
			}
		}
		g, ok := groups[target]
		if !ok {
			g = &group{samples: make([]float64, sampleLen)}
			groups[target] = g
		}
		g.weight += t.Weight
		for i, s := range t.Samples {
			g.samples[i] += t.Weight * s
		}
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Track, 0, len(keys))
	for _, k := range keys {
		g := groups[k]
		if g.weight == 0 {
			return nil, ErrZeroWeight
		}
		for i := range g.samples {
			g.samples[i] /= g.weight
		}
		out = append(out, Track{BoneID: k, Weight: 1, Samples: g.samples})
	}
	return out, nil
}

// Warnings returns the merge warnings recorded so far.
func (b *BoneLodChainBuilder) Warnings() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, len(b.warnings))
	copy(out, b.warnings)
	return out
}

// Levels returns how many levels have been built.
func (b *BoneLodChainBuilder) Levels() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.highest
}

// Stats returns a consistent snapshot of the builder counters.
func (b *BoneLodChainBuilder) Stats() BuilderStats {
	b.mu.Lock()
	defer b.mu.Unlock()
	return BuilderStats{
		Levels:   b.highest,
		Builds:   b.builds,
		Warnings: len(b.warnings),
		Merged:   b.merged,
	}
}

// Reset drops every cached level, mapping and warning.
func (b *BoneLodChainBuilder) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.levels = make(map[int]Skeleton)
	b.survivors = make(map[int]map[string]bool)
	b.parentOf = make(map[string]string)
	b.warnings = nil
	b.builds = 0
	b.merged = 0
	b.highest = 0
}
