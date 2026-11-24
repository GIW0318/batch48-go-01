// Package task plans the post processing chain of a scaled render target,
// inserting upscale and downscale passes only where the chain needs them.
package task

import "errors"

// errNotImplemented marks behaviour that still has to be provided.
var errNotImplemented = errors.New("not implemented")

// Errors reported by the post processing planner.
var (
	ErrBadScale        = errors.New("scale is outside the supported range")
	ErrBadEffect       = errors.New("effect name must not be empty")
	ErrDuplicateEffect = errors.New("effect name appears more than once")
	ErrBadSize         = errors.New("render size must be positive")
	ErrBadLimit        = errors.New("limit must be positive")
	ErrPassLimit       = errors.New("planned pass count exceeds the configured limit")
	ErrBadTargetKey    = errors.New("target key is incomplete")
	ErrTargetPoolFull  = errors.New("target pool is exhausted")
	ErrUnknownTarget   = errors.New("target handle is unknown")
)

// ResolutionClass is the resolution a pass actually runs at.
type ResolutionClass int

// Resolution classes.
const (
	ClassHalf ResolutionClass = iota
	ClassNative
)

// ResolutionNeed is the resolution an effect declares it requires.
type ResolutionNeed int

// Declared resolution needs.
const (
	NeedHalf ResolutionNeed = iota
	NeedNative
	NeedForced
)

// PassKind tells an effect pass from a resolution transition pass.
type PassKind int

// Pass kinds.
const (
	PassEffect PassKind = iota
	PassUpscale
	PassDownscale
)

// Effect is one post processing effect of the chain.
type Effect struct {
	Name    string
	Need    ResolutionNeed
	Enabled bool
}

// Pass is one executable step of the planned chain.
type Pass struct {
	Effect string
	Kind   PassKind
	Class  ResolutionClass
	Width  int
	Height int
}

// TargetKey identifies a render target in the pool.
type TargetKey struct {
	Name   string
	Width  int
	Height int
	Class  ResolutionClass
}

// PlannerStats summarises planner counters.
type PlannerStats struct {
	Passes   int
	Targets  int
	Rebuilds int
	Reused   int
}

// PostFxResolutionPlanner orders the post processing chain and owns the
// render target pool.
type PostFxResolutionPlanner struct {
	maxTargets int
	passLimit  int
	scale      float64
	chain      []Effect
	generation uint64
}

// NewPostFxResolutionPlanner builds a planner with a target pool cap.
func NewPostFxResolutionPlanner(maxTargets int) *PostFxResolutionPlanner {
	return &PostFxResolutionPlanner{maxTargets: maxTargets, passLimit: 32, scale: 1}
}

// SetScale installs a new render scale and invalidates old targets.
func (p *PostFxResolutionPlanner) SetScale(scale float64) error { return errNotImplemented }

// Scale returns the render scale currently in force.
func (p *PostFxResolutionPlanner) Scale() float64 { return 0 }

// SetChain installs the enabled and disabled effect chain.
func (p *PostFxResolutionPlanner) SetChain(effects []Effect) error { return errNotImplemented }

// Chain returns a copy of the installed effect chain.
func (p *PostFxResolutionPlanner) Chain() []Effect { return nil }

// Classify reports the resolution class an effect runs at under the current scale.
func (p *PostFxResolutionPlanner) Classify(e Effect) ResolutionClass { return ClassHalf }

// Plan expands the chain into passes for the given native render size.
func (p *PostFxResolutionPlanner) Plan(w, h int) ([]Pass, error) { return nil, errNotImplemented }

// AcquireTarget returns a handle for the requested target, reusing equal keys.
func (p *PostFxResolutionPlanner) AcquireTarget(k TargetKey) (int, error) {
	return 0, errNotImplemented
}

// ReleaseTarget drops one reference to a target handle.
func (p *PostFxResolutionPlanner) ReleaseTarget(h int) error { return errNotImplemented }

// TargetSize reports the size a live target handle was allocated with.
func (p *PostFxResolutionPlanner) TargetSize(h int) (int, int, bool) { return 0, 0, false }

// SetPassLimit changes the hard cap of planned passes.
func (p *PostFxResolutionPlanner) SetPassLimit(n int) error { return errNotImplemented }

// Generation reports how many times the scale has changed.
func (p *PostFxResolutionPlanner) Generation() uint64 { return 0 }

// TargetCount reports how many live targets the pool holds.
func (p *PostFxResolutionPlanner) TargetCount() int { return 0 }

// Stats returns a snapshot of the planner counters.
func (p *PostFxResolutionPlanner) Stats() PlannerStats { return PlannerStats{} }
