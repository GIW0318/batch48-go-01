// Package task plans the post processing chain of a scaled render target,
// inserting upscale and downscale passes only where the chain needs them.
package task

import (
	"errors"
	"math"
	"sync"
)

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

// handleGenerationBits reserves the low bits of a target handle for the
// per-generation slot. The remaining high bits carry the scale generation so
// that any handle, including a stale one, can identify the scale epoch that
// allocated it.
const handleGenerationBits = 24

type targetEntry struct {
	key        TargetKey
	width      int
	height     int
	generation uint64
	refs       int
}

// PostFxResolutionPlanner orders the post processing chain and owns the
// render target pool.
type PostFxResolutionPlanner struct {
	mu         sync.Mutex
	maxTargets int
	passLimit  int
	scale      float64
	chain      []Effect
	generation uint64

	targets     map[int]*targetEntry
	keyToHandle map[TargetKey]int
	nextSlot    uint32

	stats PlannerStats
}

// NewPostFxResolutionPlanner builds a planner with a target pool cap.
func NewPostFxResolutionPlanner(maxTargets int) *PostFxResolutionPlanner {
	return &PostFxResolutionPlanner{
		maxTargets:  maxTargets,
		passLimit:   32,
		scale:       1,
		targets:     make(map[int]*targetEntry),
		keyToHandle: make(map[TargetKey]int),
	}
}

// SetScale installs a new render scale and invalidates old targets.
//
// Only values in the half open interval (0, 1] are accepted. Every successful
// call bumps Generation and drops all previously allocated targets, so stale
// handles can no longer resolve a size.
func (p *PostFxResolutionPlanner) SetScale(scale float64) error {
	if math.IsNaN(scale) || scale <= 0 || scale > 1 {
		return ErrBadScale
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.scale = scale
	p.generation++
	p.invalidateTargetsLocked()
	p.stats.Rebuilds++
	return nil
}

// invalidateTargetsLocked drops every allocated target and its key index.
// Callers must hold p.mu.
func (p *PostFxResolutionPlanner) invalidateTargetsLocked() {
	p.targets = make(map[int]*targetEntry)
	p.keyToHandle = make(map[TargetKey]int)
	p.nextSlot = 0
}

// Scale returns the render scale currently in force.
func (p *PostFxResolutionPlanner) Scale() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.scale
}

// SetChain installs the enabled and disabled effect chain. The slice is
// copied and effect names must be present and unique.
func (p *PostFxResolutionPlanner) SetChain(effects []Effect) error {
	installed := make([]Effect, len(effects))
	seen := make(map[string]struct{}, len(effects))
	for i, effect := range effects {
		if effect.Name == "" {
			return ErrBadEffect
		}
		if _, duplicated := seen[effect.Name]; duplicated {
			return ErrDuplicateEffect
		}
		seen[effect.Name] = struct{}{}
		installed[i] = effect
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.chain = installed
	return nil
}

// Chain returns a copy of the installed effect chain.
func (p *PostFxResolutionPlanner) Chain() []Effect {
	p.mu.Lock()
	defer p.mu.Unlock()

	out := make([]Effect, len(p.chain))
	copy(out, p.chain)
	return out
}

// Classify reports the resolution class an effect runs at under the current scale.
//
// Native and forced-native needs always run at the native class. A half
// resolution need follows the render scale; at scale one its dimensions are
// the native dimensions and it is promoted to the native class.
func (p *PostFxResolutionPlanner) Classify(e Effect) ResolutionClass {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.classifyNeedLocked(e.Need)
}

func (p *PostFxResolutionPlanner) classifyNeedLocked(need ResolutionNeed) ResolutionClass {
	switch need {
	case NeedNative, NeedForced:
		return ClassNative
	case NeedHalf:
		if p.scale >= 1 {
			return ClassNative
		}
		return ClassHalf
	default:
		// An undeclared need is rendered at full resolution rather than
		// silently under sampling.
		return ClassNative
	}
}

// scaledSizeLocked returns the dimensions of the scaled (half resolution
// class) target for a native w by h frame. Rounding is applied to each axis
// independently and every axis stays at least one pixel.
func (p *PostFxResolutionPlanner) scaledSizeLocked(w, h int) (int, int) {
	scaledW := int(math.Round(p.scale * float64(w)))
	scaledH := int(math.Round(p.scale * float64(h)))
	if scaledW < 1 {
		scaledW = 1
	}
	if scaledH < 1 {
		scaledH = 1
	}
	return scaledW, scaledH
}

// Plan expands the chain into passes for the given native render size.
//
// Effects are visited in installed slice order, making the result stable for
// identical inputs. A single transition pass is inserted only when the
// resolution class changes, so adjacent effects of the same class never get a
// redundant upscale/downscale pair. Transition passes leave Effect empty.
func (p *PostFxResolutionPlanner) Plan(w, h int) ([]Pass, error) {
	if w <= 0 || h <= 0 {
		return nil, ErrBadSize
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	halfW, halfH := p.scaledSizeLocked(w, h)
	dimensions := func(class ResolutionClass) (int, int) {
		if class == ClassNative {
			return w, h
		}
		return halfW, halfH
	}

	passes := make([]Pass, 0, len(p.chain)*2)

	// The chain input is the scaled scene target. It is half class while the
	// render scale is below one and collapses to native at scale one.
	current := p.classifyNeedLocked(NeedHalf)

	for _, effect := range p.chain {
		if !effect.Enabled {
			continue
		}

		class := p.classifyNeedLocked(effect.Need)
		width, height := dimensions(class)

		if class != current {
			kind := PassUpscale
			if class == ClassHalf {
				kind = PassDownscale
			}
			transitionW, transitionH := dimensions(class)
			passes = append(passes, Pass{
				Effect: "",
				Kind:   kind,
				Class:  class,
				Width:  transitionW,
				Height: transitionH,
			})
			current = class
		}

		passes = append(passes, Pass{
			Effect: effect.Name,
			Kind:   PassEffect,
			Class:  class,
			Width:  width,
			Height: height,
		})
	}

	if len(passes) > p.passLimit {
		return nil, ErrPassLimit
	}

	p.stats.Passes += len(passes)
	return passes, nil
}

// AcquireTarget returns a handle for the requested target, reusing equal keys.
// Repeated acquires of one live key return the same handle and add a
// reference. Distinct live targets are capped by the pool capacity.
func (p *PostFxResolutionPlanner) AcquireTarget(k TargetKey) (int, error) {
	if k.Name == "" || k.Width <= 0 || k.Height <= 0 {
		return 0, ErrBadTargetKey
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.targets == nil {
		p.targets = make(map[int]*targetEntry)
	}
	if p.keyToHandle == nil {
		p.keyToHandle = make(map[TargetKey]int)
	}

	if handle, ok := p.keyToHandle[k]; ok {
		if entry, live := p.targets[handle]; live {
			entry.refs++
			p.stats.Reused++
			return handle, nil
		}
		// Defensive cleanup of a mapping whose entry is already gone.
		delete(p.keyToHandle, k)
	}

	if len(p.targets) >= p.maxTargets {
		return 0, ErrTargetPoolFull
	}

	p.nextSlot++
	handle := encodeTargetHandle(p.generation, p.nextSlot)
	p.targets[handle] = &targetEntry{
		key:        k,
		width:      k.Width,
		height:     k.Height,
		generation: p.generation,
		refs:       1,
	}
	p.keyToHandle[k] = handle
	p.stats.Targets++
	return handle, nil
}

// ReleaseTarget drops one reference to a target handle. When the last
// reference is released the target leaves the pool and its handle becomes
// unknown. Releasing an unknown, stale or already fully released handle
// reports ErrUnknownTarget.
func (p *PostFxResolutionPlanner) ReleaseTarget(handle int) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	entry, ok := p.targets[handle]
	if !ok {
		return ErrUnknownTarget
	}

	entry.refs--
	if entry.refs > 0 {
		return nil
	}

	delete(p.targets, handle)
	if current, mapped := p.keyToHandle[entry.key]; mapped && current == handle {
		delete(p.keyToHandle, entry.key)
	}
	return nil
}

// TargetSize reports the size a live target handle was allocated with.
// Released and scale-invalidated handles report ok=false.
func (p *PostFxResolutionPlanner) TargetSize(handle int) (int, int, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	entry, ok := p.targets[handle]
	if !ok {
		return 0, 0, false
	}
	return entry.width, entry.height, true
}

// SetPassLimit changes the hard cap of planned passes.
func (p *PostFxResolutionPlanner) SetPassLimit(n int) error {
	if n <= 0 {
		return ErrBadLimit
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.passLimit = n
	return nil
}

// Generation reports how many times the scale has been installed.
func (p *PostFxResolutionPlanner) Generation() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.generation
}

// TargetCount reports how many live targets the pool holds.
func (p *PostFxResolutionPlanner) TargetCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.targets)
}

// Stats returns a snapshot of the planner counters.
func (p *PostFxResolutionPlanner) Stats() PlannerStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stats
}

// TargetGeneration reports the scale generation a handle belongs to. For a
// live handle ok is true and the allocated generation is returned. For a
// stale or unknown handle ok is false, but the generation embedded in the
// handle is still decoded so old-size residue can be traced to its scale.
func (p *PostFxResolutionPlanner) TargetGeneration(handle int) (uint64, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if entry, ok := p.targets[handle]; ok {
		return entry.generation, true
	}
	return decodeTargetHandleGeneration(handle), false
}

func encodeTargetHandle(generation uint64, slot uint32) int {
	return int(generation<<handleGenerationBits) | int(slot)
}

func decodeTargetHandleGeneration(handle int) uint64 {
	if handle <= 0 {
		return 0
	}
	return uint64(handle) >> handleGenerationBits
}
