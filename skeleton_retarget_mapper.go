// Package task maps a source skeleton onto a target skeleton and retargets
// poses while keeping rest pose differences and missing bones explicit.
package task

import (
	"errors"
	"math"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// Errors reported by the skeleton retargeter.
var (
	ErrBadSkeleton     = errors.New("skeleton is malformed")
	ErrNoMap           = errors.New("no bone map has been built")
	ErrBadMaxMappings  = errors.New("mapping limit must be positive")
	ErrMappingCapacity = errors.New("bone map exceeds the configured mapping limit")
	ErrBadRestDelta    = errors.New("rest delta is outside the allowed range")
)

// Vec3 is a three component vector used for rest pose and pose channels.
type Vec3 struct {
	X float64
	Y float64
	Z float64
}

// Bone is one skeleton joint.
type Bone struct {
	Name      string
	Parent    string
	Role      string
	Length    float64
	RestPos   Vec3
	RestRot   Vec3
	RestScale Vec3
}

// Skeleton is a named bone hierarchy.
type Skeleton struct {
	Root  string
	Bones []Bone
}

// Pose carries per bone rotation, position and scale channels.
type Pose struct {
	Rot      map[string]Vec3
	Pos      map[string]Vec3
	Scale    map[string]Vec3
	Fallback []string
}

// Mapping is one source to target bone pair.
type Mapping struct {
	Source    string
	Target    string
	MatchedBy string
}

// Conflict records a source bone that matched several target bones.
type Conflict struct {
	Source  string
	Targets []string
}

// Fallback records a source bone without a target counterpart.
type Fallback struct {
	Source   string
	Strategy string
}

// BoneMap is the result of matching one source skeleton to one target skeleton.
type BoneMap struct {
	Source    string
	Target    string
	Pairs     []Mapping
	Conflicts []Conflict
	Missing   []Fallback
}

type retargetState struct {
	boneMap         BoneMap
	srcBones        []Bone
	dstBones        []Bone
	srcByName       map[string]Bone
	dstByName       map[string]Bone
	mappingBySource map[string]Mapping
	sourceByTarget  map[string]string
	missingBySource map[string]Fallback
}

// SkeletonRetargeter builds bone maps and retargets poses onto them.
type SkeletonRetargeter struct {
	mu          sync.RWMutex
	maxMappings int
	restDelta   Vec3
	state       *retargetState
}

// NewSkeletonRetargeter returns a retargeter capped at maxMappings pairs.
func NewSkeletonRetargeter(maxMappings int) *SkeletonRetargeter {
	return &SkeletonRetargeter{maxMappings: maxMappings}
}

// SetMaxMappings changes the hard cap of one bone map.
func (r *SkeletonRetargeter) SetMaxMappings(n int) error {
	if n <= 0 {
		return ErrBadMaxMappings
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.maxMappings = n
	return nil
}

// MaxMappings returns the current mapping cap.
func (r *SkeletonRetargeter) MaxMappings() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.maxMappings
}

// BuildMap matches source bones to target bones by role first and name second.
func (r *SkeletonRetargeter) BuildMap(src, dst Skeleton) (*BoneMap, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := validateSkeleton(src); err != nil {
		return nil, err
	}
	if err := validateSkeleton(dst); err != nil {
		return nil, err
	}

	state := buildState(src, dst)
	if len(state.boneMap.Pairs) > r.maxMappings {
		return nil, ErrMappingCapacity
	}

	r.state = state
	return cloneBoneMap(state.boneMap), nil
}

// Map returns the current bone map.
func (r *SkeletonRetargeter) Map() (*BoneMap, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.state == nil {
		return nil, false
	}
	return cloneBoneMap(r.state.boneMap), true
}

// Retarget converts a source pose into the target skeleton pose.
func (r *SkeletonRetargeter) Retarget(p Pose) (Pose, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	state := r.state
	if state == nil {
		return Pose{}, ErrNoMap
	}

	rootRatio := state.dstByName[state.boneMap.Target].Length /
		state.srcByName[state.boneMap.Source].Length

	type channels struct {
		rot   Vec3
		pos   Vec3
		scale Vec3
	}

	computed := make(map[string]channels, len(state.mappingBySource))
	for target, source := range state.sourceByTarget {
		srcBone := state.srcByName[source]
		dstBone := state.dstByName[target]

		srcRot, ok := p.Rot[source]
		if !ok {
			srcRot = srcBone.RestRot
		}
		srcPos, ok := p.Pos[source]
		if !ok {
			srcPos = srcBone.RestPos
		}
		srcScale, ok := p.Scale[source]
		if !ok {
			srcScale = srcBone.RestScale
		}

		computed[target] = channels{
			rot: Vec3{
				X: dstBone.RestRot.X + (srcRot.X - srcBone.RestRot.X) + r.restDelta.X,
				Y: dstBone.RestRot.Y + (srcRot.Y - srcBone.RestRot.Y) + r.restDelta.Y,
				Z: dstBone.RestRot.Z + (srcRot.Z - srcBone.RestRot.Z) + r.restDelta.Z,
			},
			pos: Vec3{
				X: dstBone.RestPos.X + (srcPos.X-srcBone.RestPos.X)*rootRatio,
				Y: dstBone.RestPos.Y + (srcPos.Y-srcBone.RestPos.Y)*rootRatio,
				Z: dstBone.RestPos.Z + (srcPos.Z-srcBone.RestPos.Z)*rootRatio,
			},
			scale: Vec3{
				X: (srcScale.X / srcBone.RestScale.X) * (dstBone.Length / srcBone.Length),
				Y: (srcScale.Y / srcBone.RestScale.Y) * (dstBone.Length / srcBone.Length),
				Z: (srcScale.Z / srcBone.RestScale.Z) * (dstBone.Length / srcBone.Length),
			},
		}
	}

	out := Pose{
		Rot:   make(map[string]Vec3, len(state.dstBones)),
		Pos:   make(map[string]Vec3, len(state.dstBones)),
		Scale: make(map[string]Vec3, len(state.dstBones)),
	}
	fallback := make([]string, 0)

	for _, dstBone := range state.dstBones {
		if value, ok := computed[dstBone.Name]; ok {
			out.Rot[dstBone.Name] = value.rot
			out.Pos[dstBone.Name] = value.pos
			out.Scale[dstBone.Name] = value.scale
			continue
		}

		inherited := false
		for parent := dstBone.Parent; parent != ""; {
			if value, ok := computed[parent]; ok {
				out.Rot[dstBone.Name] = value.rot
				out.Pos[dstBone.Name] = value.pos
				out.Scale[dstBone.Name] = value.scale
				inherited = true
				break
			}
			parentBone, ok := state.dstByName[parent]
			if !ok {
				break
			}
			parent = parentBone.Parent
		}

		if !inherited {
			out.Rot[dstBone.Name] = dstBone.RestRot
			out.Pos[dstBone.Name] = dstBone.RestPos
			out.Scale[dstBone.Name] = dstBone.RestScale
		}
		fallback = append(fallback, dstBone.Name)
	}

	sort.Strings(fallback)
	out.Fallback = fallback
	return out, nil
}

// SetRestDelta sets the constant rotation offset added after retargeting.
func (r *SkeletonRetargeter) SetRestDelta(v Vec3) error {
	if !validDeltaComponent(v.X) || !validDeltaComponent(v.Y) || !validDeltaComponent(v.Z) {
		return ErrBadRestDelta
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.restDelta = v
	return nil
}

// RestDelta returns the current rotation offset.
func (r *SkeletonRetargeter) RestDelta() Vec3 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.restDelta
}

// Missing reports the fallback strategy registered for a source bone.
func (r *SkeletonRetargeter) Missing(name string) (Fallback, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.state == nil {
		return Fallback{}, false
	}
	fallback, ok := r.state.missingBySource[name]
	return fallback, ok
}

// Mappings returns the current pairs ordered by source bone name.
func (r *SkeletonRetargeter) Mappings() []Mapping {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.state == nil {
		return nil
	}
	return cloneMappings(r.state.boneMap.Pairs)
}

// Conflicts returns the registered one to many matches ordered by source name.
func (r *SkeletonRetargeter) Conflicts() []Conflict {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.state == nil {
		return nil
	}
	return cloneConflicts(r.state.boneMap.Conflicts)
}

// Normalize folds a bone name into its canonical matching form.
func (r *SkeletonRetargeter) Normalize(name string) string {
	return normalizeName(name)
}

// Reset drops the current bone map and rest delta.
func (r *SkeletonRetargeter) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state = nil
	r.restDelta = Vec3{}
}

func buildState(src, dst Skeleton) *retargetState {
	state := &retargetState{
		boneMap: BoneMap{
			Source: src.Root,
			Target: dst.Root,
		},
		srcBones:        append([]Bone(nil), src.Bones...),
		dstBones:        append([]Bone(nil), dst.Bones...),
		srcByName:       make(map[string]Bone, len(src.Bones)),
		dstByName:       make(map[string]Bone, len(dst.Bones)),
		mappingBySource: make(map[string]Mapping, len(src.Bones)),
		sourceByTarget:  make(map[string]string, len(dst.Bones)),
		missingBySource: make(map[string]Fallback),
	}
	for _, bone := range src.Bones {
		state.srcByName[bone.Name] = bone
	}
	for _, bone := range dst.Bones {
		state.dstByName[bone.Name] = bone
	}

	srcBones := append([]Bone(nil), src.Bones...)
	sort.Slice(srcBones, func(i, j int) bool { return srcBones[i].Name < srcBones[j].Name })
	dstBones := append([]Bone(nil), dst.Bones...)
	sort.Slice(dstBones, func(i, j int) bool { return dstBones[i].Name < dstBones[j].Name })

	occupied := make(map[string]bool, len(dst.Bones))
	conflicted := make(map[string]bool)
	pairs := make([]Mapping, 0, len(src.Bones))
	conflicts := make([]Conflict, 0)

	for _, srcBone := range srcBones {
		if srcBone.Role != "" {
			candidates := matchingTargets(dstBones, occupied, func(dstBone Bone) bool {
				return dstBone.Role == srcBone.Role
			})
			switch len(candidates) {
			case 1:
				pairs = append(pairs, registerMapping(srcBone, candidates[0], "role", occupied))
				continue
			case 0:
			default:
				conflicts = append(conflicts, Conflict{
					Source:  srcBone.Name,
					Targets: candidateNames(candidates),
				})
				conflicted[srcBone.Name] = true
				continue
			}
		}

		normalized := normalizeName(srcBone.Name)
		candidates := matchingTargets(dstBones, occupied, func(dstBone Bone) bool {
			return normalizeName(dstBone.Name) == normalized
		})
		switch len(candidates) {
		case 1:
			pairs = append(pairs, registerMapping(srcBone, candidates[0], "name", occupied))
		case 0:
		default:
			conflicts = append(conflicts, Conflict{
				Source:  srcBone.Name,
				Targets: candidateNames(candidates),
			})
			conflicted[srcBone.Name] = true
		}
	}

	for _, pair := range pairs {
		state.mappingBySource[pair.Source] = pair
		state.sourceByTarget[pair.Target] = pair.Source
	}

	missing := make([]Fallback, 0)
	for _, srcBone := range srcBones {
		if _, ok := state.mappingBySource[srcBone.Name]; ok {
			continue
		}
		if conflicted[srcBone.Name] {
			continue
		}
		fallback := Fallback{Source: srcBone.Name, Strategy: missingStrategy(srcBone, state.srcByName, state.mappingBySource)}
		missing = append(missing, fallback)
		state.missingBySource[srcBone.Name] = fallback
	}

	sort.Slice(pairs, func(i, j int) bool { return pairs[i].Source < pairs[j].Source })
	sort.Slice(conflicts, func(i, j int) bool { return conflicts[i].Source < conflicts[j].Source })
	sort.Slice(missing, func(i, j int) bool { return missing[i].Source < missing[j].Source })
	state.boneMap.Pairs = pairs
	state.boneMap.Conflicts = conflicts
	state.boneMap.Missing = missing
	return state
}

func registerMapping(srcBone, dstBone Bone, matchedBy string, occupied map[string]bool) Mapping {
	occupied[dstBone.Name] = true
	return Mapping{Source: srcBone.Name, Target: dstBone.Name, MatchedBy: matchedBy}
}

func matchingTargets(bones []Bone, occupied map[string]bool, match func(Bone) bool) []Bone {
	candidates := make([]Bone, 0)
	for _, bone := range bones {
		if occupied[bone.Name] {
			continue
		}
		if match(bone) {
			candidates = append(candidates, bone)
		}
	}
	return candidates
}

func candidateNames(bones []Bone) []string {
	names := make([]string, 0, len(bones))
	for _, bone := range bones {
		names = append(names, bone.Name)
	}
	sort.Strings(names)
	return names
}

func missingStrategy(bone Bone, srcByName map[string]Bone, mappings map[string]Mapping) string {
	for parent := bone.Parent; parent != ""; {
		if _, ok := mappings[parent]; ok {
			return "parent"
		}
		parentBone, ok := srcByName[parent]
		if !ok {
			break
		}
		parent = parentBone.Parent
	}
	return "root"
}

func normalizeName(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.Map(func(c rune) rune {
		switch {
		case c == '.' || c == ':':
			return '_'
		case unicode.IsSpace(c):
			return '_'
		default:
			return c
		}
	}, s)
	s = collapseUnderscores(s)

	for {
		switch {
		case strings.HasPrefix(s, "bip01_"):
			s = strings.TrimPrefix(s, "bip01_")
		case strings.HasPrefix(s, "mixamorig_"):
			s = strings.TrimPrefix(s, "mixamorig_")
		default:
			goto suffix
		}
		s = collapseUnderscores(s)
	}

suffix:
	if strings.HasSuffix(s, "_l") || strings.HasSuffix(s, "_r") {
		s = s[:len(s)-2]
	}
	return s
}

func collapseUnderscores(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	lastUnderscore := false
	for _, c := range s {
		if c == '_' {
			if lastUnderscore {
				continue
			}
			lastUnderscore = true
		} else {
			lastUnderscore = false
		}
		b.WriteRune(c)
	}
	return b.String()
}

func validDeltaComponent(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && math.Abs(v) <= 180
}

func validateSkeleton(s Skeleton) error {
	if s.Root == "" || len(s.Bones) == 0 {
		return ErrBadSkeleton
	}

	byName := make(map[string]Bone, len(s.Bones))
	for _, bone := range s.Bones {
		if bone.Name == "" {
			return ErrBadSkeleton
		}
		if _, exists := byName[bone.Name]; exists {
			return ErrBadSkeleton
		}
		if !(bone.Length > 0) {
			return ErrBadSkeleton
		}
		if !(bone.RestScale.X > 0) || !(bone.RestScale.Y > 0) || !(bone.RestScale.Z > 0) {
			return ErrBadSkeleton
		}
		byName[bone.Name] = bone
	}

	rootBone, ok := byName[s.Root]
	if !ok || rootBone.Parent != "" {
		return ErrBadSkeleton
	}
	for _, bone := range s.Bones {
		if bone.Name == s.Root {
			continue
		}
		if bone.Parent == "" {
			return ErrBadSkeleton
		}
		if _, exists := byName[bone.Parent]; !exists {
			return ErrBadSkeleton
		}
	}

	const (
		visiting = 1
		done     = 2
	)
	status := make(map[string]int, len(s.Bones))
	var visit func(string) bool
	visit = func(name string) bool {
		switch status[name] {
		case visiting:
			return false
		case done:
			return true
		}
		status[name] = visiting
		bone, ok := byName[name]
		if !ok {
			return false
		}
		if bone.Parent != "" && !visit(bone.Parent) {
			return false
		}
		status[name] = done
		return true
	}
	for _, bone := range s.Bones {
		if !visit(bone.Name) {
			return ErrBadSkeleton
		}
	}
	return nil
}

func cloneBoneMap(boneMap BoneMap) *BoneMap {
	clone := boneMap
	clone.Pairs = cloneMappings(boneMap.Pairs)
	clone.Conflicts = cloneConflicts(boneMap.Conflicts)
	clone.Missing = cloneFallbacks(boneMap.Missing)
	return &clone
}

func cloneMappings(mappings []Mapping) []Mapping {
	if mappings == nil {
		return nil
	}
	clone := make([]Mapping, len(mappings))
	copy(clone, mappings)
	return clone
}

func cloneFallbacks(fallbacks []Fallback) []Fallback {
	if fallbacks == nil {
		return nil
	}
	clone := make([]Fallback, len(fallbacks))
	copy(clone, fallbacks)
	return clone
}

func cloneConflicts(conflicts []Conflict) []Conflict {
	if conflicts == nil {
		return nil
	}
	clone := make([]Conflict, len(conflicts))
	for i, conflict := range conflicts {
		clone[i] = Conflict{
			Source:  conflict.Source,
			Targets: append([]string(nil), conflict.Targets...),
		}
	}
	return clone
}
