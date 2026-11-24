package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// Sentinels reported by the combo counter guard.
var (
	ErrInvalidRequest = errors.New("invalid combo request")
	ErrInvalidReason  = errors.New("unknown break reason")
	ErrInvalidExtend  = errors.New("extend must be positive")
	ErrInvalidControl = errors.New("invalid control window")
	ErrUnknownPlayer  = errors.New("player has no combo state")
	ErrBadSnapshot    = errors.New("snapshot payload rejected")
)

// Options carries the combo window and bonus curve of one ruleset.
type Options struct {
	WindowMs     int64
	MaxCombo     int
	BonusStepPct int
	MaxBonusPct  int
}

// Combo is the combo state of one player as seen by the caller.
type Combo struct {
	Player    string
	Target    string
	Count     int
	Bonus     int
	ExpiresAt int64
	LastHitAt int64
}

// comboState is the mutable chain of exactly one player. The embedded mutex is
// only ever entered for that player, so independent players never contend on a
// shared per-player lock.
type comboState struct {
	mu sync.Mutex

	target          string
	count           int
	expiresAt       int64
	lastHitAt       int64
	lastSeenAt      int64
	extended        int64
	controlledUntil int64
	controlFrom     int64
	lastBreak       string
}

const (
	snapshotMagic   = "gsb.batch48/combo-counter-guard/v1"
	snapshotVersion = 1
)

type snapshotEnvelope struct {
	Magic   string          `json:"magic"`
	Version int             `json:"version"`
	Players []snapshotEntry `json:"players"`
}

type snapshotEntry struct {
	Player          string `json:"player"`
	Target          string `json:"target"`
	Count           int    `json:"count"`
	ExpiresAt       int64  `json:"expires_at"`
	LastHitAt       int64  `json:"last_hit_at"`
	LastSeenAt      int64  `json:"last_seen_at"`
	Extended        int64  `json:"extended"`
	ControlledUntil int64  `json:"controlled_until"`
	ControlFrom     int64  `json:"control_from"`
	LastBreak       string `json:"last_break,omitempty"`
}

// ComboCounterGuard keeps one combo chain per player.
type ComboCounterGuard struct {
	opts Options

	// life is a snapshot/restart barrier. Ordinary operations only take the
	// shared read lock, which never serialises distinct players; Snapshot and
	// Restore take the exclusive write lock so they are mutually exclusive
	// with every in-flight operation.
	life    sync.RWMutex
	players sync.Map // string(player) -> *comboState
}

// NewComboCounterGuard builds a guard with the given ruleset.
func NewComboCounterGuard(opts Options) *ComboCounterGuard {
	return &ComboCounterGuard{opts: opts}
}

var breakReasons = map[string]struct{}{
	"timeout": {},
	"stun":    {},
	"damage":  {},
}

func bonusFor(count, stepPct, capPct int) int {
	if count <= 0 {
		return 0
	}
	bonus := count * stepPct
	if bonus > capPct {
		bonus = capPct
	}
	return bonus
}

func resetComboLocked(s *comboState) {
	s.target = ""
	s.count = 0
	s.expiresAt = 0
	s.lastHitAt = 0
	s.extended = 0
	s.controlledUntil = 0
	s.controlFrom = 0
}

// refreshLocked records the latest logical timestamp and clears a chain once
// its window has elapsed. The deadline millisecond itself is still inside the
// window, so only a strictly later timestamp is an expiry. A running control
// window freezes natural expiry instead of breaking the chain.
func (g *ComboCounterGuard) refreshLocked(s *comboState, at int64) {
	if at > s.lastSeenAt {
		s.lastSeenAt = at
	}
	if s.count <= 0 {
		return
	}
	frozenByControl := s.controlledUntil > 0 && at < s.controlledUntil
	if at > s.expiresAt && !frozenByControl {
		resetComboLocked(s)
	}
}

func (g *ComboCounterGuard) comboViewLocked(player string, s *comboState) Combo {
	return Combo{
		Player:    player,
		Target:    s.target,
		Count:     s.count,
		Bonus:     bonusFor(s.count, g.opts.BonusStepPct, g.opts.MaxBonusPct),
		ExpiresAt: s.expiresAt,
		LastHitAt: s.lastHitAt,
	}
}

func (g *ComboCounterGuard) stateOrCreate(player string) *comboState {
	value, _ := g.players.LoadOrStore(player, &comboState{})
	return value.(*comboState)
}

// Hit records one landed hit and returns the resulting combo.
func (g *ComboCounterGuard) Hit(player, target string, at int64) (Combo, error) {
	if player == "" || target == "" || at < 0 {
		return Combo{}, ErrInvalidRequest
	}

	g.life.RLock()
	defer g.life.RUnlock()

	s := g.stateOrCreate(player)
	s.mu.Lock()
	defer s.mu.Unlock()

	g.refreshLocked(s, at)

	// Hits inside an active control window neither add a segment nor break
	// the chain. The deadline was shifted by the control duration when
	// Control was registered, so once control ends the same window resumes.
	if s.controlledUntil > 0 && at < s.controlledUntil {
		return g.comboViewLocked(player, s), nil
	}
	if s.controlledUntil != 0 && at >= s.controlledUntil {
		s.controlledUntil = 0
		s.controlFrom = 0
	}

	// Increment by one per serialized call. A positive MaxCombo is the hard
	// segment cap; a non-positive value leaves the chain uncapped.
	if g.opts.MaxCombo <= 0 || s.count < g.opts.MaxCombo {
		s.count++
	}
	s.target = target
	s.lastHitAt = at
	s.expiresAt = at + g.opts.WindowMs + s.extended

	return g.comboViewLocked(player, s), nil
}

// Break ends the combo of one player for the given reason.
func (g *ComboCounterGuard) Break(player, reason string, at int64) error {
	if _, ok := breakReasons[reason]; !ok {
		return ErrInvalidReason
	}

	g.life.RLock()
	defer g.life.RUnlock()

	value, ok := g.players.Load(player)
	if !ok {
		return ErrUnknownPlayer
	}
	s := value.(*comboState)
	s.mu.Lock()
	defer s.mu.Unlock()

	resetComboLocked(s)
	s.lastSeenAt = at
	s.lastBreak = reason
	return nil
}

// Bonus reports the damage bonus percentage of one player.
func (g *ComboCounterGuard) Bonus(player string, at int64) int {
	g.life.RLock()
	defer g.life.RUnlock()

	value, ok := g.players.Load(player)
	if !ok {
		return 0
	}
	s := value.(*comboState)
	s.mu.Lock()
	defer s.mu.Unlock()

	g.refreshLocked(s, at)
	return bonusFor(s.count, g.opts.BonusStepPct, g.opts.MaxBonusPct)
}

// Extend lengthens the combo window of one player.
func (g *ComboCounterGuard) Extend(player string, ms int64) error {
	if ms <= 0 {
		return ErrInvalidExtend
	}

	g.life.RLock()
	defer g.life.RUnlock()

	s := g.stateOrCreate(player)
	s.mu.Lock()
	defer s.mu.Unlock()

	// extended is the cumulative granted time and is reapplied whenever a
	// hit re-anchors the window. Push the live deadline immediately too.
	s.extended += ms
	if s.expiresAt > 0 {
		s.expiresAt += ms
	}
	return nil
}

// Transfer moves the combo chain of one player onto a new target. The segment
// count is preserved and the window is re-anchored from the last landed hit.
func (g *ComboCounterGuard) Transfer(player, oldTarget, newTarget string) error {
	g.life.RLock()
	defer g.life.RUnlock()

	value, ok := g.players.Load(player)
	if !ok {
		return ErrUnknownPlayer
	}
	s := value.(*comboState)
	s.mu.Lock()
	defer s.mu.Unlock()

	s.target = newTarget
	s.expiresAt = s.lastHitAt + g.opts.WindowMs + s.extended
	return nil
}

// Control registers a crowd control window on one player. The combo window is
// paused for the control duration rather than allowed to expire.
func (g *ComboCounterGuard) Control(player string, until int64) error {
	if until < 0 {
		return ErrInvalidControl
	}

	g.life.RLock()
	defer g.life.RUnlock()

	s := g.stateOrCreate(player)
	s.mu.Lock()
	defer s.mu.Unlock()

	// Control is not given its own timestamp, so the most recent logical time
	// observed for this player is the start of the control window. Shift the
	// deadline by the paused duration now; refreshLocked then keeps the chain
	// alive until control ends.
	now := s.lastSeenAt
	if s.controlledUntil > now {
		// An already registered control window is being extended.
		if until > s.controlledUntil {
			s.expiresAt += until - s.controlledUntil
		}
	} else if until > now {
		s.expiresAt += until - now
	}
	s.controlFrom = now
	s.controlledUntil = until
	return nil
}

// Count reports the current combo segment count of one player.
func (g *ComboCounterGuard) Count(player string, at int64) int {
	g.life.RLock()
	defer g.life.RUnlock()

	value, ok := g.players.Load(player)
	if !ok {
		return 0
	}
	s := value.(*comboState)
	s.mu.Lock()
	defer s.mu.Unlock()

	g.refreshLocked(s, at)
	return s.count
}

// ExpiresAt reports when the combo window of one player closes.
func (g *ComboCounterGuard) ExpiresAt(player string, at int64) int64 {
	g.life.RLock()
	defer g.life.RUnlock()

	value, ok := g.players.Load(player)
	if !ok {
		return 0
	}
	s := value.(*comboState)
	s.mu.Lock()
	defer s.mu.Unlock()

	g.refreshLocked(s, at)
	if s.count <= 0 {
		return 0
	}
	return s.expiresAt
}

// Snapshot serialises the guard state. It is exclusive with writes.
func (g *ComboCounterGuard) Snapshot() ([]byte, error) {
	g.life.Lock()
	defer g.life.Unlock()

	doc := snapshotEnvelope{
		Magic:   snapshotMagic,
		Version: snapshotVersion,
		Players: make([]snapshotEntry, 0),
	}
	g.players.Range(func(key, value any) bool {
		player := key.(string)
		s := value.(*comboState)
		s.mu.Lock()
		defer s.mu.Unlock()

		doc.Players = append(doc.Players, snapshotEntry{
			Player:          player,
			Target:          s.target,
			Count:           s.count,
			ExpiresAt:       s.expiresAt,
			LastHitAt:       s.lastHitAt,
			LastSeenAt:      s.lastSeenAt,
			Extended:        s.extended,
			ControlledUntil: s.controlledUntil,
			ControlFrom:     s.controlFrom,
			LastBreak:       s.lastBreak,
		})
		return true
	})
	return json.Marshal(doc)
}

// Restore replaces the guard state from a snapshot payload. A bad payload
// returns ErrBadSnapshot before any existing state is touched; a valid payload
// replaces the whole player set atomically behind the exclusive barrier.
func (g *ComboCounterGuard) Restore(payload []byte) error {
	var doc snapshotEnvelope
	if err := json.Unmarshal(payload, &doc); err != nil {
		return fmt.Errorf("%w: %v", ErrBadSnapshot, err)
	}
	if doc.Magic != snapshotMagic || doc.Version != snapshotVersion {
		return fmt.Errorf("%w: unsupported snapshot format", ErrBadSnapshot)
	}

	seen := make(map[string]struct{}, len(doc.Players))
	next := make(map[string]*comboState, len(doc.Players))
	for _, e := range doc.Players {
		if e.Player == "" {
			return fmt.Errorf("%w: snapshot entry missing player", ErrBadSnapshot)
		}
		if _, dup := seen[e.Player]; dup {
			return fmt.Errorf("%w: duplicate player %q", ErrBadSnapshot, e.Player)
		}
		seen[e.Player] = struct{}{}
		if e.Count < 0 || e.ExpiresAt < 0 || e.LastHitAt < 0 || e.LastSeenAt < 0 ||
			e.Extended < 0 || e.ControlledUntil < 0 || e.ControlFrom < 0 {
			return fmt.Errorf("%w: negative snapshot field for %q", ErrBadSnapshot, e.Player)
		}

		count := e.Count
		if g.opts.MaxCombo > 0 && count > g.opts.MaxCombo {
			count = g.opts.MaxCombo
		}
		next[e.Player] = &comboState{
			target:          e.Target,
			count:           count,
			expiresAt:       e.ExpiresAt,
			lastHitAt:       e.LastHitAt,
			lastSeenAt:      e.LastSeenAt,
			extended:        e.Extended,
			controlledUntil: e.ControlledUntil,
			controlFrom:     e.ControlFrom,
			lastBreak:       e.LastBreak,
		}
	}

	g.life.Lock()
	defer g.life.Unlock()

	g.players.Range(func(key, _ any) bool {
		g.players.Delete(key)
		return true
	})
	for player, s := range next {
		g.players.Store(player, s)
	}
	return nil
}
