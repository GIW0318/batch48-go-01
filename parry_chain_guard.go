package task

import (
	"errors"
	"sync"
)

// Sentinels reported by the parry chain guard.
var (
	errNotImplemented = errors.New("not implemented")

	ErrInvalidRequest   = errors.New("invalid hit request")
	ErrInvalidModifiers = errors.New("modifier out of range")
	ErrInvalidImmunity  = errors.New("invalid immunity window")
	ErrBadSnapshot      = errors.New("snapshot payload rejected")
)

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
	return errNotImplemented
}

// Resolve settles one hit through the judgement chain.
func (g *ParryChainGuard) Resolve(req HitRequest) (Outcome, error) {
	return Outcome{}, errNotImplemented
}

// Batch settles several hits and returns one outcome per request.
func (g *ParryChainGuard) Batch(reqs []HitRequest) ([]Outcome, error) {
	return nil, errNotImplemented
}

// Immune makes one target untouchable until the given millisecond.
func (g *ParryChainGuard) Immune(id string, until int64) error {
	return errNotImplemented
}

// ChainOrder returns the fixed order of the judgement chain.
func (g *ParryChainGuard) ChainOrder() []string {
	return nil
}

// Explain returns the reason code of one outcome.
func (g *ParryChainGuard) Explain(o Outcome) string {
	return ""
}

// ImmuneUntil reports the immunity deadline registered for a target.
func (g *ParryChainGuard) ImmuneUntil(id string) (int64, bool) {
	return 0, false
}

// Stats returns a copy of the outcome counters.
func (g *ParryChainGuard) Stats() Stats {
	return Stats{}
}

// ResetStats clears the outcome counters.
func (g *ParryChainGuard) ResetStats() {
}

// Snapshot serialises the guard state.
func (g *ParryChainGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the guard state from a snapshot payload.
func (g *ParryChainGuard) Restore(payload []byte) error {
	return errNotImplemented
}
