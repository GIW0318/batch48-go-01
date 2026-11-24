// Package task arbitrates the per frame light budget of a forward renderer:
// it scores the submitted lights, applies per category quotas, keeps a
// hysteresis band across frames and fades evicted lights out over time.
package task

import (
	"encoding/json"
	"errors"
	"math"
	"sort"
	"sync"
)

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

const (
	snapshotMagic  = "light-budget-arbiter"
	snapshotFormat = 1
)

// LightBudgetArbiter owns the candidate table and the fading state.
type LightBudgetArbiter struct {
	mu sync.RWMutex

	cfg ArbiterConfig

	lights []Light
	byID   map[int]Light

	frame   int
	version uint64

	last    Selection
	hasLast bool

	fading map[int]float64

	lastEvaluated int
	skips         uint64
}

type candidate struct {
	light       Light
	score       float64
	evaluated   bool
	wasShaded   bool
	wasShadowed bool
}

type fadeSnapshot struct {
	ID      int     `json:"id"`
	Elapsed float64 `json:"elapsed"`
}

type snapshotState struct {
	Magic        string         `json:"magic"`
	Format       int            `json:"format"`
	Config       ArbiterConfig  `json:"config"`
	Lights       []Light        `json:"lights"`
	Frame        int            `json:"frame"`
	Version      uint64         `json:"version"`
	HasSelection bool           `json:"has_selection"`
	Selection    Selection      `json:"selection"`
	Fading       []fadeSnapshot `json:"fading"`
	Evaluated    int            `json:"evaluated"`
	Skips        uint64         `json:"skips"`
}

// NewLightBudgetArbiter builds an arbiter with the given tuning.
func NewLightBudgetArbiter(cfg ArbiterConfig) *LightBudgetArbiter {
	return &LightBudgetArbiter{
		cfg:    cfg,
		byID:   make(map[int]Light),
		fading: make(map[int]float64),
	}
}

// Submit replaces the whole candidate table of the next frame.
func (a *LightBudgetArbiter) Submit(lights []Light) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	next := make([]Light, len(lights))
	seen := make(map[int]struct{}, len(lights))
	for i, light := range lights {
		if _, exists := seen[light.ID]; exists {
			return ErrDuplicateLight
		}
		if light.Coverage < 0 {
			return ErrBadBudget
		}
		seen[light.ID] = struct{}{}
		next[i] = light
	}

	byID := make(map[int]Light, len(next))
	for _, light := range next {
		byID[light.ID] = light
	}

	a.lights = next
	a.byID = byID
	a.lastEvaluated = 0
	return nil
}

// Select arbitrates the candidate table against one budget.
func (a *LightBudgetArbiter) Select(budget Budget) (Selection, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if err := validateBudget(budget); err != nil {
		return Selection{}, err
	}
	if len(a.lights) == 0 {
		return Selection{}, ErrNoLights
	}

	return a.selectLocked(budget), nil
}

func validateBudget(budget Budget) error {
	if budget.Shaded < 0 ||
		budget.Shadowed < 0 ||
		budget.Shadowed > budget.Shaded ||
		budget.MaxEvaluations < 0 {
		return ErrBadBudget
	}
	for _, quota := range budget.CategoryQuota {
		if quota < 0 {
			return ErrBadBudget
		}
	}
	return nil
}

func (a *LightBudgetArbiter) selectLocked(budget Budget) Selection {
	prevShaded := make(map[int]struct{}, 0)
	prevShadowed := make(map[int]struct{}, 0)
	if a.hasLast {
		prevShaded = make(map[int]struct{}, len(a.last.Shaded))
		for _, id := range a.last.Shaded {
			prevShaded[id] = struct{}{}
		}
		prevShadowed = make(map[int]struct{}, len(a.last.Shadowed))
		for _, id := range a.last.Shadowed {
			prevShadowed[id] = struct{}{}
		}
	}

	candidates := make([]*candidate, len(a.lights))
	for i, light := range a.lights {
		_, wasShaded := prevShaded[light.ID]
		_, wasShadowed := prevShadowed[light.ID]
		candidates[i] = &candidate{
			light:       light,
			score:       light.Coverage * light.Intensity,
			wasShaded:   wasShaded,
			wasShadowed: wasShadowed,
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		return rawBefore(candidates[i].score, candidates[i].light.ID,
			candidates[j].score, candidates[j].light.ID)
	})

	evaluated := budget.MaxEvaluations
	if evaluated > len(candidates) {
		evaluated = len(candidates)
	}
	for i := 0; i < evaluated; i++ {
		candidates[i].evaluated = true
	}

	evaluationOrder := append([]*candidate(nil), candidates[:evaluated]...)
	sort.Slice(evaluationOrder, func(i, j int) bool {
		return arbitrationBefore(evaluationOrder[i], evaluationOrder[j], a.cfg.Hysteresis)
	})

	selectionCapacity := budget.Shaded
	if selectionCapacity > len(candidates) {
		selectionCapacity = len(candidates)
	}
	selected := make(map[int]struct{}, selectionCapacity)
	categoryCount := make(map[string]int, len(budget.CategoryQuota))
	tryAdd := func(c *candidate) {
		if len(selected) >= budget.Shaded {
			return
		}
		if _, exists := selected[c.light.ID]; exists {
			return
		}
		quota := a.quotaFor(c.light.Category, budget)
		if quota <= 0 || categoryCount[c.light.Category] >= quota {
			return
		}
		selected[c.light.ID] = struct{}{}
		categoryCount[c.light.Category]++
	}

	// Candidates beyond the evaluation cap keep their previous conclusion:
	// incumbents stay selected, everyone else is not re-considered.
	for _, c := range candidates {
		if !c.evaluated && c.wasShaded {
			tryAdd(c)
		}
	}
	for _, c := range evaluationOrder {
		tryAdd(c)
	}

	selectedOrder := make([]*candidate, 0, len(selected))
	for _, c := range candidates {
		if _, exists := selected[c.light.ID]; exists {
			selectedOrder = append(selectedOrder, c)
		}
	}

	shadowCapacity := budget.Shadowed
	if shadowCapacity > len(selectedOrder) {
		shadowCapacity = len(selectedOrder)
	}
	shadowCandidates := make([]*candidate, 0, len(selectedOrder))
	for _, c := range selectedOrder {
		if c.light.ShadowCapable {
			shadowCandidates = append(shadowCandidates, c)
		}
	}
	sort.Slice(shadowCandidates, func(i, j int) bool {
		return shadowBefore(shadowCandidates[i], shadowCandidates[j], a.cfg.Hysteresis)
	})
	shadowed := make(map[int]struct{}, shadowCapacity)
	for i := 0; i < len(shadowCandidates) && i < budget.Shadowed; i++ {
		shadowed[shadowCandidates[i].light.ID] = struct{}{}
	}

	for id := range prevShaded {
		if _, stillSelected := selected[id]; stillSelected {
			continue
		}
		if _, alreadyFading := a.fading[id]; !alreadyFading {
			a.fading[id] = 0
		}
	}

	fadingIDs := a.fadingIDsLocked()
	next := Selection{
		Shaded:   make([]int, 0, len(selectedOrder)),
		Shadowed: make([]int, 0, len(shadowed)),
		Fading:   fadingIDs,
		Frame:    a.frame,
	}
	for _, c := range selectedOrder {
		next.Shaded = append(next.Shaded, c.light.ID)
	}
	for _, c := range selectedOrder {
		if _, ok := shadowed[c.light.ID]; ok {
			next.Shadowed = append(next.Shadowed, c.light.ID)
		}
	}

	if !a.hasLast || !sameSelectionMembership(a.last, next) {
		a.version++
	}
	next.Version = a.version

	a.last = cloneSelection(next)
	a.hasLast = true
	a.lastEvaluated = evaluated
	a.skips += uint64(len(candidates) - evaluated)
	return cloneSelection(next)
}

func (a *LightBudgetArbiter) quotaFor(category string, budget Budget) int {
	if quota, ok := budget.CategoryQuota[category]; ok {
		return quota
	}
	return a.cfg.DefaultQuota
}

func rawBefore(scoreA float64, idA int, scoreB float64, idB int) bool {
	nanA := math.IsNaN(scoreA)
	nanB := math.IsNaN(scoreB)
	if nanA || nanB {
		if nanA && nanB {
			return idA < idB
		}
		return !nanA
	}
	if scoreA != scoreB {
		return scoreA > scoreB
	}
	return idA < idB
}

func arbitrationBefore(a, b *candidate, hysteresis float64) bool {
	scoreA := a.score
	scoreB := b.score
	if a.wasShaded {
		scoreA += hysteresis
	}
	if b.wasShaded {
		scoreB += hysteresis
	}

	nanA := math.IsNaN(scoreA)
	nanB := math.IsNaN(scoreB)
	if nanA || nanB {
		if nanA && nanB {
			if a.wasShaded != b.wasShaded {
				return a.wasShaded
			}
			return a.light.ID < b.light.ID
		}
		return !nanA
	}
	if scoreA != scoreB {
		return scoreA > scoreB
	}
	if a.wasShaded != b.wasShaded {
		return a.wasShaded
	}
	return a.light.ID < b.light.ID
}

func shadowBefore(a, b *candidate, hysteresis float64) bool {
	scoreA := a.score
	scoreB := b.score
	if a.wasShadowed {
		scoreA += hysteresis
	}
	if b.wasShadowed {
		scoreB += hysteresis
	}

	nanA := math.IsNaN(scoreA)
	nanB := math.IsNaN(scoreB)
	if nanA || nanB {
		if nanA && nanB {
			if a.wasShadowed != b.wasShadowed {
				return a.wasShadowed
			}
			return a.light.ID < b.light.ID
		}
		return !nanA
	}
	if scoreA != scoreB {
		return scoreA > scoreB
	}
	if a.wasShadowed != b.wasShadowed {
		return a.wasShadowed
	}
	return a.light.ID < b.light.ID
}

// AdvanceFrame moves the clock forward and ages fading lights.
func (a *LightBudgetArbiter) AdvanceFrame(dt float64) error {
	if !(dt > 0) {
		return ErrBadDelta
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.frame++
	fadeSeconds := a.cfg.FadeSeconds
	for id, elapsed := range a.fading {
		elapsed += dt
		if fadeSeconds <= 0 || elapsed >= fadeSeconds {
			delete(a.fading, id)
			continue
		}
		a.fading[id] = elapsed
	}

	if a.hasLast {
		a.last.Fading = a.fadingIDsLocked()
	}
	return nil
}

// Explain reports the last known arbitration reason of one light.
func (a *LightBudgetArbiter) Explain(id int) (Reason, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	light, ok := a.byID[id]
	if !ok {
		return Reason{}, ErrUnknownLight
	}

	reason := Reason{
		ID:       id,
		Category: light.Category,
		Score:    light.Coverage * light.Intensity,
	}
	if a.hasLast {
		reason.Shaded = containsInt(a.last.Shaded, id)
		reason.Shadowed = containsInt(a.last.Shadowed, id)
	}
	_, reason.Fading = a.fading[id]

	switch {
	case reason.Shaded && reason.Shadowed && reason.Fading:
		reason.Note = "selected for shading and shadows; fade still in progress"
	case reason.Shaded && reason.Shadowed:
		reason.Note = "selected for shading and shadows"
	case reason.Shaded && reason.Fading:
		reason.Note = "selected for shading; fade still in progress"
	case reason.Shaded:
		reason.Note = "selected for shading"
	case reason.Fading:
		reason.Note = "evicted; fading out"
	case !a.hasLast:
		reason.Note = "waiting for the first arbitration"
	default:
		reason.Note = "not selected by the current budget"
	}
	return reason, nil
}

// SetHysteresis changes the score margin that protects incumbents.
func (a *LightBudgetArbiter) SetHysteresis(margin float64) error {
	if math.IsNaN(margin) || margin < 0 {
		return ErrBadHysteresis
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg.Hysteresis != margin {
		a.cfg.Hysteresis = margin
	}
	return nil
}

// SetFadeSeconds changes how long an evicted light keeps fading.
func (a *LightBudgetArbiter) SetFadeSeconds(seconds float64) error {
	if math.IsNaN(seconds) || seconds < 0 {
		return ErrBadFade
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg.FadeSeconds != seconds {
		a.cfg.FadeSeconds = seconds
	}
	return nil
}

// Frame reports the current frame number.
func (a *LightBudgetArbiter) Frame() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.frame
}

// LastSelection reports the most recent successful selection.
func (a *LightBudgetArbiter) LastSelection() (Selection, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.hasLast {
		return Selection{}, false
	}
	return cloneSelection(a.last), true
}

// FadingLights lists the lights that are still fading out.
func (a *LightBudgetArbiter) FadingLights() []int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.fadingIDsLocked()
}

// Stats reports the live counters of the arbiter.
func (a *LightBudgetArbiter) Stats() ArbiterStats {
	a.mu.RLock()
	defer a.mu.RUnlock()

	stats := ArbiterStats{
		Candidates: len(a.lights),
		Fading:     len(a.fading),
		Frames:     a.frame,
		Evaluated:  a.lastEvaluated,
		Skips:      a.skips,
	}
	if a.hasLast {
		stats.Selected = len(a.last.Shaded)
		stats.Shadowed = len(a.last.Shadowed)
	}
	return stats
}

// Snapshot serialises candidates, selection, fade progress and frame number.
func (a *LightBudgetArbiter) Snapshot() ([]byte, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	state := snapshotState{
		Magic:        snapshotMagic,
		Format:       snapshotFormat,
		Config:       a.cfg,
		Lights:       cloneLights(a.lights),
		Frame:        a.frame,
		Version:      a.version,
		HasSelection: a.hasLast,
		Selection:    cloneSelection(a.last),
		Fading:       make([]fadeSnapshot, 0, len(a.fading)),
		Evaluated:    a.lastEvaluated,
		Skips:        a.skips,
	}
	if !a.hasLast {
		state.Selection = Selection{}
	}

	ids := a.fadingIDsLocked()
	for _, id := range ids {
		state.Fading = append(state.Fading, fadeSnapshot{ID: id, Elapsed: a.fading[id]})
	}
	return json.Marshal(state)
}

// Restore rebuilds the arbiter from a snapshot, either completely or not at all.
func (a *LightBudgetArbiter) Restore(payload []byte) error {
	var state snapshotState
	if err := json.Unmarshal(payload, &state); err != nil {
		return ErrBadSnapshot
	}
	if state.Magic != snapshotMagic || state.Format != snapshotFormat {
		return ErrBadSnapshot
	}
	if math.IsNaN(state.Config.Hysteresis) || state.Config.Hysteresis < 0 ||
		math.IsNaN(state.Config.FadeSeconds) || state.Config.FadeSeconds < 0 {
		return ErrBadSnapshot
	}
	if state.Frame < 0 || state.Evaluated < 0 {
		return ErrBadSnapshot
	}

	byID := make(map[int]Light, len(state.Lights))
	for _, light := range state.Lights {
		if light.Coverage < 0 {
			return ErrBadSnapshot
		}
		if _, exists := byID[light.ID]; exists {
			return ErrBadSnapshot
		}
		byID[light.ID] = light
	}

	fading := make(map[int]float64, len(state.Fading))
	for _, entry := range state.Fading {
		if math.IsNaN(entry.Elapsed) || entry.Elapsed < 0 {
			return ErrBadSnapshot
		}
		if _, exists := fading[entry.ID]; exists {
			return ErrBadSnapshot
		}
		fading[entry.ID] = entry.Elapsed
	}

	if state.HasSelection {
		if err := validateSelection(state.Selection); err != nil {
			return ErrBadSnapshot
		}
	}

	lights := cloneLights(state.Lights)
	selection := cloneSelection(state.Selection)

	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg = state.Config
	a.lights = lights
	a.byID = byID
	a.frame = state.Frame
	a.version = state.Version
	a.last = selection
	a.hasLast = state.HasSelection
	a.fading = fading
	a.lastEvaluated = state.Evaluated
	a.skips = state.Skips
	return nil
}

func validateSelection(selection Selection) error {
	if selection.Frame < 0 {
		return ErrBadSnapshot
	}
	shaded, err := uniqueIDs(selection.Shaded)
	if err != nil {
		return err
	}
	shadowed, err := uniqueIDs(selection.Shadowed)
	if err != nil {
		return err
	}
	if _, err := uniqueIDs(selection.Fading); err != nil {
		return err
	}
	for id := range shadowed {
		if _, ok := shaded[id]; !ok {
			return ErrBadSnapshot
		}
	}
	return nil
}

func uniqueIDs(ids []int) (map[int]struct{}, error) {
	set := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		if _, exists := set[id]; exists {
			return nil, ErrBadSnapshot
		}
		set[id] = struct{}{}
	}
	return set, nil
}

// Reset clears candidates, selection, fading state and counters.
func (a *LightBudgetArbiter) Reset() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.lights = nil
	a.byID = make(map[int]Light)
	a.frame = 0
	a.version = 0
	a.last = Selection{}
	a.hasLast = false
	a.fading = make(map[int]float64)
	a.lastEvaluated = 0
	a.skips = 0
	return nil
}

func (a *LightBudgetArbiter) fadingIDsLocked() []int {
	ids := make([]int, 0, len(a.fading))
	for id := range a.fading {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

func sameSelectionMembership(a, b Selection) bool {
	return sameInts(a.Shaded, b.Shaded) &&
		sameInts(a.Shadowed, b.Shadowed) &&
		sameInts(a.Fading, b.Fading)
}

func sameInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsInt(ids []int, target int) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

func cloneSelection(selection Selection) Selection {
	return Selection{
		Shaded:   cloneInts(selection.Shaded),
		Shadowed: cloneInts(selection.Shadowed),
		Fading:   cloneInts(selection.Fading),
		Frame:    selection.Frame,
		Version:  selection.Version,
	}
}

func cloneInts(ids []int) []int {
	if ids == nil {
		return nil
	}
	cloned := make([]int, len(ids))
	copy(cloned, ids)
	return cloned
}

func cloneLights(lights []Light) []Light {
	if lights == nil {
		return nil
	}
	cloned := make([]Light, len(lights))
	copy(cloned, lights)
	return cloned
}
