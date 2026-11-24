// Package task maps a source skeleton onto a target skeleton and retargets
// poses while keeping rest pose differences and missing bones explicit.
package task

import "errors"

// errNotImplemented marks behaviour that still has to be provided.
var errNotImplemented = errors.New("not implemented")

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

// SkeletonRetargeter builds bone maps and retargets poses onto them.
type SkeletonRetargeter struct {
	maxMappings int
	restDelta   Vec3
}

// NewSkeletonRetargeter returns a retargeter capped at maxMappings pairs.
func NewSkeletonRetargeter(maxMappings int) *SkeletonRetargeter {
	return &SkeletonRetargeter{maxMappings: maxMappings}
}

// SetMaxMappings changes the hard cap of one bone map.
func (r *SkeletonRetargeter) SetMaxMappings(n int) error { return errNotImplemented }

// MaxMappings returns the current mapping cap.
func (r *SkeletonRetargeter) MaxMappings() int { return 0 }

// BuildMap matches source bones to target bones by role first and name second.
func (r *SkeletonRetargeter) BuildMap(src, dst Skeleton) (*BoneMap, error) {
	return nil, errNotImplemented
}

// Map returns the current bone map.
func (r *SkeletonRetargeter) Map() (*BoneMap, bool) { return nil, false }

// Retarget converts a source pose into the target skeleton pose.
func (r *SkeletonRetargeter) Retarget(p Pose) (Pose, error) { return Pose{}, errNotImplemented }

// SetRestDelta sets the constant rotation offset added after retargeting.
func (r *SkeletonRetargeter) SetRestDelta(v Vec3) error { return errNotImplemented }

// RestDelta returns the current rotation offset.
func (r *SkeletonRetargeter) RestDelta() Vec3 { return Vec3{} }

// Missing reports the fallback strategy registered for a source bone.
func (r *SkeletonRetargeter) Missing(name string) (Fallback, bool) { return Fallback{}, false }

// Mappings returns the current pairs ordered by source bone name.
func (r *SkeletonRetargeter) Mappings() []Mapping { return nil }

// Conflicts returns the registered one to many matches ordered by source name.
func (r *SkeletonRetargeter) Conflicts() []Conflict { return nil }

// Normalize folds a bone name into its canonical matching form.
func (r *SkeletonRetargeter) Normalize(name string) string { return "" }

// Reset drops the current bone map and rest delta.
func (r *SkeletonRetargeter) Reset() {}
