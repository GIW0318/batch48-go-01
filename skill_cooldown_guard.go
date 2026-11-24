package task

import (
	"errors"
	"sync"
)

// Sentinels reported by the skill cooldown guard.
var (
	errNotImplemented = errors.New("not implemented")

	ErrInvalidSkillDef = errors.New("invalid skill definition")
	ErrDuplicateSkill  = errors.New("skill already defined")
	ErrUnknownSkill    = errors.New("skill not defined")
	ErrNotReady        = errors.New("skill is not ready")
	ErrGroupCooling    = errors.New("shared cooldown group is cooling")
	ErrInvalidReduce   = errors.New("invalid cooldown reduction")
	ErrBadSnapshot     = errors.New("snapshot payload rejected")
)

// Options carries the hard limits of the cooldown system.
type Options struct {
	MaxReducePct    int
	GroupCooldownMs int64
}

// SkillDef is one registered skill.
type SkillDef struct {
	Name       string
	CooldownMs int64
	Charges    int
	RechargeMs int64
	Group      string
}

// CastResult describes one accepted cast.
type CastResult struct {
	Skill        string
	At           int64
	Charges      int
	ChargesMax   int
	CooldownEnds int64
	Group        string
}

type skillState struct {
	spec      SkillDef
	charges   int
	chargeAt  int64
	reducePct int
}

type groupState struct {
	readyAt int64
}

// SkillCooldownGuard keeps the cooldown timers of every skill of one character.
type SkillCooldownGuard struct {
	mu     sync.Mutex
	opts   Options
	skills map[string]*skillState
	groups map[string]*groupState
	casts  int
}

// NewSkillCooldownGuard builds a guard with the given limits.
func NewSkillCooldownGuard(opts Options) *SkillCooldownGuard {
	return &SkillCooldownGuard{
		opts:   opts,
		skills: make(map[string]*skillState),
		groups: make(map[string]*groupState),
	}
}

// Define registers a skill definition.
func (g *SkillCooldownGuard) Define(spec SkillDef) error {
	return errNotImplemented
}

// Cast consumes one charge of the skill at the caller supplied millisecond.
func (g *SkillCooldownGuard) Cast(skill string, at int64) (CastResult, error) {
	return CastResult{}, errNotImplemented
}

// Ready reports whether the skill can be cast at the given millisecond.
func (g *SkillCooldownGuard) Ready(skill string, at int64) bool {
	return false
}

// Charges reports the current charge count at the given millisecond.
func (g *SkillCooldownGuard) Charges(skill string, at int64) int {
	return 0
}

// Reduce accumulates a cooldown reduction percentage on one skill.
func (g *SkillCooldownGuard) Reduce(skill string, pct int) error {
	return errNotImplemented
}

// Reset restores the skill to full charges and clears its personal cooldown.
func (g *SkillCooldownGuard) Reset(skill string) error {
	return errNotImplemented
}

// CooldownRemaining reports the milliseconds until the skill is castable again.
func (g *SkillCooldownGuard) CooldownRemaining(skill string, at int64) int64 {
	return 0
}

// GroupRemaining reports the milliseconds left on a shared cooldown group.
func (g *SkillCooldownGuard) GroupRemaining(group string, at int64) int64 {
	return 0
}

// Skills returns every registered skill name in ascending order.
func (g *SkillCooldownGuard) Skills() []string {
	return nil
}

// Snapshot serialises the guard state.
func (g *SkillCooldownGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the guard state from a snapshot payload.
func (g *SkillCooldownGuard) Restore(payload []byte) error {
	return errNotImplemented
}
