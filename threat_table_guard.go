package task

import (
	"encoding/json"
	"errors"
	"sort"
	"sync"
)

// Sentinels reported by the threat table guard.
var (
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

func (g *ThreatTableGuard) stateFor(target string) *targetState {
	ts, ok := g.targets[target]
	if !ok {
		ts = &targetState{entries: make(map[string]*Entry)}
		g.targets[target] = ts
	}
	return ts
}

func (ts *targetState) entryFor(player string) *Entry {
	e, ok := ts.entries[player]
	if !ok {
		e = &Entry{Player: player, InRange: true, Visible: true}
		ts.entries[player] = e
	}
	return e
}

// topEntry returns the entry with the highest threat, breaking ties by
// ascending player name so the result never depends on map iteration order.
func (ts *targetState) topEntry() *Entry {
	var best *Entry
	for _, e := range ts.entries {
		if best == nil || e.Threat > best.Threat ||
			(e.Threat == best.Threat && e.Player < best.Player) {
			best = e
		}
	}
	return best
}

func (ts *targetState) resetHysteresis() {
	ts.candidate = ""
	ts.candidateSince = 0
}

// AddDamage converts damage into threat for one player.
func (g *ThreatTableGuard) AddDamage(target, player string, amount int64) error {
	if target == "" || player == "" {
		return ErrInvalidRequest
	}
	if amount <= 0 {
		return ErrInvalidAmount
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	ts := g.stateFor(target)
	e := ts.entryFor(player)
	e.Threat += amount * int64(g.opts.DamageFactorPct) / 100
	return nil
}

// AddHeal converts healing into threat split between healer and current target.
func (g *ThreatTableGuard) AddHeal(target, healer string, amount int64) error {
	if target == "" || healer == "" {
		return ErrInvalidRequest
	}
	if amount <= 0 {
		return ErrInvalidAmount
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	ts := g.stateFor(target)

	denominator := int64(100)
	if ratioSum := int64(g.opts.HealCasterPct + g.opts.HealTargetPct); ratioSum > 100 {
		denominator = ratioSum
	}
	casterThreat := amount * int64(g.opts.HealCasterPct) / denominator
	targetThreat := amount * int64(g.opts.HealTargetPct) / denominator

	recipient := ts.sticky
	if recipient == "" || ts.entries[recipient] == nil {
		if top := ts.topEntry(); top != nil {
			recipient = top.Player
		} else {
			recipient = healer
		}
	}

	ts.entryFor(healer).Threat += casterThreat
	ts.entryFor(recipient).Threat += targetThreat
	return nil
}

// Taunt forces the target to stay on the player until the given millisecond.
func (g *ThreatTableGuard) Taunt(target, player string, until int64) error {
	if target == "" || player == "" {
		return ErrInvalidRequest
	}
	if until <= 0 {
		return ErrInvalidTaunt
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	ts := g.stateFor(target)
	ts.tauntPlayer = player
	ts.tauntUntil = until
	ts.sticky = player
	ts.resetHysteresis()
	return nil
}

// Current reports the player the target is locked on at the given millisecond.
func (g *ThreatTableGuard) Current(target string, at int64) (string, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	ts, ok := g.targets[target]
	if !ok || len(ts.entries) == 0 {
		return "", false
	}

	if ts.tauntPlayer != "" {
		if at < ts.tauntUntil {
			return ts.tauntPlayer, true
		}
		// The taunt window has ended: recompute the lock from threat alone
		// instead of lingering on the taunting player.
		ts.tauntPlayer = ""
		ts.tauntUntil = 0
		ts.sticky = ""
		ts.resetHysteresis()
	}

	top := ts.topEntry()
	if top == nil {
		return "", false
	}

	if ts.sticky == "" || ts.entries[ts.sticky] == nil {
		ts.sticky = top.Player
		ts.resetHysteresis()
		return ts.sticky, true
	}

	currentThreat := ts.entries[ts.sticky].Threat
	challenges := top.Player != ts.sticky &&
		top.Threat*100 > currentThreat*int64(g.opts.SwitchRatioPct)

	switch {
	case !challenges:
		ts.resetHysteresis()
	case ts.candidate != top.Player:
		ts.candidate = top.Player
		ts.candidateSince = at
	case at-ts.candidateSince >= g.opts.StickMs:
		ts.sticky = top.Player
		ts.resetHysteresis()
	}
	return ts.sticky, true
}

// Transfer moves a percentage of one player's threat to another player.
func (g *ThreatTableGuard) Transfer(from, to string, pct int) error {
	if from == "" || to == "" {
		return ErrInvalidRequest
	}
	if pct < 1 || pct > 100 {
		return ErrInvalidTransfer
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	found := false
	for _, ts := range g.targets {
		source, ok := ts.entries[from]
		if !ok {
			continue
		}
		found = true
		moved := source.Threat * int64(pct) / 100
		source.Threat -= moved
		ts.entryFor(to).Threat += moved
	}
	if !found {
		return ErrUnknownPlayer
	}
	return nil
}

// Tick decays the entries of out of range or invisible players.
func (g *ThreatTableGuard) Tick(at int64) {
	g.mu.Lock()
	defer g.mu.Unlock()

	keep := int64(100 - g.opts.DecayPct)
	if keep < 0 {
		keep = 0
	}
	for _, ts := range g.targets {
		for _, e := range ts.entries {
			if !e.InRange || !e.Visible {
				e.Threat = e.Threat * keep / 100
			}
		}
	}
}

// Table returns a stable sorted copy of one target's threat table.
func (g *ThreatTableGuard) Table(target string) []Entry {
	g.mu.Lock()
	defer g.mu.Unlock()

	out := []Entry{}
	ts, ok := g.targets[target]
	if !ok {
		return out
	}
	for _, e := range ts.entries {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Threat != out[j].Threat {
			return out[i].Threat > out[j].Threat
		}
		return out[i].Player < out[j].Player
	})
	return out
}

// SetInRange marks whether a player counts as in range for the target.
func (g *ThreatTableGuard) SetInRange(target, player string, inRange bool) error {
	if target == "" || player == "" {
		return ErrInvalidRequest
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	ts, ok := g.targets[target]
	if !ok {
		return ErrUnknownTarget
	}
	e, ok := ts.entries[player]
	if !ok {
		return ErrUnknownPlayer
	}
	e.InRange = inRange
	return nil
}

// SetVisible marks whether a player is visible to the target.
func (g *ThreatTableGuard) SetVisible(target, player string, visible bool) error {
	if target == "" || player == "" {
		return ErrInvalidRequest
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	ts, ok := g.targets[target]
	if !ok {
		return ErrUnknownTarget
	}
	e, ok := ts.entries[player]
	if !ok {
		return ErrUnknownPlayer
	}
	e.Visible = visible
	return nil
}

type snapshotTarget struct {
	Entries        []Entry `json:"entries"`
	Sticky         string  `json:"sticky"`
	Candidate      string  `json:"candidate"`
	CandidateSince int64   `json:"candidate_since"`
	TauntPlayer    string  `json:"taunt_player"`
	TauntUntil     int64   `json:"taunt_until"`
}

type snapshotPayload struct {
	Version int                       `json:"version"`
	Targets map[string]snapshotTarget `json:"targets"`
}

// Snapshot serialises the guard state.
func (g *ThreatTableGuard) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	payload := snapshotPayload{Version: 1, Targets: make(map[string]snapshotTarget)}
	for name, ts := range g.targets {
		st := snapshotTarget{
			Sticky:         ts.sticky,
			Candidate:      ts.candidate,
			CandidateSince: ts.candidateSince,
			TauntPlayer:    ts.tauntPlayer,
			TauntUntil:     ts.tauntUntil,
		}
		for _, e := range ts.entries {
			st.Entries = append(st.Entries, *e)
		}
		payload.Targets[name] = st
	}
	return json.Marshal(payload)
}

// Restore replaces the guard state from a snapshot payload.
func (g *ThreatTableGuard) Restore(payload []byte) error {
	if len(payload) == 0 {
		return ErrBadSnapshot
	}
	var snap snapshotPayload
	if err := json.Unmarshal(payload, &snap); err != nil || snap.Targets == nil {
		return ErrBadSnapshot
	}

	next := make(map[string]*targetState, len(snap.Targets))
	for name, st := range snap.Targets {
		ts := &targetState{
			entries:        make(map[string]*Entry, len(st.Entries)),
			sticky:         st.Sticky,
			candidate:      st.Candidate,
			candidateSince: st.CandidateSince,
			tauntPlayer:    st.TauntPlayer,
			tauntUntil:     st.TauntUntil,
		}
		for i := range st.Entries {
			entry := st.Entries[i]
			ts.entries[entry.Player] = &entry
		}
		next[name] = ts
	}

	g.mu.Lock()
	g.targets = next
	g.mu.Unlock()
	return nil
}
