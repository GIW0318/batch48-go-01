package task

import (
	"errors"
	"sync"
)

// Sentinels reported by the threat table guard.
var (
	errNotImplemented = errors.New("not implemented")

	ErrInvalidRequest  = errors.New("invalid threat table request")
	ErrInvalidAmount   = errors.New("amount must be positive")
	ErrInvalidTaunt    = errors.New("invalid taunt window")
	ErrInvalidTransfer = errors.New("invalid transfer percentage")
	ErrUnknownPlayer   = errors.New("player not on the threat table")
	ErrUnknownTarget   = errors.New("target not on the threat table")
	ErrBadSnapshot     = errors.New("snapshot payload rejected")
)

// Options carries the threat conversion and switching rules.
type Options struct {
	DamageFactorPct int
	HealCasterPct   int
	HealTargetPct   int
	SwitchRatioPct  int
	StickMs         int64
	DecayPct        int
}

// Entry is one player row of a target's threat table.
type Entry struct {
	Player  string
	Threat  int64
	InRange bool
	Visible bool
}

type targetState struct {
	entries        map[string]*Entry
	sticky         string
	candidate      string
	candidateSince int64
	tauntPlayer    string
	tauntUntil     int64
}

// ThreatTableGuard keeps the threat table of every hostile target.
type ThreatTableGuard struct {
	mu      sync.Mutex
	opts    Options
	targets map[string]*targetState
}

// NewThreatTableGuard builds a guard with the given rules.
func NewThreatTableGuard(opts Options) *ThreatTableGuard {
	return &ThreatTableGuard{
		opts:    opts,
		targets: make(map[string]*targetState),
	}
}

// AddDamage converts damage into threat for one player.
func (g *ThreatTableGuard) AddDamage(target, player string, amount int64) error {
	return errNotImplemented
}

// AddHeal converts healing into threat split between healer and current target.
func (g *ThreatTableGuard) AddHeal(target, healer string, amount int64) error {
	return errNotImplemented
}

// Taunt forces the target to stay on the player until the given millisecond.
func (g *ThreatTableGuard) Taunt(target, player string, until int64) error {
	return errNotImplemented
}

// Current reports the player the target is locked on at the given millisecond.
func (g *ThreatTableGuard) Current(target string, at int64) (string, bool) {
	return "", false
}

// Transfer moves a percentage of one player's threat to another player.
func (g *ThreatTableGuard) Transfer(from, to string, pct int) error {
	return errNotImplemented
}

// Tick decays the entries of out of range or invisible players.
func (g *ThreatTableGuard) Tick(at int64) {
}

// Table returns a stable sorted copy of one target's threat table.
func (g *ThreatTableGuard) Table(target string) []Entry {
	return nil
}

// SetInRange marks whether a player counts as in range for the target.
func (g *ThreatTableGuard) SetInRange(target, player string, inRange bool) error {
	return errNotImplemented
}

// SetVisible marks whether a player is visible to the target.
func (g *ThreatTableGuard) SetVisible(target, player string, visible bool) error {
	return errNotImplemented
}

// Snapshot serialises the guard state.
func (g *ThreatTableGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the guard state from a snapshot payload.
func (g *ThreatTableGuard) Restore(payload []byte) error {
	return errNotImplemented
}
