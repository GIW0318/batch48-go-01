package task

import (
	"encoding/json"
	"errors"
	"sync"
)

// Sentinels reported by the parry chain guard.
var (
	ErrInvalidRequest   = errors.New("invalid hit request")
	ErrInvalidModifiers = errors.New("modifier out of range")
	ErrInvalidImmunity  = errors.New("invalid immunity window")
	ErrBadSnapshot      = errors.New("snapshot payload rejected")
)

// Reason codes emitted by the judgement chain.
const (
	ReasonImmune = "immune"
	ReasonMiss   = "miss"
	ReasonDodge  = "dodge"
	ReasonParry  = "parry"
	ReasonBlock  = "block"
	ReasonCrit   = "crit"
	ReasonHit    = "hit"
)

var chainOrder = []string{
	ReasonImmune,
	ReasonHit,
	ReasonDodge,
	ReasonParry,
	ReasonBlock,
	ReasonCrit,
}

// Options carries the judgement thresholds of the combat chain.
type Options struct {
	HitChancePct     int
	ParryKeepPct     int
	BlockReducePct   int
	CritMultPct      int
	BackstabBonusPct int
	MinDamage        int
}

// Modifiers are the per target avoidance ratings.
type Modifiers struct {
	HitBonus int
	Dodge    int
	Parry    int
	Block    int
	Crit     int
}

// HitRequest is one incoming hit with caller supplied deterministic rolls.
type HitRequest struct {
	Target     string
	Seq        int
	At         int64
	Roll       int
	DodgeRoll  int
	ParryRoll  int
	BlockRoll  int
	CritRoll   int
	BaseDamage int
	Behind     bool
}

// Outcome is the settlement of one hit.
type Outcome struct {
	Target  string
	Seq     int
	Reason  string
	Damage  int
	Blocked bool
	Crit    bool
}

// Stats counts every settled outcome.
type Stats struct {
	Total   int
	Hits    int
	Misses  int
	Dodges  int
	Parries int
	Blocks  int
	Crits   int
	Immunes int
}

// ParryChainGuard runs the fixed combat judgement chain.
type ParryChainGuard struct {
	mu     sync.Mutex
	opts   Options
	mods   map[string]Modifiers
	immune map[string]int64
	stats  Stats
}

// NewParryChainGuard builds a guard with the given thresholds.
func NewParryChainGuard(opts Options) *ParryChainGuard {
	return &ParryChainGuard{
		opts:   opts,
		mods:   make(map[string]Modifiers),
		immune: make(map[string]int64),
	}
}

// SetModifiers registers the avoidance ratings of one target.
func (g *ParryChainGuard) SetModifiers(id string, mod Modifiers) error {
	if mod.Dodge < 0 || mod.Dodge > 100 ||
		mod.Parry < 0 || mod.Parry > 100 ||
		mod.Block < 0 || mod.Block > 100 ||
		mod.Crit < 0 || mod.Crit > 100 ||
		mod.HitBonus < -100 || mod.HitBonus > 100 {
		return ErrInvalidModifiers
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.mods[id] = mod
	return nil
}

func validRoll(roll int) bool {
	return roll >= 0 && roll <= 99
}

func (g *ParryChainGuard) validateRequest(req HitRequest) error {
	if req.Target == "" || req.BaseDamage <= 0 {
		return ErrInvalidRequest
	}
	if !validRoll(req.Roll) || !validRoll(req.DodgeRoll) ||
		!validRoll(req.ParryRoll) || !validRoll(req.BlockRoll) ||
		!validRoll(req.CritRoll) {
		return ErrInvalidRequest
	}
	return nil
}

// settle runs the fixed judgement chain for one validated request.
// The caller must hold g.mu.
func (g *ParryChainGuard) settle(req HitRequest) Outcome {
	out := Outcome{Target: req.Target, Seq: req.Seq}
	mod := g.mods[req.Target]

	if until, ok := g.immune[req.Target]; ok && req.At < until {
		out.Reason = ReasonImmune
		g.stats.Immunes++
		g.stats.Total++
		return out
	}

	hitChance := g.opts.HitChancePct + mod.HitBonus
	if req.Behind {
		hitChance += g.opts.BackstabBonusPct
	}
	if hitChance < 0 {
		hitChance = 0
	}
	if hitChance > 100 {
		hitChance = 100
	}
	if req.Roll >= hitChance {
		out.Reason = ReasonMiss
		g.stats.Misses++
		g.stats.Total++
		return out
	}

	if !req.Behind {
		if req.DodgeRoll < mod.Dodge {
			out.Reason = ReasonDodge
			g.stats.Dodges++
			g.stats.Total++
			return out
		}
		if req.ParryRoll < mod.Parry {
			out.Reason = ReasonParry
			out.Damage = req.BaseDamage * g.opts.ParryKeepPct / 100
			if out.Damage < g.opts.MinDamage {
				out.Damage = g.opts.MinDamage
			}
			g.stats.Parries++
			g.stats.Total++
			return out
		}
		if req.BlockRoll < mod.Block {
			out.Blocked = true
			out.Damage = req.BaseDamage * (100 - g.opts.BlockReducePct) / 100
		}
	}
	if !out.Blocked {
		out.Damage = req.BaseDamage
	}

	if req.CritRoll < mod.Crit {
		out.Crit = true
		out.Damage = out.Damage * g.opts.CritMultPct / 100
	}

	if out.Damage < g.opts.MinDamage {
		out.Damage = g.opts.MinDamage
	}

	switch {
	case out.Crit:
		out.Reason = ReasonCrit
		g.stats.Hits++
		g.stats.Crits++
	case out.Blocked:
		out.Reason = ReasonBlock
		g.stats.Blocks++
	default:
		out.Reason = ReasonHit
		g.stats.Hits++
	}
	g.stats.Total++
	return out
}

// Resolve settles one hit through the judgement chain.
func (g *ParryChainGuard) Resolve(req HitRequest) (Outcome, error) {
	if err := g.validateRequest(req); err != nil {
		return Outcome{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.settle(req), nil
}

// Batch settles several hits and returns one outcome per request.
func (g *ParryChainGuard) Batch(reqs []HitRequest) ([]Outcome, error) {
	for _, req := range reqs {
		if err := g.validateRequest(req); err != nil {
			return nil, err
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	outs := make([]Outcome, len(reqs))
	for i, req := range reqs {
		outs[i] = g.settle(req)
	}
	return outs, nil
}

// Immune makes one target untouchable until the given millisecond.
func (g *ParryChainGuard) Immune(id string, until int64) error {
	if id == "" || until < 0 {
		return ErrInvalidImmunity
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.immune[id] = until
	return nil
}

// ChainOrder returns the fixed order of the judgement chain.
func (g *ParryChainGuard) ChainOrder() []string {
	out := make([]string, len(chainOrder))
	copy(out, chainOrder)
	return out
}

// Explain returns the reason code of one outcome.
func (g *ParryChainGuard) Explain(o Outcome) string {
	return o.Reason
}

// ImmuneUntil reports the immunity deadline registered for a target.
func (g *ParryChainGuard) ImmuneUntil(id string) (int64, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	until, ok := g.immune[id]
	return until, ok
}

// Stats returns a copy of the outcome counters.
func (g *ParryChainGuard) Stats() Stats {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.stats
}

// ResetStats clears the outcome counters.
func (g *ParryChainGuard) ResetStats() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.stats = Stats{}
}

// snapshotState is the serialised form of the guard state.
type snapshotState struct {
	Version int                  `json:"version"`
	Mods    map[string]Modifiers `json:"mods"`
	Immune  map[string]int64     `json:"immune"`
	Stats   Stats                `json:"stats"`
}

// Snapshot serialises the guard state.
func (g *ParryChainGuard) Snapshot() ([]byte, error) {
	g.mu.Lock()
	state := snapshotState{
		Version: 1,
		Mods:    make(map[string]Modifiers, len(g.mods)),
		Immune:  make(map[string]int64, len(g.immune)),
		Stats:   g.stats,
	}
	for id, mod := range g.mods {
		state.Mods[id] = mod
	}
	for id, until := range g.immune {
		state.Immune[id] = until
	}
	g.mu.Unlock()
	return json.Marshal(state)
}

// Restore replaces the guard state from a snapshot payload.
func (g *ParryChainGuard) Restore(payload []byte) error {
	var state snapshotState
	if err := json.Unmarshal(payload, &state); err != nil {
		return ErrBadSnapshot
	}
	if state.Version != 1 || state.Mods == nil || state.Immune == nil {
		return ErrBadSnapshot
	}
	for _, mod := range state.Mods {
		if mod.Dodge < 0 || mod.Dodge > 100 ||
			mod.Parry < 0 || mod.Parry > 100 ||
			mod.Block < 0 || mod.Block > 100 ||
			mod.Crit < 0 || mod.Crit > 100 ||
			mod.HitBonus < -100 || mod.HitBonus > 100 {
			return ErrBadSnapshot
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.mods = state.Mods
	g.immune = state.Immune
	g.stats = state.Stats
	return nil
}
