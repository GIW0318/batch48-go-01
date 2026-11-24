// Package task arbitrates the per frame light budget of a forward renderer:
// it scores the submitted lights, applies per category quotas, keeps a
// hysteresis band across frames and fades evicted lights out over time.
package task

import (
	"errors"
	"sync"
)

// errNotImplemented marks behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// Light is one candidate light of the current frame.
type Light struct {
	ID            int
	Category      string
	Coverage      float64
	Intensity     float64
	ShadowCapable bool
}

// Budget carries the per frame limits requested by the renderer.
type Budget struct {
	Shaded         int
	Shadowed       int
	MaxEvaluations int
	CategoryQuota  map[string]int
}

// Selection is the arbitration result of one frame.
type Selection struct {
	Shaded   []int
	Shadowed []int
	Fading   []int
	Frame    int
	Version  uint64
}

// Reason explains why one light ended up in or out of the selection.
type Reason struct {
	ID       int
	Category string
	Score    float64
	Shaded   bool
	Shadowed bool
	Fading   bool
	Note     string
}

// ArbiterConfig carries the hysteresis and fade tuning.
type ArbiterConfig struct {
	Hysteresis   float64
	FadeSeconds  float64
	DefaultQuota int
}

// ArbiterStats reports the live counters of the arbiter.
type ArbiterStats struct {
	Candidates int
	Selected   int
	Shadowed   int
	Fading     int
	Frames     int
	Evaluated  int
	Skips      uint64
}

// Errors reported by the arbiter.
var (
	ErrDuplicateLight = errors.New("light id is submitted twice")
	ErrUnknownLight   = errors.New("light id is not in the candidate table")
	ErrNoLights       = errors.New("no light has been submitted")
	ErrBadBudget      = errors.New("budget values are out of range")
	ErrBadDelta       = errors.New("frame delta must be positive")
	ErrBadHysteresis  = errors.New("hysteresis must not be negative")
	ErrBadFade        = errors.New("fade duration must be positive")
	ErrBadSnapshot    = errors.New("snapshot payload is invalid")
)

// LightBudgetArbiter owns the candidate table and the fading state.
type LightBudgetArbiter struct {
	mu  sync.RWMutex
	cfg ArbiterConfig
}

// NewLightBudgetArbiter builds an arbiter with the given tuning.
func NewLightBudgetArbiter(cfg ArbiterConfig) *LightBudgetArbiter {
	return &LightBudgetArbiter{cfg: cfg}
}

// Submit replaces the whole candidate table of the next frame.
func (a *LightBudgetArbiter) Submit(lights []Light) error {
	return errNotImplemented
}

// Select arbitrates the candidate table against one budget.
func (a *LightBudgetArbiter) Select(budget Budget) (Selection, error) {
	return Selection{}, errNotImplemented
}

// AdvanceFrame moves the clock forward and ages fading lights.
func (a *LightBudgetArbiter) AdvanceFrame(dt float64) error {
	return errNotImplemented
}

// Explain reports the last known arbitration reason of one light.
func (a *LightBudgetArbiter) Explain(id int) (Reason, error) {
	return Reason{}, errNotImplemented
}

// SetHysteresis changes the score margin that protects incumbents.
func (a *LightBudgetArbiter) SetHysteresis(margin float64) error {
	return errNotImplemented
}

// SetFadeSeconds changes how long an evicted light keeps fading.
func (a *LightBudgetArbiter) SetFadeSeconds(seconds float64) error {
	return errNotImplemented
}

// Frame reports the current frame number.
func (a *LightBudgetArbiter) Frame() int {
	return 0
}

// LastSelection reports the most recent successful selection.
func (a *LightBudgetArbiter) LastSelection() (Selection, bool) {
	return Selection{}, false
}

// FadingLights lists the lights that are still fading out.
func (a *LightBudgetArbiter) FadingLights() []int {
	return nil
}

// Stats reports the live counters of the arbiter.
func (a *LightBudgetArbiter) Stats() ArbiterStats {
	return ArbiterStats{}
}

// Snapshot serialises candidates, selection, fade progress and frame number.
func (a *LightBudgetArbiter) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore rebuilds the arbiter from a snapshot, either completely or not at all.
func (a *LightBudgetArbiter) Restore(payload []byte) error {
	return errNotImplemented
}

// Reset clears candidates, selection, fading state and counters.
func (a *LightBudgetArbiter) Reset() error {
	return errNotImplemented
}
